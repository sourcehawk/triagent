package aws

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// managedProfilesMu serializes managed-profile writes within this process; the
// advisory file lock acquireConfigLock takes serializes them across processes.
// Together they make the read-modify-write of ~/.aws/config atomic against a
// concurrent generation for another alias (a launcher probe and a serve
// subprocess, or two AWS sources, all write the same file).
var managedProfilesMu sync.Mutex

// profileName is the AWS_PROFILE for one configured account: a deterministic
// triagent-cloud-<alias>-<account_id> so the server's ActiveTargetEnv and the
// generated ~/.aws/config block name the same profile. Exported for reuse by the
// launcher-side env builders, which must pin the same profile the provider
// generated.
func ProfileName(alias, accountID string) string {
	return profileName(alias, accountID)
}

func profileName(alias, accountID string) string {
	return "triagent-cloud-" + alias + "-" + accountID
}

// blockMarkers returns the BEGIN/END comment lines delimiting one alias's
// managed section in ~/.aws/config, so a rewrite replaces exactly that block and
// never touches operator-authored profiles or another alias's block.
func blockMarkers(alias string) (begin, end string) {
	return "# BEGIN triagent-cloud-" + alias, "# END triagent-cloud-" + alias
}

// writeManagedProfiles writes (or replaces) the managed assume-role profiles for
// one cloud source's accounts into configPath, atomically and idempotently. Each
// account gets a [profile triagent-cloud-<alias>-<account_id>] section layering
// its role_arn over sourceProfile (the operator's SSO base); triagent holds no
// credential — the aws CLI performs the assume-role from sourceProfile at run
// time. The section is bounded by # BEGIN/# END triagent-cloud-<alias> markers,
// so a rewrite replaces only that alias's block and leaves operator-authored
// profiles and other aliases' blocks untouched. Writing is tmp-file-then-rename
// so a crash never leaves a half-written config.
func writeManagedProfiles(configPath, alias, sourceProfile string, accounts []Account) error {
	begin, end := blockMarkers(alias)

	var block strings.Builder
	block.WriteString(begin)
	block.WriteString("\n")
	for _, a := range accounts {
		fmt.Fprintf(&block, "[profile %s]\n", profileName(alias, a.ID))
		fmt.Fprintf(&block, "role_arn       = %s\n", a.RoleARN)
		fmt.Fprintf(&block, "source_profile = %s\n", sourceProfile)
	}
	block.WriteString(end)
	block.WriteString("\n")

	managedProfilesMu.Lock()
	defer managedProfilesMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("aws: create config dir for %s: %w", configPath, err)
	}
	release, err := acquireConfigLock(configPath)
	if err != nil {
		return err
	}
	defer release()

	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("aws: read config %s: %w", configPath, err)
	}
	merged := replaceBlock(string(existing), begin, end, block.String())
	// Fail closed: never land content the aws CLI cannot parse. A single stray
	// line breaks every profile in the file (including the operator's own base
	// credentials), so refuse rather than write — surfacing the offending line
	// instead of a downstream "sts get-caller-identity failed".
	if bad := firstInvalidConfigLine(merged); bad != "" {
		return fmt.Errorf("aws: refusing to write %s: line %q is not valid config (expected a [section], comment, or key=value); fix it and retry", configPath, bad)
	}
	return atomicWrite(configPath, []byte(merged))
}

// acquireConfigLock takes an exclusive advisory lock on a sibling lock file so
// the read-modify-write below is atomic across processes. It returns a release
// func that unlocks and closes the lock file; the lock file itself is left in
// place (an empty marker), never the config.
func acquireConfigLock(configPath string) (func(), error) {
	lockPath := configPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("aws: open config lock %s: %w", lockPath, err)
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("aws: lock config %s: %w", lockPath, err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// replaceBlock splices block in place of any existing begin..end region in
// content, appending it (after a separating blank line) when no prior block is
// present. Lines outside the region are preserved verbatim, so operator-authored
// profiles survive.
func replaceBlock(content, begin, end, block string) string {
	bIdx := strings.Index(content, begin)
	if bIdx < 0 {
		if content == "" {
			return block
		}
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + "\n" + block
	}
	eIdx := strings.Index(content[bIdx:], end)
	if eIdx < 0 {
		// Truncated prior block (no END): replace from BEGIN to end of file.
		return content[:bIdx] + block
	}
	tailStart := bIdx + eIdx + len(end)
	tail := content[tailStart:]
	tail = strings.TrimPrefix(tail, "\n")
	return content[:bIdx] + block + tail
}

// firstInvalidConfigLine returns the first structurally-invalid line in an
// ~/.aws/config body, or "" when every line is valid. Used to fail closed
// before writing: the aws CLI rejects the whole file if any line is not blank,
// a comment, a [section] header, or a key=value (which also covers the indented
// sub-keys AWS uses for nested settings).
func firstInvalidConfigLine(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"), strings.HasPrefix(t, ";"):
		case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
		case strings.Contains(t, "="):
		default:
			return t
		}
	}
	return ""
}

// atomicWrite writes body to dst via a sibling tmp file then renames it into
// place, so a reader never observes a partially written ~/.aws/config.
func atomicWrite(dst string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("aws: create config dir for %s: %w", dst, err)
	}
	tmp := dst + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("aws: write config tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("aws: rename config %s: %w", dst, err)
	}
	return nil
}

// awsConfigPath resolves the file writeManagedProfiles writes to: AWS_CONFIG_FILE
// when set (so a deployment or test can redirect it), else $HOME/.aws/config —
// the location the aws CLI reads profiles from. The empty return (no HOME, no
// override) signals the caller to skip generation rather than write to a
// surprising path.
func awsConfigPath() string {
	if override := os.Getenv("AWS_CONFIG_FILE"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}
