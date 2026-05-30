package strategies

import (
	cryptoRand "crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"gopkg.in/yaml.v3"
)

// Playbook is a structured investigation flow loaded from YAML.
type Playbook struct {
	ID          string          `yaml:"id"`
	Symptom     string          `yaml:"symptom"`
	Description string          `yaml:"description,omitempty"`
	Entrypoint  string          `yaml:"entrypoint"`
	Nodes       map[string]Node `yaml:"nodes"`
	// Dispatch picks how walk_playbook handles this playbook. Empty (the
	// default) walks the agent through nodes one step at a time. "subagent"
	// hands the whole playbook to a single sub-agent run.
	Dispatch DispatchMode `yaml:"dispatch,omitempty"`
	// Version is a legacy counter (v1, v2, …). Still accepted in YAML
	// for back-compat but no longer written — git history is the record
	// of changes. Validated against v<int> when present; stripped before
	// pushing to upstream.
	Version string `yaml:"version,omitempty"`
	// Active controls whether the loader picks this version of the id.
	// Unset (nil) = default true (so embedded system playbooks load
	// without YAML noise). Per-id invariant: at most one user version
	// has active=true. A user file with active=false acts as a
	// tombstone — the loader treats the id as disabled rather than
	// falling back to the embedded version. To disable an embedded
	// playbook, write a user override with active=false.
	Active *bool `yaml:"active,omitempty"`
	// Type is a runtime field populated from the playbook's path
	// (the immediate parent directory under the plugin / user
	// playbooks dir). It is NOT serialised to YAML — the directory
	// name is the source of truth for the type, and stamping the
	// value into the YAML would just create a drift surface. The
	// loader sets this field after parsing; the renderer's
	// `yaml:"-"` tag keeps it out of round-tripped output.
	Type string `yaml:"-" json:"type,omitempty"`
	// Locked marks playbooks loaded from the launcher's bundled
	// system tier — meta-playbooks that ship with the launcher
	// binary and define the contract of the investigation system
	// itself (entrypoint, follow-up handling, proposal flow). Locked
	// entries cannot be overridden, edited, or tombstoned by user
	// dir files; the loader silently drops any user file targeting
	// a locked id. Set by LoadPlaybooksFrom; not serialised.
	Locked bool `yaml:"-" json:"locked,omitempty"`
	// SchemaVersion records which playbook contract version this
	// YAML targets. Bumped only on breaking schema changes (renamed
	// fields, removed semantics, …) — separate from `version` which
	// is the operator-iterating playbook content version. The
	// loader treats absence as CurrentSchemaVersion=1 for back-compat;
	// a future bump that drops back-compat will require an explicit
	// schema_version in every file.
	SchemaVersion int `yaml:"schema_version,omitempty"`
	// Services / Errors / Symptoms are canonical entity tags used by
	// playbook_correlate to rank candidate playbooks against a query
	// set lifted from findings. All three are optional; empty arrays
	// = "not tagged" (the playbook is invisible to correlate as a
	// direct match but can still surface via lifting from a tagged
	// child playbook). Names must match the shape enforced by
	// entities.NamePattern (^[a-z0-9][a-z0-9-]*$) — same vocabulary
	// the wiki uses, so an agent that lifted entities for wiki_correlate
	// can pass them straight to playbook_correlate.
	// The json tags are forward-looking — the Playbook struct isn't
	// currently JSON-marshalled, but playbook_correlate's output
	// (playbookCorrelateMatch) projects these fields and other
	// projections may follow.
	Services []string `yaml:"services,omitempty" json:"services,omitempty"`
	Errors   []string `yaml:"errors,omitempty"   json:"errors,omitempty"`
	Symptoms []string `yaml:"symptoms,omitempty" json:"symptoms,omitempty"`
}

// DispatchMode picks how walk_playbook should treat this playbook. Default
// walks the agent through nodes one step at a time (today's behaviour).
// "subagent" handles the entire playbook in a single sub-agent run on the
// profile's Models.Subagent default — used for structured-output proposals
// (wiki_proposal, playbook_proposal) where the multi-turn walker is wasteful.
type DispatchMode string

const (
	DispatchDefault  DispatchMode = ""
	DispatchSubagent DispatchMode = "subagent"
)

// CurrentSchemaVersion is the latest playbook contract version this
// build of triagent-mcp speaks. Bumps when we make a breaking change to
// the playbook shape (rename a field, drop an enum value, change
// semantics). Each bump must ship with a migration in the registry
// (Phase 2; empty for now).
const CurrentSchemaVersion = 1

// EffectiveSchemaVersion returns the playbook's declared schema
// version, or CurrentSchemaVersion-equivalent fallback when unset
// (for back-compat with playbooks authored before the field
// existed). Use this everywhere the schema version is consulted at
// runtime.
func (p *Playbook) EffectiveSchemaVersion() int {
	if p.SchemaVersion == 0 {
		return 1 // pre-field default; we started at v1
	}
	return p.SchemaVersion
}

// DefaultPlaybookType is the type the loader assigns when a playbook
// lives directly under the system/user dir (not in a type
// subdirectory). Used as a back-compat fallback during the migration
// to directory-as-type and for tests that supply a flat user dir.
const DefaultPlaybookType = "investigation"

// EffectiveType returns the playbook's declared type or
// DefaultPlaybookType when unset.
func (p *Playbook) EffectiveType() string {
	if p.Type == "" {
		return DefaultPlaybookType
	}
	return p.Type
}

