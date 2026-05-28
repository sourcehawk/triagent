package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// playbookIDPattern bounds what the launcher will accept as a playbook id
// when writing a user file. Mirrors the loose convention the embedded
// playbooks follow (lowercase + underscores) and prevents path traversal
// or shell-meaningful characters from landing in the filename.
var playbookIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// userPlaybookGroup is the per-id view used by the list API.
// Each id has exactly one file on disk (<type>/<id>.yaml); git history
// is the record of previous versions. Type carries the directory the
// id lives in (directory-as-type layout). Active mirrors the file's
// `active` field (absent = true). Path is the absolute path on disk.
type userPlaybookGroup struct {
	Type   string
	YAML   string
	Active bool
	Path   string
}

// loadUserPlaybookGroups walks <dir>/<type>/<id>.yaml and returns one
// entry per id. Files that don't match the <id>.yaml pattern (wrong
// id shape, directories, non-yaml files) are silently skipped.
func loadUserPlaybookGroups(dir string) (map[string]userPlaybookGroup, error) {
	out := map[string]userPlaybookGroup{}
	if dir == "" {
		return out, nil
	}
	typeEntries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, td := range typeEntries {
		if !td.IsDir() || !typePattern.MatchString(td.Name()) {
			continue
		}
		typeDir := filepath.Join(dir, td.Name())
		fileEntries, err := os.ReadDir(typeDir)
		if err != nil {
			continue
		}
		for _, f := range fileEntries {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".yaml")
			if !playbookIDPattern.MatchString(id) {
				continue
			}
			path := filepath.Join(typeDir, f.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var head struct {
				ID     string `yaml:"id"`
				Active *bool  `yaml:"active"`
			}
			if err := yaml.Unmarshal(data, &head); err != nil || head.ID != id {
				continue
			}
			active := true
			if head.Active != nil {
				active = *head.Active
			}
			out[id] = userPlaybookGroup{
				Type:   td.Name(),
				YAML:   string(data),
				Active: active,
				Path:   path,
			}
		}
	}
	return out, nil
}

// typePattern restricts what counts as a type subdirectory. Same
// shape as a playbook id; rejects proposals/, .git, dot-dirs,
// build outputs.
var typePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// writeUserPlaybook validates the YAML via `triagent-mcp validate-playbook`,
// writes the file via `triagent-mcp write-user-playbook`, then commits the
// change in the user dir's git repo. message is the operator's
// commit message (auto-generated when empty). Skips the commit if
// the post-write file content matches HEAD.
func writeUserPlaybook(ctx context.Context, dir, mcpBin, typeName, id, body string, activate bool, message string) (validationErrs []string, err error) {
	if dir == "" {
		return nil, fmt.Errorf("user playbooks dir is not configured")
	}
	if !typePattern.MatchString(typeName) {
		return nil, fmt.Errorf("invalid type %q", typeName)
	}
	if !playbookIDPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid playbook id %q (allowed: alphanumeric, _-, up to 64 chars)", id)
	}
	bodyID, parseErr := parseYAMLPlaybookID(body)
	if parseErr != nil {
		return []string{parseErr.Error()}, nil
	}
	if bodyID != id {
		return []string{fmt.Sprintf("YAML id %q does not match URL id %q", bodyID, id)}, nil
	}
	if errs, err := validatePlaybookViaSubprocess(ctx, mcpBin, body); err != nil {
		return nil, fmt.Errorf("invoke validator: %w", err)
	} else if len(errs) > 0 {
		return errs, nil
	}
	// Read the current file before writing so we can detect active-only
	// flips and emit the right commit message (update / enabled / disabled).
	target := filepath.Join(dir, typeName, id+".yaml")
	oldBody := ""
	if data, readErr := os.ReadFile(target); readErr == nil {
		oldBody = string(data)
	}

	if errs, err := writeUserPlaybookViaSubprocess(ctx, mcpBin, dir, typeName, id, body, activate); err != nil {
		return nil, err
	} else if len(errs) > 0 {
		return errs, nil
	}

	// Classify the change for the auto-commit message.
	relPath := filepath.Join(typeName, id+".yaml")
	autoMsg := fmt.Sprintf("%s: update", id)
	if oldBody != "" {
		// Compare semantic bodies (active + version stripped) to detect
		// active-only flips. playbookBodiesMatch strips both fields.
		newBody, _ := os.ReadFile(target)
		if playbookBodiesMatch(oldBody, string(newBody)) {
			// Body unchanged; this is an active-only flip.
			if activate {
				autoMsg = fmt.Sprintf("%s: enabled", id)
			} else {
				autoMsg = fmt.Sprintf("%s: disabled", id)
			}
		}
	} else if !activate {
		// No prior file + activate=false: seed-from-system inactive path.
		autoMsg = fmt.Sprintf("%s: disabled", id)
	}

	commitMsg := strings.TrimSpace(message)
	if commitMsg == "" {
		commitMsg = autoMsg
	}
	if err := commitPlaybookChange(ctx, dir, relPath, commitMsg); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: commit %s: %v — file written but not recorded in history\n", relPath, err)
	}
	return nil, nil
}

