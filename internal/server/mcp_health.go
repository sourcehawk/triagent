package server

import (
	"strings"
	"sync"
	"time"
)

// MCPCallStats is the per-alias rolling health view exposed to the
// frontend. Lock-protected so the telemetry hot path and the HTTP read
// path can coexist without surprises.
type MCPCallStats struct {
	Alias        string    `json:"alias"`
	LastSeen     time.Time `json:"lastSeen,omitempty"`
	LastSuccess  time.Time `json:"lastSuccess,omitempty"`
	LastErrorAt  time.Time `json:"lastErrorAt,omitempty"`
	LastErrorMsg string    `json:"lastErrorMsg,omitempty"`
	Calls        int       `json:"calls"`
	Errors       int       `json:"errors"`
	InFlight     int       `json:"inFlight"`
}

// mcpHealth tracks {investigationID → {alias → stats}}. Allocated lazily
// per session; cleared when the session is removed (hook into the
// existing manager.Remove or close path).
type mcpHealth struct {
	mu     sync.RWMutex
	bySess map[string]map[string]*MCPCallStats
}

func newMCPHealth() *mcpHealth {
	return &mcpHealth{bySess: make(map[string]map[string]*MCPCallStats)}
}

// aliasFromToolName extracts "triagent-k8s" from "mcp__triagent-k8s__list_resources".
// Returns "" when the wire name isn't an MCP-prefixed tool — local
// claude tools (Read/Glob/Grep/Bash) and anything else fall through.
func aliasFromToolName(toolName string) string {
	if !strings.HasPrefix(toolName, "mcp__") {
		return ""
	}
	rest := toolName[len("mcp__"):]
	idx := strings.Index(rest, "__")
	if idx <= 0 {
		return ""
	}
	return rest[:idx]
}

func (h *mcpHealth) recordStart(sessID, toolName string) {
	alias := aliasFromToolName(toolName)
	if alias == "" || sessID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	bucket, ok := h.bySess[sessID]
	if !ok {
		bucket = make(map[string]*MCPCallStats)
		h.bySess[sessID] = bucket
	}
	stats, ok := bucket[alias]
	if !ok {
		stats = &MCPCallStats{Alias: alias}
		bucket[alias] = stats
	}
	stats.LastSeen = time.Now()
	stats.InFlight++
	stats.Calls++
}

func (h *mcpHealth) recordEnd(sessID, toolName, errorText string) {
	alias := aliasFromToolName(toolName)
	if alias == "" || sessID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	bucket, ok := h.bySess[sessID]
	if !ok {
		return
	}
	stats, ok := bucket[alias]
	if !ok {
		return
	}
	now := time.Now()
	stats.LastSeen = now
	if stats.InFlight > 0 {
		stats.InFlight--
	}
	if errorText != "" {
		stats.LastErrorAt = now
		stats.LastErrorMsg = errorText
		stats.Errors++
	} else {
		stats.LastSuccess = now
	}
}

// snapshot returns a copy of the per-alias stats for sessID. Empty when
// the session has never recorded any tool calls.
func (h *mcpHealth) snapshot(sessID string) []MCPCallStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	bucket := h.bySess[sessID]
	out := make([]MCPCallStats, 0, len(bucket))
	for _, s := range bucket {
		out = append(out, *s)
	}
	return out
}

// drop removes per-session state when the investigation closes.
func (h *mcpHealth) drop(sessID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.bySess, sessID)
}