// PlaybookType describes one of the known type slots — surfaced via
// the playbook_types MCP tool. Each type is a directory under the
// system playbooks dir; its description lives in <type>/type.txt.
// Empty / missing type.txt is allowed (description will be blank).
type PlaybookType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// IsActive returns true when the playbook should be loaded into the
// running set. Empty Active defaults to true so embedded YAML stays
// terse.
func (p *Playbook) IsActive() bool {
	if p.Active == nil {
		return true
	}
	return *p.Active
}

// boolPtr is a small helper for setting Active without a per-callsite
// temporary. Used when re-rendering YAML for the active flag stamp in
// WriteUserPlaybook.
func boolPtr(b bool) *bool { return &b }

// Node is a single step in a playbook. The walker reports its description,
// suggested_calls, and expected_findings to the agent; the agent decides which
// branch to follow by calling step_complete with the relevant findings and goto.
type Node struct {
	ID               string          `yaml:"-"`
	Description      string          `yaml:"description"`
	SuggestedCalls   []SuggestedCall `yaml:"suggested_calls,omitempty"`
	ExpectedFindings []string        `yaml:"expected_findings,omitempty"`
	Next             []Branch        `yaml:"next,omitempty"`
	TerminalAdvice   string          `yaml:"terminal_advice,omitempty"`
	// Handoff lists the playbook ids this terminal node hands off to.
	// Set on terminal nodes (no `next`) when the root cause turns out to
	// live in another playbook's domain — the agent should
	// walk_playbook against one of these. Multi-element when the
	// terminal_advice splits into "either A or B depending on …" — the
	// prose carries the why, this carries the structured pointer the UI
	// can render as a clickable link. Cross-playbook id resolution is
	// soft: a single-document validate doesn't know about other
	// playbooks, so a typoed id here surfaces at runtime via the editor's
	// listPlaybooks integration rather than as a structural error.
	Handoff []string `yaml:"handoff,omitempty"`
	// DelegateTo names another playbook to walk to its non-handoff
	// terminal as a sub-flow before resuming this node's `next`. The
	// walker pushes a return frame on Session.CallStack at advance time
	// and auto-pops on the delegate's terminal_done. Mutually exclusive
	// with terminal_advice / handoff / suggested_calls; requires `next`.
	// Cross-playbook id resolution is soft (same as Handoff): a typo
	// surfaces at runtime, not in single-doc validate.
	DelegateTo string `yaml:"delegate_to,omitempty"`
}

// SuggestedCall describes a concrete tool invocation the agent should make.
// Args may contain ${cluster_id} / ${namespace} placeholders that the walker
// substitutes at suggestion time using the active session's context.
type SuggestedCall struct {
	Tool string         `yaml:"tool"`
	Args map[string]any `yaml:"args,omitempty"`
}

// Branch is a conditional transition. Condition is descriptive prose that the
// agent reads to decide whether the branch applies; the walker does not
// evaluate it. To take a branch, the agent calls advance(node_id).
type Branch struct {
	Condition string `yaml:"condition"`
	Goto      string `yaml:"goto"`
}

// LoadPlaybooks is a thin convenience wrapper around LoadPlaybooksFrom
// that takes only a plugin dir (no system, no user overrides). Used
// by tests. Production callers go through LoadPlaybooksFrom directly
// so they can layer the system tier + user dir.
func LoadPlaybooks(pluginDir string) (map[string]*Playbook, error) {
	return LoadPlaybooksFrom(pluginDir, "")
}

// LoadPlaybooksFrom loads tiered playbook sets and returns the merged
// result.
//
// Tier order (highest priority last):
//
//  1. plugin tier — the upstream playbooks clone the
//     launcher manages (~/.config/triagent/upstream-playbooks
//     by default). Domain knowledge — overridable by user dir files.
//  2. user dirs — operator-authored overrides + new playbooks. Each
//     user dir is processed in argument order; later dirs override
//     earlier ones. Tombstones (active=false, no nodes) suppress
//     plugin ids.
//  3. system tier — launcher-bundled meta-playbooks shipped with the
//     launcher binary (the master "investigation" entrypoint, the
//     proposal/wiki/followup metas). Locked: cannot be overridden,
//     edited, or tombstoned by user-dir files. The loader silently
//     drops user-dir files targeting a locked id and stamps Locked=true
//     on every system entry so callers can render the lock state.
//
// Either pluginDir or systemDir can be empty (tests / dev setups).
//
// User dir layout (managed by the launcher's save flow):
//
//	<dir>/<type>/<id>.yaml   — single file per id; git history is the version record
//	<dir>/proposals/         — drafts; ignored by the loader
func LoadPlaybooksFrom(pluginDir, systemDir string, userDirs ...string) (map[string]*Playbook, error) {
	out := make(map[string]*Playbook)

	// Tier 1: plugin (the upstream library).
	if pluginDir != "" {
		stat, err := os.Stat(pluginDir)
		if err != nil {
			return nil, fmt.Errorf("plugin playbooks dir %s: %w", pluginDir, err)
		}
		if !stat.IsDir() {
			return nil, fmt.Errorf("plugin playbooks dir %s is not a directory", pluginDir)
		}
		// Plugin tier: soft-skip broken files. The upstream library
		// is operator-cloneable; a single malformed YAML shouldn't
		// take the strategies MCP down with it.
		plugin, err := loadSystemTypedDir(pluginDir, true)
		if err != nil {
			return nil, err
		}
		// Plugin entries with active=false in YAML are dropped from
		// the running set (experimental / WIP / disabled-by-default
		// playbooks); the launcher's list endpoint still shows them
		// so operators can override + re-enable.
		for id, pb := range plugin {
			if !pb.IsActive() {
				continue
			}
			out[id] = pb
		}
	}

	// Tier 2: user overrides. Layered over plugin entries; tombstones
	// suppress plugin ids.
	for _, dir := range userDirs {
		if dir == "" {
			continue
		}
		stat, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !stat.IsDir()) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat user playbooks dir %s: %w", dir, err)
		}
		userBooks, err := loadUserDir(dir)
		if err != nil {
			return nil, err
		}
		for id, pb := range userBooks {
			// Suppression: a user file with active=false means the
			// operator disabled this id. Drop it from the result
			// entirely — including any plugin playbook with the same id.
			if pb != nil && !pb.IsActive() {
				delete(out, id)
				continue
			}
			out[id] = pb
		}
	}

	// Tier 3: system (launcher-bundled metas). Locked: overrides
	// everything below, ignores user attempts to suppress.
	if systemDir != "" {
		stat, err := os.Stat(systemDir)
		if err != nil {
			return nil, fmt.Errorf("system playbooks dir %s: %w", systemDir, err)
		}
		if !stat.IsDir() {
			return nil, fmt.Errorf("system playbooks dir %s is not a directory", systemDir)
		}
		// System tier: hard-fail on invalid. Bundled metas ship with
		// the launcher binary; a broken one is a release bug that
		// should fail loud rather than silently degrading the
		// agent's flow contract.
		system, err := loadSystemTypedDir(systemDir, false)
		if err != nil {
			return nil, err
		}
		for id, pb := range system {
			if !pb.IsActive() {
				continue // shipped with the launcher but flagged off
			}
			pb.Locked = true
			out[id] = pb // wins over any plugin/user entry
		}
	}

	// validateSet runs across the merged set but skips suppressed
	// entries (already removed). Any remaining tombstones would have
	// failed validateOne when they were parsed individually.
	if err := validateSet(out); err != nil {
		return nil, err
	}
	return out, nil
}