// writeUserPlaybookViaSubprocess runs `triagent-mcp write-user-playbook
// <dir> <type> <id>` with the YAML on stdin. activate maps to
// --activate=false when the operator staged a disable.
func writeUserPlaybookViaSubprocess(ctx context.Context, mcpBin, dir, typeName, id, body string, activate bool) (validationErrs []string, err error) {
	if mcpBin == "" {
		return nil, fmt.Errorf("triagent-mcp binary path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"write-user-playbook", dir, typeName, id}
	if !activate {
		args = append(args, "--activate=false")
	}
	var stdout, stderr bytes.Buffer
	runErr := runCmdWithETXTBSYRetry(func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, mcpBin, args...)
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return nil, fmt.Errorf("run write-user-playbook: %w (stderr: %s)", runErr, stderr.String())
	}
	if exitErr != nil && exitErr.ExitCode() == 2 {
		var result struct{ Error string `json:"error"` }
		_ = json.Unmarshal(stdout.Bytes(), &result)
		if result.Error == "" {
			result.Error = stderr.String()
		}
		return nil, fmt.Errorf("write-user-playbook hard error: %s", result.Error)
	}
	var result struct {
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("decode write-user-playbook output: %w (stdout: %s)", err, stdout.String())
	}
	if result.OK {
		return nil, nil
	}
	if len(result.Errors) > 0 {
		return result.Errors, nil
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return []string{"write failed (no detail)"}, nil
}

// promoteProposalViaSubprocess runs `triagent-mcp promote-proposal <dir>
// <proposal_id>`. Always activates the promoted version — approve is a
// single action (write + activate); decline is the only alternative.
// On success returns (id, type, version, activated, nil);
// on validation failure returns ("", "", "", false, []errs, nil);
// on hard error returns ("", "", "", false, nil, err).
func promoteProposalViaSubprocess(ctx context.Context, mcpBin, dir, proposalID string) (id, typeName, version string, activated bool, validationErrs []string, err error) {
	if mcpBin == "" {
		return "", "", "", false, nil, fmt.Errorf("triagent-mcp binary path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"promote-proposal", dir, proposalID}
	var stdout, stderr bytes.Buffer
	runErr := runCmdWithETXTBSYRetry(func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, mcpBin, args...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return "", "", "", false, nil, fmt.Errorf("run promote-proposal: %w (stderr: %s)", runErr, stderr.String())
	}
	var result struct {
		OK        bool     `json:"ok"`
		ID        string   `json:"id"`
		Type      string   `json:"type"`
		Version   string   `json:"version"`
		Activated bool     `json:"activated"`
		Errors    []string `json:"errors"`
		Error     string   `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", "", "", false, nil, fmt.Errorf("decode promote-proposal output: %w (stdout: %s)", err, stdout.String())
	}
	if exitErr != nil && exitErr.ExitCode() == 2 {
		msg := result.Error
		if msg == "" {
			msg = stderr.String()
		}
		return "", "", "", false, nil, fmt.Errorf("promote-proposal hard error: %s", msg)
	}
	if result.OK {
		return result.ID, result.Type, result.Version, result.Activated, nil, nil
	}
	if len(result.Errors) > 0 {
		return "", "", "", false, result.Errors, nil
	}
	return "", "", "", false, nil, fmt.Errorf("promote-proposal failed (no detail)")
}

// deleteProposalViaSubprocess runs `triagent-mcp delete-proposal <dir>
// <proposal_id>`. Idempotent; missing drafts return ok=true.
func deleteProposalViaSubprocess(ctx context.Context, mcpBin, dir, proposalID string) error {
	if mcpBin == "" {
		return fmt.Errorf("triagent-mcp binary path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	err := runCmdWithETXTBSYRetry(func() *exec.Cmd {
		stderr.Reset()
		cmd := exec.CommandContext(ctx, mcpBin, "delete-proposal", dir, proposalID)
		cmd.Stderr = &stderr
		return cmd
	})
	if err != nil {
		return fmt.Errorf("delete-proposal: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}


// removeIDFromOtherTypes deletes the <type>/<id>.yaml file for `id`
// in every type directory that differs from `keepType`. Used by the
// save handler to clean up orphaned files when an operator changes the
// type slot of an existing playbook — without this, the loader's
// invariant "same id can't span types" would be violated.
//
// Returns the count of files removed and the first I/O error if any.
// A missing user dir is not an error (no work to do).
func removeIDFromOtherTypes(dir, id, keepType string) (removed int, err error) {
	if dir == "" {
		return 0, nil
	}
	if !playbookIDPattern.MatchString(id) {
		return 0, fmt.Errorf("invalid playbook id %q", id)
	}
	if !typePattern.MatchString(keepType) {
		return 0, fmt.Errorf("invalid keep type %q", keepType)
	}
	typeDirs, dirErr := os.ReadDir(dir)
	if errors.Is(dirErr, fs.ErrNotExist) {
		return 0, nil
	}
	if dirErr != nil {
		return 0, fmt.Errorf("read user playbooks dir: %w", dirErr)
	}
	target := id + ".yaml"
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		typeName := td.Name()
		if typeName == keepType || !typePattern.MatchString(typeName) {
			continue
		}
		path := filepath.Join(dir, typeName, target)
		rmErr := os.Remove(path)
		if rmErr == nil {
			removed++
		} else if !errors.Is(rmErr, fs.ErrNotExist) {
			return removed, fmt.Errorf("remove %s: %w", path, rmErr)
		}
	}
	return removed, nil
}

// deleteUserPlaybook removes the user file for <id> at <type>/<id>.yaml.
// If a system or upstream playbook with the same id exists, the loader
// falls back to that on next session start.
//
// Returns fs.ErrNotExist when no user file matches the id.
func deleteUserPlaybook(dir, id string) error {
	if dir == "" {
		return fmt.Errorf("user playbooks dir is not configured")
	}
	if !playbookIDPattern.MatchString(id) {
		return fmt.Errorf("invalid playbook id %q", id)
	}
	groups, err := loadUserPlaybookGroups(dir)
	if err != nil {
		return err
	}
	g, ok := groups[id]
	if !ok || g.Path == "" {
		return fs.ErrNotExist
	}
	return os.Remove(g.Path)
}

// parseYAMLPlaybookID extracts just the top-level `id:` field. Lighter
// than a full Playbook decode and gives a clearer error if the file is
// malformed.
func parseYAMLPlaybookID(body string) (string, error) {
	var head struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return "", fmt.Errorf("parse YAML: %w", err)
	}
	if head.ID == "" {
		return "", fmt.Errorf("playbook id is empty")
	}
	return head.ID, nil
}

// playbookBodiesMatch reports whether two playbook YAML bodies are
// semantically equivalent ignoring the `active` and `version`
// fields. Used by the unsynced-vs-upstream and push-PR-gating
// checks: upstream files don't carry active/version stamps, while
// user files always do, so a byte-compare always disagrees. Diff
// at the structural level instead — equivalent under
// reordering/whitespace, and active/version stripped on both sides
// before the comparison.
//
// Returns false on parse errors (we can't safely declare equality
// when we can't read one of them — caller treats that as "different
// / unsynced", which is the conservative answer).
//
// Stripped on both sides — these are infrastructure stamps or
// directory-derived metadata, not operator intent:
//   - active: stamped at write time (writeUserPlaybook / push-PR).
//   - version: launcher-side cache stamp.
//   - type: redundant with the directory the file lives in. The editor
//     injects it into draft.type from the API response, so dumpPlaybookYAML
//     emits it on push, but writeUserPlaybook drops it on save. Without
//     stripping here every push leaves the list claiming drift forever
//     even though the editor sees no diff.
func playbookBodiesMatch(a, b string) bool {
	stripped := func(body string) (map[string]any, bool) {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(body), &m); err != nil || m == nil {
			return nil, false
		}
		for _, k := range transientPlaybookFields {
			delete(m, k)
		}
		return m, true
	}
	ma, ok := stripped(a)
	if !ok {
		return false
	}
	mb, ok := stripped(b)
	if !ok {
		return false
	}
	// Re-marshal canonically and compare bytes. yaml.v3 sorts map
	// keys deterministically, so reordering on disk doesn't trip the
	// comparison.
	ya, err := yaml.Marshal(ma)
	if err != nil {
		return false
	}
	yb, err := yaml.Marshal(mb)
	if err != nil {
		return false
	}
	return bytes.Equal(ya, yb)
}

// transientPlaybookFields are dropped wherever a playbook YAML is
// canonicalised (push-write + drift-compare). They are written by
// infrastructure (active/version stamps) or derived from the file's
// directory (type), not by the operator. Keeping them in the body
// would create round-trip drift between the user save path (which
// doesn't write them) and the push path (which used to). Single
// list = single rule; the alternative is each call site forgetting
// one field, which is exactly the bug class we just hit with `type`.
var transientPlaybookFields = []string{"active", "version", "type"}

// stripVersionFromPlaybookYAML round-trips body through yaml.v3,
// dropping the transient/redundant fields enumerated in
// transientPlaybookFields. Used by push-PR so launcher stamps and
// directory-derived metadata don't leak into upstream diffs and so
// the post-pull file matches what the user-side save path would have
// produced.
func stripVersionFromPlaybookYAML(body string) (string, error) {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		return "", err
	}
	for _, k := range transientPlaybookFields {
		delete(m, k)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// validatePlaybookViaSubprocess runs `triagent-mcp validate-playbook` with the
// supplied YAML on stdin. Returns (nil, nil) when the input is valid;
// (errs, nil) when triagent-mcp produced structured validation errors;
// (nil, err) when the subprocess itself failed (binary missing, JSON
// garbage, etc.).
func validatePlaybookViaSubprocess(ctx context.Context, mcpBin, body string) ([]string, error) {
	if mcpBin == "" {
		return nil, fmt.Errorf("triagent-mcp binary path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := runCmdWithETXTBSYRetry(func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, mcpBin, "validate-playbook")
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	// validate-playbook exits 1 on validation failure (still produces
	// usable JSON on stdout), 2 on a hard error. Distinguish by exit code.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, fmt.Errorf("run validator: %w (stderr: %s)", err, stderr.String())
	}
	if exitErr != nil && exitErr.ExitCode() == 2 {
		return nil, fmt.Errorf("validator hard error (stderr: %s)", stderr.String())
	}
	var result struct {
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("decode validator output: %w (stdout: %s)", err, stdout.String())
	}
	if result.OK {
		return nil, nil
	}
	if len(result.Errors) == 0 {
		return []string{"validation failed (no detail)"}, nil
	}
	return result.Errors, nil
}
