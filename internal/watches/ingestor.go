package watches

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

// ingestRunsKeep caps how many per-run logs we retain on disk per
// watch. Pruning happens after each saveIngestRun. 50 is plenty for
// debugging a flaky watch without runaway disk use.
const ingestRunsKeep = 50

// IngestRunMeta is the per-run sidecar persisted to
// <dataDir>/ingest-runs/<id>.json. The launcher writes this alongside
// the raw output (<id>.log) so the UI can list recent runs cheaply
// without reading every log file.
//
// Status lifecycle:
//   "running"  — saved at agent invocation; durationMs/exitCode unset
//   "ok"       — claude returned with exit 0
//   "error"    — claude returned non-zero OR spawn failed (Error set)
//   "skipped"  — poll happened but the agent wasn't invoked
type IngestRunMeta struct {
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
	ExitCode   int       `json:"exitCode"`
	ItemCount  int       `json:"itemCount"`
	Status     string    `json:"status,omitempty"`
	Error      string    `json:"error,omitempty"`
	// UserPrompt is the batch prompt the agent received. Useful when
	// debugging "why did the agent ignore item X" — operator can see
	// exactly what claude was handed.
	UserPrompt string `json:"userPrompt,omitempty"`
	// SystemHint is the first line of the system prompt, enough to
	// confirm the prompt template version + the watch context block
	// without bloating the row.
	SystemHint string `json:"systemHint,omitempty"`
}

// IngestRunListEntry is the trimmed shape the list endpoint returns —
// metadata only, no log content (callers fetch that separately).
type IngestRunListEntry struct {
	ID string `json:"id"`
	IngestRunMeta
}

// IngestRunDetail bundles the metadata + the full log content for a
// single run. Returned by the per-run endpoint.
type IngestRunDetail struct {
	IngestRunListEntry
	Log string `json:"log"`
}

// saveIngestRun writes a run's metadata sidecar + raw log under
// <dataDir>/ingest-runs/. Filenames sort lexicographically by time so
// the list endpoint can scan the dir and take the last N entries.
// Best-effort: errors writing the run log are logged to stderr but
// don't propagate — losing an audit file is strictly less bad than
// failing the ingestion path. Returns the generated id so callers
// can later update the same row (e.g. transition "running" → "ok").
func saveIngestRun(dataDir string, meta IngestRunMeta, log []byte) string {
	runsDir := filepath.Join(dataDir, "ingest-runs")
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs mkdir: %v\n", err)
		return ""
	}
	id := meta.StartedAt.UTC().Format("20060102-150405.000000000")
	id = strings.ReplaceAll(id, ".", "-")
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs marshal: %v\n", err)
		return ""
	}
	if err := os.WriteFile(filepath.Join(runsDir, id+".json"), metaJSON, 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs write meta: %v\n", err)
		return ""
	}
	if err := os.WriteFile(filepath.Join(runsDir, id+".log"), log, 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs write log: %v\n", err)
		return id
	}
	pruneIngestRuns(runsDir, ingestRunsKeep)
	return id
}

// updateIngestRun overwrites an existing run's metadata + log.
// Used when transitioning a "running" placeholder to its terminal
// state without leaving the placeholder around as a phantom row.
func updateIngestRun(dataDir, id string, meta IngestRunMeta, log []byte) {
	runsDir := filepath.Join(dataDir, "ingest-runs")
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs marshal: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(runsDir, id+".json"), metaJSON, 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs update meta: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(runsDir, id+".log"), log, 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "ingest-runs update log: %v\n", err)
		return
	}
}

// pruneIngestRuns keeps the newest `keep` (sidecar, log) pairs in the
// runs dir, deleting the rest. Best-effort; errors are logged.
func pruneIngestRuns(runsDir string, keep int) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	ids := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	if len(ids) <= keep {
		return
	}
	sort.Strings(ids) // ascending — oldest first
	drop := ids[:len(ids)-keep]
	for _, id := range drop {
		_ = os.Remove(filepath.Join(runsDir, id+".json"))
		_ = os.Remove(filepath.Join(runsDir, id+".log"))
	}
}