// loadUserDir walks <dir>/<type>/<id>.yaml (single-file layout) and
// returns one playbook per id. The proposals/ subdirectory at every
// level is skipped. Malformed or invalid files are soft-skipped with
// a stderr warning — a hand-edited junk file shouldn't block every
// other playbook from loading.
//
// Active semantics: a file with active=false acts as a tombstone — the
// loader suppresses any plugin/system playbook with the same id rather
// than falling back to it. This is how operators turn off a system
// playbook they don't want.
//
// Per-id invariant: an id may NOT span multiple type directories in
// the same user dir. The loader rejects collisions so the type of an
// id stays unambiguous.
func loadUserDir(dir string) (map[string]*Playbook, error) {
	typeDirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read user playbooks dir %s: %w", dir, err)
	}
	out := make(map[string]*Playbook)
	idType := make(map[string]string) // id → type, for collision detection
	for _, td := range typeDirs {
		if !td.IsDir() {
			if strings.HasSuffix(td.Name(), ".yaml") {
				return nil, fmt.Errorf("user playbook %q lives at the dir root; the layout requires <type>/<id>.yaml", filepath.Join(dir, td.Name()))
			}
			continue
		}
		typeName := td.Name()
		if !playbookTypePattern.MatchString(typeName) {
			continue // proposals/, .git, dot-dirs
		}
		typeDir := filepath.Join(dir, typeName)
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", typeDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".yaml") {
				continue
			}
			fileID := strings.TrimSuffix(name, ".yaml")
			if !playbookIDPattern.MatchString(fileID) {
				// Soft-skip rather than hard-fail: legacy `<id>.<vN>.yaml`
				// files left behind by the pre-25670d8 versioned-loader
				// scheme would otherwise take the whole strategies MCP
				// down on startup, dragging every other tool with it.
				// Warn so an operator inspecting telemetry can spot the
				// orphan, but keep loading the rest of the dir.
				fmt.Fprintf(os.Stderr, "strategies: skipping user playbook %s/%s: filename stem %q is not a valid playbook id (legacy vN file? rename to <id>.yaml)\n", typeName, name, fileID)
				continue
			}
			data, err := os.ReadFile(filepath.Join(typeDir, name))
			if err != nil {
				return nil, fmt.Errorf("read %s/%s: %w", typeName, name, err)
			}
			pb, err := parsePlaybookYAML("user/"+typeName+"/"+name, data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "strategies: skipping user playbook %s/%s: %v\n", typeName, name, err)
				continue
			}
			if errs := validateOne(pb, "user/"+typeName+"/"+name); len(errs) > 0 {
				fmt.Fprintf(os.Stderr, "strategies: skipping invalid user playbook %s/%s: %s\n", typeName, name, strings.Join(errs, "; "))
				continue
			}
			if pb.ID != fileID {
				return nil, fmt.Errorf("user playbook %s/%s: filename id %q != yaml id %q", typeName, name, fileID, pb.ID)
			}
			pb.Type = typeName
			if existingType, dup := idType[pb.ID]; dup && existingType != typeName {
				return nil, fmt.Errorf("user playbook id %q exists in multiple type dirs (%s and %s); pick one", pb.ID, existingType, typeName)
			}
			idType[pb.ID] = typeName
			out[pb.ID] = pb
		}
	}
	return out, nil
}

// atomicWrite writes body to path via a temp-file + rename so a crash
// mid-write doesn't leave a half-written playbook on disk. Used by
// WriteUserPlaybook and WriteProposalDraft.
func atomicWrite(path, body string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	cleanup = false
	return nil
}

// ParseAndValidatePlaybookYAML parses one YAML document and validates it
// in isolation (id non-empty, entrypoint resolves to one of its own nodes,
// every goto resolves to one of its own nodes, no empty node descriptions).
// Used by the `triagent-mcp validate-playbook` subcommand to check a single
// playbook before the launcher writes it to the user playbooks dir; doesn't
// run cross-playbook checks because a single-doc check shouldn't fail when
// other playbooks happen to share an id (override semantics).
func ParseAndValidatePlaybookYAML(data []byte) (*Playbook, []string) {
	pb, err := parsePlaybookYAML("input", data)
	if err != nil {
		return nil, []string{err.Error()}
	}
	errs := validateOne(pb, "input")
	if len(errs) > 0 {
		return pb, errs
	}
	return pb, nil
}

// loadFromFS walks one filesystem rooted at base and returns a map of
// id → *Playbook. The label is used in error messages so callers can tell
// which source rejected which file.
// loadSystemTypedDir walks <systemDir>/<type>/*.yaml. Each immediate
// subdirectory is treated as a type slot — the playbooks inside it
// inherit the dir's name as their Type. The layout MUST be one-level
// subdirectory per type.
//
// Anything that isn't a type directory is skipped silently:
//
//   - Non-directory entries at the root (README.md, LICENSE, .gitignore,
//     CODEOWNERS, …) — these are repo-management files, not playbooks.
//   - Dot-prefixed directories (.git, .github, .github/workflows, …) —
//     the type-name regex requires [a-zA-Z0-9] as the first char, so
//     anything starting with `.` or `_` is rejected.
//   - Other special directories (build outputs, IDE folders, etc.).
//
// Per-id duplicate detection runs across types: an id may live in
// only one type slot to keep cross-references unambiguous.
func loadSystemTypedDir(systemDir string, softSkip bool) (map[string]*Playbook, error) {
	entries, err := os.ReadDir(systemDir)
	if err != nil {
		return nil, fmt.Errorf("read system playbooks dir %s: %w", systemDir, err)
	}
	out := make(map[string]*Playbook)
	for _, e := range entries {
		if !e.IsDir() {
			continue // README.md, LICENSE, .gitignore, etc.
		}
		typeName := e.Name()
		if !playbookTypePattern.MatchString(typeName) {
			continue // .git, .github, _build, dot-dirs, anything not type-shaped
		}
		typeDir := filepath.Join(systemDir, typeName)
		books, err := loadFromFS(os.DirFS(typeDir), ".", "system:"+typeName, softSkip)
		if err != nil {
			return nil, err
		}
		for id, pb := range books {
			pb.Type = typeName
			if existing, dup := out[id]; dup {
				return nil, fmt.Errorf("duplicate playbook id %q across types (%s and %s); each id must live in only one type", id, existing.Type, typeName)
			}
			out[id] = pb
		}
	}
	return out, nil
}