// ListIngestRuns reads the per-watch ingest-runs/ directory and
// returns the newest-first entries (cap: ingestRunsKeep). Caller
// reads each .log via ReadIngestRun(id).
func ListIngestRuns(dataDir string, limit int) ([]IngestRunListEntry, error) {
	runsDir := filepath.Join(dataDir, "ingest-runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") {
			ids = append(ids, strings.TrimSuffix(n, ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // newest first
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]IngestRunListEntry, 0, len(ids))
	for _, id := range ids {
		b, err := os.ReadFile(filepath.Join(runsDir, id+".json"))
		if err != nil {
			continue
		}
		var m IngestRunMeta
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		out = append(out, IngestRunListEntry{ID: id, IngestRunMeta: m})
	}
	return out, nil
}

// ReadIngestRun returns the metadata + raw log for a single run.
// Returns os.ErrNotExist when no run with that id exists.
func ReadIngestRun(dataDir, id string) (IngestRunDetail, error) {
	runsDir := filepath.Join(dataDir, "ingest-runs")
	metaB, err := os.ReadFile(filepath.Join(runsDir, id+".json"))
	if err != nil {
		return IngestRunDetail{}, err
	}
	var m IngestRunMeta
	if err := json.Unmarshal(metaB, &m); err != nil {
		return IngestRunDetail{}, err
	}
	log, err := os.ReadFile(filepath.Join(runsDir, id+".log"))
	if err != nil && !os.IsNotExist(err) {
		return IngestRunDetail{}, err
	}
	return IngestRunDetail{
		IngestRunListEntry: IngestRunListEntry{ID: id, IngestRunMeta: m},
		Log:                string(log),
	}, nil
}

//go:embed templates/system.tmpl
var systemTmplStr string

var systemTmpl = template.Must(template.New("ingest-system").Parse(systemTmplStr))

// PromptInputs are the per-watch facts the launcher binds into the
// system prompt template before spawning the ingestion agent.
type PromptInputs struct {
	WatchName          string
	WatchKind          string
	SourceDescription  string
	KubeContexts       []string
	Repos              []string
	SlackChannels      []string
	CustomInstructions string
	WikiAvailable      bool
}

// BuildSystemPrompt renders the system prompt for a given watch context.
func BuildSystemPrompt(in PromptInputs) (string, error) {
	var buf bytes.Buffer
	if err := systemTmpl.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// BuildUserPrompt formats the batch the agent should classify on this run.
func BuildUserPrompt(items []Item) string {
	var buf bytes.Buffer
	buf.WriteString("Batch:\n\n")
	for _, it := range items {
		fmt.Fprintf(&buf, "Item %s:\n  source: %s\n  url: %s\n  title: %s\n  by: %s at %s\n  body:\n%s\n\n",
			it.ID,
			it.SourceKind,
			it.Snapshot.URL,
			truncate(it.Snapshot.Title, 200),
			it.Snapshot.Author,
			it.SourceRef.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			indent(truncate(it.Snapshot.Body+it.Snapshot.Text, 4*1024), 4),
		)
	}
	buf.WriteString("Begin.\n")
	return buf.String()
}

func indent(s string, n int) string {
	pad := bytes.Repeat([]byte{' '}, n)
	out := bytes.Buffer{}
	for _, line := range bytes.Split([]byte(s), []byte{'\n'}) {
		out.Write(pad)
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ClaudeIngestor spawns `claude -p` per batch. Implements Ingestor.
type ClaudeIngestor struct {
	ClaudeBinary   string
	BuildEnv       func(watchID string) []string
	BuildMCPConfig func(watchID, dataDir string) (string, error) // returns mcp.json path
	BuildPrompt    func(w Watch) PromptInputs
	// Publisher fires ingest_run_started / ingest_run_finished SSE
	// events around the claude invocation so the UI's runs panel can
	// flip a row to "running" the instant the agent spawns, instead of
	// polling the disk or waiting on the next watch_status tick.
	Publisher Publisher
	// Semaphore caps the number of concurrent claude processes across
	// all watches. Each Run acquires before spawning and releases when
	// claude returns. Empty channel → no cap. Sized 1+ via
	// DefaultIngestSemaphore() at construction.
	Semaphore chan struct{}
}

// DefaultMaxConcurrentIngest is the launcher-wide cap on concurrent
// ingestion-agent claude processes. N watches with ingest enabled won't
// fan out beyond this — additional polls block waiting for a slot.
// 3 keeps cost predictable while still allowing some parallelism for
// operators monitoring multiple sources.
const DefaultMaxConcurrentIngest = 3

// DefaultIngestSemaphore returns a buffered channel sized to the
// default concurrency cap. Pass it to ClaudeIngestor.Semaphore at
// construction. nil semaphore = unbounded (avoid in production).
func DefaultIngestSemaphore() chan struct{} {
	return make(chan struct{}, DefaultMaxConcurrentIngest)
}

func (c *ClaudeIngestor) Run(ctx context.Context, w Watch, items []Item, dataDir string) error {
	sys, err := BuildSystemPrompt(c.BuildPrompt(w))
	if err != nil {
		return err
	}
	mcpCfg, err := c.BuildMCPConfig(w.ID, dataDir)
	if err != nil {
		return err
	}
	user := BuildUserPrompt(items)

	// Acquire a slot on the global concurrency cap before doing
	// anything that touches claude. If the semaphore is full this
	// blocks until another run finishes; ctx cancellation drops us
	// out so the loop teardown is responsive.
	if c.Semaphore != nil {
		select {
		case c.Semaphore <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-c.Semaphore }()
	}
	// No --output-format=stream-json: we don't consume the stdout
	// stream (signals get written via the MCP loopback as side
	// effects), and recent claude versions reject the flag without
	// --verbose. Plain mode just dumps the final assistant message,
	// which we discard on success and log on error.
	//
	// Prompt is piped via stdin (rather than as a positional arg to
	// --print) so any leading "-" character in a synthesized briefing
	// doesn't trip claude's flag parser ("error: unknown option ...").
	cmd := exec.CommandContext(ctx, c.ClaudeBinary,
		"--print",
		"--append-system-prompt", sys,
		"--mcp-config", mcpCfg,
	)
	cmd.Stdin = strings.NewReader(user)
	cmd.Env = append(cmd.Env, c.BuildEnv(w.ID)...)
	cmd.Dir = dataDir
	started := time.Now().UTC()

	// Persist a "running" placeholder BEFORE invoking claude so the
	// operator can see in-flight runs in the UI. The agent can take
	// many minutes (or hang indefinitely on a bad MCP config); without
	// this, the panel was empty for the entire run and operators
	// couldn't tell whether the agent was running, failed silently, or
	// was never invoked.
	runID := saveIngestRun(dataDir, IngestRunMeta{
		StartedAt:  started,
		ItemCount:  len(items),
		Status:     "running",
		UserPrompt: user,
		SystemHint: firstLine(sys),
	}, nil)
	pub := c.Publisher
	if pub == nil {
		pub = NopPublisher{}
	}
	pub.PublishIngestRunStarted(w.ID, runID, started, len(items))

	out, runErr := cmd.CombinedOutput()
	duration := time.Since(started)

	// Persist the run regardless of outcome so operators can audit why
	// signals are empty (agent ran but didn't call any tools, claude
	// rejected its args, MCP config was wrong, etc.). Pruned to keep
	// the last ingestRunsKeep runs per watch — typical run output is a
	// few KB but a runaway agent loop could grow it.
	exitCode := 0
	status := "ok"
	errMsg := ""
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
		status = "error"
		errMsg = runErr.Error()
	}
	// Update the in-flight placeholder rather than appending a new row
	// so the run shows up exactly once in the list, transitioning from
	// "running" → "ok"/"error". If the runID is empty (placeholder
	// write failed) fall through to a fresh save.
	finalMeta := IngestRunMeta{
		StartedAt:  started,
		DurationMs: duration.Milliseconds(),
		ExitCode:   exitCode,
		ItemCount:  len(items),
		Status:     status,
		Error:      errMsg,
		UserPrompt: user,
		SystemHint: firstLine(sys),
	}
	if runID != "" {
		updateIngestRun(dataDir, runID, finalMeta, out)
	} else {
		runID = saveIngestRun(dataDir, finalMeta, out)
	}
	pub.PublishIngestRunFinished(w.ID, runID, status, duration.Milliseconds(), errMsg)

	if runErr != nil {
		return fmt.Errorf("claude ingest: %w: %s", runErr, string(out))
	}
	return nil
}

// firstLine extracts a short hint from the system prompt for the run-
// list display — full prompt is too long for the row.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// MCPConfigOpts configures WriteMCPConfig. The generated mcp.json wires the
// ingestion agent into triagent-signal-ingest (always) and triagent-wiki (when WikiPath
// is set).
type MCPConfigOpts struct {
	Dir           string
	MCPBinaryPath string
	LauncherURL   string
	LauncherToken string
	WatchID       string
	WikiPath      string
	WikiProposals string
}

// WriteMCPConfig writes the per-batch mcp.json the ingestion agent's claude
// subprocess consumes via --mcp-config. Returns the absolute path.
func WriteMCPConfig(opts MCPConfigOpts) (string, error) {
	servers := map[string]any{
		"triagent-signal-ingest": map[string]any{
			"command": opts.MCPBinaryPath,
			"args":    []string{"serve", "--kind=signal-ingest"},
			"env": map[string]string{
				"TRIAGENT_MCP_TELEMETRY_URL":   opts.LauncherURL,
				"TRIAGENT_MCP_TRACE_ID":        opts.WatchID,
				"TRIAGENT_MCP_TELEMETRY_TOKEN": opts.LauncherToken,
			},
		},
	}
	if opts.WikiPath != "" {
		servers["triagent-wiki"] = map[string]any{
			"command": opts.MCPBinaryPath,
			"args":    []string{"serve", "--kind=wiki"},
			"env": map[string]string{
				"TRIAGENT_MCP_WIKI_PATH":           opts.WikiPath,
				"TRIAGENT_MCP_WIKI_PROPOSALS_PATH": opts.WikiProposals,
			},
		}
	}
	cfg := map[string]any{"mcpServers": servers}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(opts.Dir, "mcp.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