// playbookTypePattern restricts what a type directory name can be —
// alphanumeric + underscore + hyphen, not starting with a dot. Same
// shape as a playbook id; it ends up in the URL/filename so we
// keep it conservative.
var playbookTypePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// LoadPlaybookTypes walks <systemDir>/<type>/ and returns one entry
// per type, with the description read from <type>/type.txt (empty
// when the file is missing or unreadable). Returned in name-sorted
// order so the editor's UI is stable across reloads.
//
// Skips the same things loadSystemTypedDir does: non-directories at
// the root (README.md, LICENSE, .gitignore, …) and any directory
// whose name doesn't pass the type-name regex (.git, .github,
// dot-dirs, build outputs).
func LoadPlaybookTypes(systemDir string) ([]PlaybookType, error) {
	if systemDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(systemDir)
	if err != nil {
		return nil, fmt.Errorf("read system playbooks dir %s: %w", systemDir, err)
	}
	var out []PlaybookType
	for _, e := range entries {
		if !e.IsDir() {
			continue // README.md, LICENSE, .gitignore, etc.
		}
		name := e.Name()
		if !playbookTypePattern.MatchString(name) {
			continue // .git, .github, _build, dot-dirs, anything not type-shaped
		}
		t := PlaybookType{Name: name}
		if data, err := os.ReadFile(filepath.Join(systemDir, name, "type.txt")); err == nil {
			t.Description = strings.TrimSpace(string(data))
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// loadFromFS walks one filesystem rooted at base. softSkip controls
// what happens when a file fails parse or validate:
//
//   - softSkip=true  → log to stderr, drop the file, continue. Used
//     for the plugin tier (upstream library): a single broken file
//     in the operator's clone shouldn't block every other playbook
//     from loading. The launcher's editor surfaces broken files via
//     its own walker so the operator still sees them.
//   - softSkip=false → return error on first failure. Used for the
//     system tier: bundled metas ship with the launcher binary, so
//     a broken one is an authoring/release bug that should fail
//     loud, not silently degrade the agent.
func loadFromFS(rfs fs.FS, base, label string, softSkip bool) (map[string]*Playbook, error) {
	entries, err := fs.ReadDir(rfs, base)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	out := make(map[string]*Playbook)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := e.Name()
		if base != "." {
			path = base + "/" + e.Name()
		}
		data, err := fs.ReadFile(rfs, path)
		if err != nil {
			return nil, fmt.Errorf("read %s/%s: %w", label, e.Name(), err)
		}
		pb, parseErr := parsePlaybookYAML(label+"/"+e.Name(), data)
		if parseErr != nil {
			if softSkip {
				fmt.Fprintf(os.Stderr, "strategies: skipping unparseable playbook %s/%s: %v\n", label, e.Name(), parseErr)
				continue
			}
			return nil, parseErr
		}
		if errs := validateOne(pb, label+"/"+e.Name()); len(errs) > 0 {
			if softSkip {
				fmt.Fprintf(os.Stderr, "strategies: skipping invalid playbook %s/%s: %s\n", label, e.Name(), strings.Join(errs, "; "))
				continue
			}
			return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		if existing, dup := out[pb.ID]; dup {
			return nil, fmt.Errorf("duplicate playbook id %q in %s (already loaded from %s/<earlier>)", pb.ID, label, existing.ID)
		}
		out[pb.ID] = pb
	}
	return out, nil
}

// parsePlaybookYAML decodes one YAML document into a Playbook and stamps
// each node with its map key as its ID for downstream convenience.
func parsePlaybookYAML(label string, data []byte) (*Playbook, error) {
	var pb Playbook
	if err := yaml.Unmarshal(data, &pb); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	for id, node := range pb.Nodes {
		node.ID = id
		pb.Nodes[id] = node
	}
	return &pb, nil
}

// validateOne checks a single playbook in isolation. Returns a slice of
// human-readable error strings — empty if everything's fine.
//
// The checks are sized to catch the structural mistakes an LLM-authored
// draft is most likely to make. Anything that would crash the editor's
// graph layout, the runtime walker, or filename construction belongs
// here; soft authoring conventions (e.g. preferring snake_case) belong
// in the prompt, not the validator.
func validateOne(pb *Playbook, label string) []string {
	var errs []string
	if pb.ID == "" {
		errs = append(errs, fmt.Sprintf("%s: missing id", label))
	} else if !playbookIDPattern.MatchString(pb.ID) {
		errs = append(errs, fmt.Sprintf("%s: id %q is not a valid playbook id (allowed: alphanumeric + _- starting with alphanum, max 64 chars)", label, pb.ID))
	}
	if pb.Symptom == "" {
		errs = append(errs, fmt.Sprintf("%s: missing symptom", label))
	}
	// version field is a legacy stamp; when present it must match the strict
	// v<int> pattern (for any files predating the single-file migration).
	if pb.Version != "" {
		if !versionPattern.MatchString(pb.Version) {
			errs = append(errs, fmt.Sprintf("%s: version %q does not match v<int> (e.g. v1, v2)", label, pb.Version))
		}
	}
	// schema_version in the YAML must not be ahead of what this
	// build of triagent-mcp speaks — that means the playbook was authored
	// against a newer plugin and may use fields / semantics this
	// build doesn't understand. Hard reject so we don't silently
	// run a contract we can't honour. Older / unset is fine; the
	// UI surfaces a "migrate" prompt for it.
	if pb.SchemaVersion < 0 {
		errs = append(errs, fmt.Sprintf("%s: schema_version must be >= 1", label))
	}
	if pb.SchemaVersion > CurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("%s: schema_version %d is newer than this build of triagent-mcp (currently speaks %d) — update triagent-mcp before loading this playbook", label, pb.SchemaVersion, CurrentSchemaVersion))
	}
	switch pb.Dispatch {
	case DispatchDefault, DispatchSubagent:
		// ok
	default:
		errs = append(errs, fmt.Sprintf("%s: unknown dispatch mode %q (allowed: \"\", \"subagent\")", label, pb.Dispatch))
	}
	if pb.Entrypoint == "" {
		errs = append(errs, fmt.Sprintf("%s: missing entrypoint", label))
	} else if _, ok := pb.Nodes[pb.Entrypoint]; !ok {
		errs = append(errs, fmt.Sprintf("%s: entrypoint %q not found in nodes", label, pb.Entrypoint))
	}
	if len(pb.Nodes) == 0 {
		errs = append(errs, fmt.Sprintf("%s: no nodes defined", label))
	}
	terminalCount := 0
	for id, node := range pb.Nodes {
		if id == "" {
			errs = append(errs, fmt.Sprintf("%s: empty node id", label))
		} else if !playbookIDPattern.MatchString(id) {
			errs = append(errs, fmt.Sprintf("%s: node id %q does not match the allowed shape (alphanumeric + _- starting with alphanum, max 64 chars)", label, id))
		}
		if node.Description == "" {
			errs = append(errs, fmt.Sprintf("%s: node %q has empty description", label, id))
		}
		for i, br := range node.Next {
			if br.Goto == "" {
				errs = append(errs, fmt.Sprintf("%s: node %q next[%d] has empty goto", label, id, i))
				continue
			}
			if br.Condition == "" {
				errs = append(errs, fmt.Sprintf("%s: node %q next[%d] has empty condition — every branch must describe when it applies so the walker can pick", label, id, i))
			}
			if br.Goto == id {
				errs = append(errs, fmt.Sprintf("%s: node %q next[%d] points at itself — self-loops are not allowed; route through a distinct node", label, id, i))
			}
			if _, ok := pb.Nodes[br.Goto]; !ok {
				errs = append(errs, fmt.Sprintf("%s: node %q next[%d] goto %q does not resolve", label, id, i, br.Goto))
			}
		}
		for i, sc := range node.SuggestedCalls {
			if strings.TrimSpace(sc.Tool) == "" {
				errs = append(errs, fmt.Sprintf("%s: node %q suggested_calls[%d] has empty tool — expected `<server>/<tool>`", label, id, i))
				continue
			}
			if !strings.Contains(sc.Tool, "/") {
				errs = append(errs, fmt.Sprintf("%s: node %q suggested_calls[%d] tool %q is not in `<server>/<tool>` form", label, id, i, sc.Tool))
			}
			for argName := range sc.Args {
				if strings.TrimSpace(argName) == "" {
					errs = append(errs, fmt.Sprintf("%s: node %q suggested_calls[%d] has an empty arg name", label, id, i))
				}
			}
		}
		for i, ef := range node.ExpectedFindings {
			if strings.TrimSpace(ef) == "" {
				errs = append(errs, fmt.Sprintf("%s: node %q expected_findings[%d] is empty", label, id, i))
			}
		}
		// handoff is structural-only here: each entry must be non-empty
		// and shape-valid (same id pattern as a top-level playbook id).
		// Cross-playbook id resolution happens at the editor + runtime
		// layer, not at single-document validate time, so a target that
		// isn't loaded in this set isn't a hard error.
		for i, h := range node.Handoff {
			if strings.TrimSpace(h) == "" {
				errs = append(errs, fmt.Sprintf("%s: node %q handoff[%d] is empty", label, id, i))
				continue
			}
			if !playbookIDPattern.MatchString(h) {
				errs = append(errs, fmt.Sprintf("%s: node %q handoff[%d] %q is not a valid playbook id", label, id, i, h))
			}
		}
		if len(node.Handoff) > 0 && node.TerminalAdvice == "" {
			errs = append(errs, fmt.Sprintf("%s: node %q has handoff but no terminal_advice — handoff lives on terminal nodes", label, id))
		}
		// delegate_to: structural-only here. Each entry must be a
		// shape-valid playbook id, and the node must have `next` (the
		// resume target) and MUST NOT also carry terminal_advice,
		// handoff, or suggested_calls. Cross-playbook id resolution
		// happens at runtime; an unknown delegate target surfaces there,
		// not here.
		if node.DelegateTo != "" {
			if !playbookIDPattern.MatchString(node.DelegateTo) {
				errs = append(errs, fmt.Sprintf("%s: node %q delegate_to %q is not a valid playbook id (allowed: alphanumeric + _- starting with alphanum, max 64 chars)", label, id, node.DelegateTo))
			}
			if len(node.Next) == 0 {
				errs = append(errs, fmt.Sprintf("%s: node %q has delegate_to but no next branches — every delegate node must declare its resume target", label, id))
			}
			if node.TerminalAdvice != "" {
				errs = append(errs, fmt.Sprintf("%s: node %q has both delegate_to and terminal_advice — a delegate node returns to the parent, it cannot also be terminal", label, id))
			}
			if len(node.Handoff) > 0 {
				errs = append(errs, fmt.Sprintf("%s: node %q has both delegate_to and handoff — a delegate node returns to the parent, it cannot also hand off", label, id))
			}
			if len(node.SuggestedCalls) > 0 {
				errs = append(errs, fmt.Sprintf("%s: node %q has both delegate_to and suggested_calls — a delegate node only delegates; tool calls belong inside the delegated playbook", label, id))
			}
		}
		// Terminal = no next branches OR has terminal_advice. We don't
		// require both — some walkers leave a node with `next` AND
		// `terminal_advice` as "you can stop here OR keep going". But
		// at least one node in the playbook must be terminal so the
		// walker has somewhere to land.
		if len(node.Next) == 0 || node.TerminalAdvice != "" {
			terminalCount++
		}
	}
	// Entity tag validation: each entry must be a canonical name
	// (^[a-z0-9][a-z0-9-]*$). Reuses entities.ValidateNames so the
	// error shape — including the "did you mean" hint — is identical
	// to what wiki_correlate surfaces.
	if err := entities.ValidateNames("services", "", pb.Services); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", label, err))
	}
	if err := entities.ValidateNames("errors", "", pb.Errors); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", label, err))
	}
	if err := entities.ValidateNames("symptoms", "", pb.Symptoms); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", label, err))
	}
	if len(pb.Nodes) > 0 && terminalCount == 0 {
		errs = append(errs, fmt.Sprintf("%s: no terminal nodes — every node has `next` branches but none carries `terminal_advice`, so the walker has no landing spot", label))
	}
	// Reachability: nodes defined in the map but unreachable from the
	// entrypoint via `next` edges (handoff jumps to a different
	// playbook entirely so they don't count for this check) are dead
	// weight at best and a sign of a forgotten wire-up at worst.
	// Skip when the entrypoint is itself missing — the earlier
	// "entrypoint not found" error already covers that.
	if pb.Entrypoint != "" {
		if _, ok := pb.Nodes[pb.Entrypoint]; ok {
			reached := map[string]bool{pb.Entrypoint: true}
			frontier := []string{pb.Entrypoint}
			for len(frontier) > 0 {
				cur := frontier[len(frontier)-1]
				frontier = frontier[:len(frontier)-1]
				node := pb.Nodes[cur]
				for _, br := range node.Next {
					if br.Goto == "" {
						continue
					}
					if reached[br.Goto] {
						continue
					}
					if _, ok := pb.Nodes[br.Goto]; !ok {
						continue // dangling — already flagged above
					}
					reached[br.Goto] = true
					frontier = append(frontier, br.Goto)
				}
			}
			for id := range pb.Nodes {
				if !reached[id] {
					errs = append(errs, fmt.Sprintf("%s: node %q is unreachable from entrypoint %q (no `next` chain leads to it) — add a branch that targets it or remove the dead node", label, id, pb.Entrypoint))
				}
			}
		}
	}
	return errs
}

// validateSet runs cross-playbook checks against the merged set (currently
// only "no two playbooks share an id" — but loadFromFS already prevents
// in-source duplicates, and merge semantics are intentional for user
// overrides, so this is a thin guard against future cross-checks).
func validateSet(books map[string]*Playbook) error {
	for id, pb := range books {
		if errs := validateOne(pb, id); len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
	}
	return nil
}

// playbookIDPattern bounds what playbook_proposal will accept as an id.
// Same shape as the launcher's regex (allows alphanumeric, _, -, /,
// max 64 chars) so a YAML-declared id can't escape the playbooks dir.
var playbookIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// versionPattern validates a legacy `version:` field in a playbook file.
// Strict v<int> (no v0, no semver, no labels). Only consulted when the
// field is non-empty; new files written by WriteUserPlaybook omit it.
var versionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

// ProposalsSubdir is the user-dir subdirectory drafts live in,
// keyed by `<id>__<proposal_id>.yaml`. The loader skips this
// subdirectory entirely; only the launcher's approve flow promotes
// drafts out of proposals/ into the parent dir.
const ProposalsSubdir = "proposals"

// WriteUserPlaybook writes (or overwrites) <dir>/<typeName>/<id>.yaml
// with body, stamping active per the activate flag and stripping the
// legacy version field. No version bumping, no sibling files. Caller
// (the launcher) wraps this with git add + git commit in the user dir.
//
// Returns (validationErrs, nil) when the body fails structural
// validation (file not written); (nil, nil) on success;
// (nil, err) on I/O failure.
func WriteUserPlaybook(dir, typeName, id, body string, activate bool) (validationErrs []string, err error) {
	if dir == "" {
		return nil, fmt.Errorf("user playbooks dir is not configured")
	}
	if !playbookTypePattern.MatchString(typeName) {
		return []string{fmt.Sprintf("invalid type %q (allowed: alphanumeric, _-, up to 64 chars)", typeName)}, nil
	}
	if !playbookIDPattern.MatchString(id) {
		return []string{fmt.Sprintf("invalid playbook id %q (allowed: alphanumeric, _-, up to 64 chars)", id)}, nil
	}
	// Body-level structural validation runs before the id-mismatch
	// check below — a body with multiple problems surfaces the
	// structural ones first, which are usually what the operator
	// needs to fix.
	pb, errs := ParseAndValidatePlaybookYAML([]byte(body))
	if len(errs) > 0 {
		return errs, nil
	}
	if pb.ID != id {
		return []string{fmt.Sprintf("YAML id %q does not match argument id %q", pb.ID, id)}, nil
	}
	pb.Version = ""               // strip the legacy field
	pb.Active = boolPtr(activate) // launcher's intent overrides the body
	rendered, err := renderPlaybookYAML(pb)
	if err != nil {
		return nil, fmt.Errorf("render yaml: %w", err)
	}
	typeDir := filepath.Join(dir, typeName)
	if err := os.MkdirAll(typeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create type dir %s: %w", typeDir, err)
	}
	target := filepath.Join(typeDir, id+".yaml")
	return nil, atomicWrite(target, rendered)
}

// proposalIDPattern guards what we'll accept as a proposal id, used
// in the draft filename. Same family as the playbook id pattern so
// nothing escapes the proposals dir.
var proposalIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// NewProposalID returns a short hex id suitable for naming a draft
// file. Independent of session ids so a draft survives session
// recreation.
func NewProposalID() string {
	var b [6]byte
	_, _ = randRead(b[:])
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0xf]
	}
	return string(out)
}

// WriteProposalDraft validates the YAML and writes the draft to
// dir/proposals/<type>/<playbook_id>__<proposal_id>.yaml. Doesn't
// touch the loaded playbook set — drafts are inert until the
// launcher's approve handler promotes them.
//
// typeName is the type slot the draft belongs to (matches the
// directory the proposal would land in if approved).
func WriteProposalDraft(dir, typeName, proposalID, body string) (validationErrs []string, err error) {
	if dir == "" {
		return nil, fmt.Errorf("user playbooks dir is not configured")
	}
	if !playbookTypePattern.MatchString(typeName) {
		return nil, fmt.Errorf("invalid type %q", typeName)
	}
	if !proposalIDPattern.MatchString(proposalID) {
		return nil, fmt.Errorf("invalid proposal id %q", proposalID)
	}
	pb, errs := ParseAndValidatePlaybookYAML([]byte(body))
	if len(errs) > 0 {
		return errs, nil
	}
	if !playbookIDPattern.MatchString(pb.ID) {
		return []string{fmt.Sprintf("invalid playbook id %q", pb.ID)}, nil
	}
	proposalTypeDir := filepath.Join(dir, ProposalsSubdir, typeName)
	if err := os.MkdirAll(proposalTypeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create proposals type dir: %w", err)
	}
	target := filepath.Join(proposalTypeDir, pb.ID+"__"+proposalID+".yaml")
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("proposal %q already exists for %s", proposalID, pb.ID)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", target, err)
	}
	tmp, err := os.CreateTemp(proposalTypeDir, pb.ID+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return nil, fmt.Errorf("rename %s to %s: %w", tmpPath, target, err)
	}
	cleanup = false
	return nil, nil
}

// LoadProposalDraft reads a draft back from disk by proposal id.
// Walks every type subdirectory under proposals/ since the proposal
// id alone doesn't tell us which type slot it lives in. Returns the
// playbook id, the type slot, and the raw YAML.
func LoadProposalDraft(dir, proposalID string) (playbookID, typeName, body string, err error) {
	if dir == "" {
		return "", "", "", fmt.Errorf("user playbooks dir is not configured")
	}
	if !proposalIDPattern.MatchString(proposalID) {
		return "", "", "", fmt.Errorf("invalid proposal id %q", proposalID)
	}
	proposalsRoot := filepath.Join(dir, ProposalsSubdir)
	typeDirs, err := os.ReadDir(proposalsRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("read proposals dir: %w", err)
	}
	suffix := "__" + proposalID + ".yaml"
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		typeDir := filepath.Join(proposalsRoot, td.Name())
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(typeDir, e.Name()))
			if err != nil {
				return "", "", "", err
			}
			id := strings.TrimSuffix(e.Name(), suffix)
			return id, td.Name(), string(data), nil
		}
	}
	return "", "", "", fmt.Errorf("no draft found for proposal id %q", proposalID)
}

// DeleteProposalDraft removes the draft from disk, walking all type
// subdirs since the proposal id alone doesn't carry the type.
func DeleteProposalDraft(dir, proposalID string) error {
	if dir == "" {
		return fmt.Errorf("user playbooks dir is not configured")
	}
	if !proposalIDPattern.MatchString(proposalID) {
		return fmt.Errorf("invalid proposal id %q", proposalID)
	}
	proposalsRoot := filepath.Join(dir, ProposalsSubdir)
	typeDirs, err := os.ReadDir(proposalsRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	suffix := "__" + proposalID + ".yaml"
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		typeDir := filepath.Join(proposalsRoot, td.Name())
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
				continue
			}
			return os.Remove(filepath.Join(typeDir, e.Name()))
		}
	}
	return nil
}

// randRead is a small wrapper so the proposal-id helper doesn't pull
// crypto/rand into this file's import block. We only need
// non-cryptographic uniqueness (proposal ids are operator-local) but
// the existing newSessionID() uses crypto/rand for the same purpose;
// reuse the same call to keep one source of randomness.
func randRead(b []byte) (int, error) {
	return cryptoRand.Read(b)
}

// renderPlaybookYAML re-serialises a parsed Playbook back to YAML.
// Used by get_playbook_raw for user playbooks (we only kept their raw
// bytes for the embedded set; user files round-trip through the
// parser). Lossy on comments/exact whitespace but semantically
// identical, which is sufficient for an agent reading the current
// playbook as a base for a proposed update.
func renderPlaybookYAML(pb *Playbook) (string, error) {
	body, err := yaml.Marshal(pb)
	if err != nil {
		return "", fmt.Errorf("marshal playbook: %w", err)
	}
	return string(body), nil
}

// RawPlaybook pairs a system playbook's raw YAML with its type
// (the directory it lives in). Used by callers that need both the
// original bytes (for diff bases / editor rendering) and the type
// classifier without a second walk.
type RawPlaybook struct {
	YAML string
	Type string
}

// SystemRawPlaybooks walks <systemDir>/<type>/*.yaml and returns
// the raw YAML for each playbook keyed by id, paired with its type
// (the directory name). Used by the launcher's in-process catalog
// (internal/server/meta.go::loadMeta) so it can show operators the
// source YAML alongside the parsed graph.
//
// systemDir is the launcher-managed clone of the upstream playbooks
// repo. Empty dir → empty map. Skip rules match loadSystemTypedDir:
// non-dir entries at the root (README.md, LICENSE, …) and dir names
// that don't pass the type regex (.git, .github, dot-dirs) are
// silently ignored.
func SystemRawPlaybooks(systemDir string) (map[string]RawPlaybook, error) {
	out := make(map[string]RawPlaybook)
	if systemDir == "" {
		return out, nil
	}
	typeDirs, err := os.ReadDir(systemDir)
	if err != nil {
		return nil, fmt.Errorf("read system playbooks dir %s: %w", systemDir, err)
	}
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue // README.md, LICENSE, .gitignore, etc.
		}
		typeName := td.Name()
		if !playbookTypePattern.MatchString(typeName) {
			continue // .git, .github, dot-dirs, build outputs
		}
		typeDir := filepath.Join(systemDir, typeName)
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", typeDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(typeDir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("read %s/%s: %w", typeName, e.Name(), err)
			}
			pb, err := parsePlaybookYAML("system/"+typeName+"/"+e.Name(), data)
			if err != nil {
				return nil, err
			}
			if pb.ID == "" {
				return nil, fmt.Errorf("system %s/%s: missing id", typeName, e.Name())
			}
			out[pb.ID] = RawPlaybook{YAML: string(data), Type: typeName}
		}
	}
	return out, nil
}

// PlaybookSummary is the projection returned by list_playbooks.
type PlaybookSummary struct {
	ID          string `json:"id"`
	Symptom     string `json:"symptom"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
}

// summariesFiltered returns playbook summaries sorted by ID, narrowed by
// optional type and substring filters. When includeDescription is false the
// Description field is left empty in every returned summary.
func summariesFiltered(books map[string]*Playbook, typeFilter, nameFilter string, includeDescription bool) []PlaybookSummary {
	want := strings.ToLower(strings.TrimSpace(nameFilter))
	out := make([]PlaybookSummary, 0, len(books))
	for _, pb := range books {
		if typeFilter != "" && pb.EffectiveType() != typeFilter {
			continue
		}
		if want != "" {
			if !strings.Contains(strings.ToLower(pb.ID), want) &&
				!strings.Contains(strings.ToLower(pb.Symptom), want) {
				continue
			}
		}
		summary := PlaybookSummary{
			ID:      pb.ID,
			Symptom: pb.Symptom,
			Type:    pb.EffectiveType(),
		}
		if includeDescription {
			summary.Description = pb.Description
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
