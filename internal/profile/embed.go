package profile

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:profiles
var embedded embed.FS

// LoadEmbedded returns the named profile from the binary's embedded
// profiles directory. Returns an error if the profile is unknown.
//
// The loader reads profile.yaml from `profiles/<name>/profile.yaml`,
// then walks `profiles/<name>/prompts/*.md` into Profile.Prompts, and
// resolves `profiles/<name>/k8s/kinds.json` into Profile.KindsPath as a
// path-relative-to-the-embed-fs marker (an on-disk extraction is done by
// the launcher when it writes the per-session config).
//
// `prompt_files` and `kinds_file` declarations in profile.yaml are also
// honored: their paths are resolved relative to the profile's embedded
// dir and override the conventional locations.
func LoadEmbedded(name string) (*Profile, error) {
	dir := path.Join("profiles", name)
	yamlPath := path.Join(dir, "profile.yaml")

	data, err := fs.ReadFile(embedded, yamlPath)
	if err != nil {
		return nil, fmt.Errorf("embedded profile %q not found: %w", name, err)
	}
	p, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("embedded profile %q: %w", name, err)
	}

	// Walk prompts/*.md.
	p.Prompts = map[string]string{}
	promptsDir := path.Join(dir, "prompts")
	entries, err := fs.ReadDir(embedded, promptsDir)
	if err == nil { // OK if dir is absent in skeleton profiles
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			body, err := fs.ReadFile(embedded, path.Join(promptsDir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("embedded profile %q: read prompt %s: %w", name, e.Name(), err)
			}
			p.Prompts[e.Name()] = string(body)
		}
	}
	// Explicit prompt_files declarations win over conventional prompts/.
	for promptName, rel := range p.PromptFiles {
		body, err := fs.ReadFile(embedded, path.Join(dir, rel))
		if err != nil {
			return nil, fmt.Errorf("embedded profile %q: read prompt %s from %s: %w", name, promptName, rel, err)
		}
		p.Prompts[promptName] = string(body)
	}

	// Resolve k8s/kinds.json marker.
	kindsPath := path.Join(dir, "k8s", "kinds.json")
	if _, err := fs.Stat(embedded, kindsPath); err == nil {
		p.KindsPath = "embedded:" + kindsPath
	}
	if p.KindsFile != "" {
		resolved := path.Join(dir, p.KindsFile)
		if _, err := fs.Stat(embedded, resolved); err != nil {
			return nil, fmt.Errorf("embedded profile %q: kinds_file %s: %w", name, p.KindsFile, err)
		}
		p.KindsPath = "embedded:" + resolved
	}

	p, err = applyBase(p)
	if err != nil {
		return nil, err
	}
	normalizeRootSentinel(p)
	p.ApplyDefaults()

	return p, nil
}

// Load resolves a profile reference: a bare name maps to an embedded
// profile; a path (contains "/" or starts with ".") maps to a filesystem
// directory or yaml file.
func Load(ref string) (*Profile, error) {
	if strings.ContainsRune(ref, '/') || strings.HasPrefix(ref, ".") {
		return LoadPath(ref)
	}
	return LoadEmbedded(ref)
}

// LoadPath reads a profile from a filesystem reference. The reference
// may be either a directory containing `profile.yaml` (conventional
// layout) or a path to the yaml file itself — useful when the file is
// named something other than profile.yaml, or when you want sibling
// `architecture.md` / `kinds.json` files declared inline via
// `prompt_files` / `kinds_file` without a `prompts/` or `k8s/` subdir.
// Relative paths in declarations resolve against the directory holding
// the yaml file.
func LoadPath(ref string) (*Profile, error) {
	info, err := os.Stat(ref)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", ref, err)
	}
	var dir, yamlPath string
	if info.IsDir() {
		dir = ref
		yamlPath = filepath.Join(ref, "profile.yaml")
	} else {
		dir = filepath.Dir(ref)
		yamlPath = ref
	}
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", yamlPath, err)
	}
	p, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
	}

	p.Prompts = map[string]string{}
	promptsDir := filepath.Join(dir, "prompts")
	entries, err := os.ReadDir(promptsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			body, err := os.ReadFile(filepath.Join(promptsDir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("read prompt %s: %w", e.Name(), err)
			}
			p.Prompts[e.Name()] = string(body)
		}
	}
	// Explicit prompt_files declarations win over conventional prompts/.
	for promptName, rel := range p.PromptFiles {
		body, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return nil, fmt.Errorf("read prompt %s from %s: %w", promptName, rel, err)
		}
		p.Prompts[promptName] = string(body)
	}

	kindsPath := filepath.Join(dir, "k8s", "kinds.json")
	if _, err := os.Stat(kindsPath); err == nil {
		p.KindsPath = kindsPath
	}
	if p.KindsFile != "" {
		resolved := filepath.Join(dir, p.KindsFile)
		if _, err := os.Stat(resolved); err != nil {
			return nil, fmt.Errorf("kinds_file %s: %w", p.KindsFile, err)
		}
		p.KindsPath = resolved
	}
	// Absolutize so a triagent-mcp child running with a different cwd
	// (e.g. a session dir) can still open the file.
	if p.KindsPath != "" {
		abs, err := filepath.Abs(p.KindsPath)
		if err != nil {
			return nil, fmt.Errorf("absolutize kinds path %s: %w", p.KindsPath, err)
		}
		p.KindsPath = abs
	}

	p, err = applyBase(p)
	if err != nil {
		return nil, err
	}
	normalizeRootSentinel(p)
	p.ApplyDefaults()

	return p, nil
}

// normalizeRootSentinel rewrites the `playbooks_path` / `wiki_path` /
// `sessions_path` fields when a profile sets them to "/" — the
// "use repo root" sentinel that survives applyBase (where literal ""
// is indistinguishable from "field absent → fall back to base"). After
// normalization, runtime code sees an ordinary empty string.
func normalizeRootSentinel(p *Profile) {
	if p.Defaults.PlaybooksPath == "/" {
		p.Defaults.PlaybooksPath = ""
	}
	if p.Defaults.WikiPath == "/" {
		p.Defaults.WikiPath = ""
	}
	if p.Defaults.SessionsPath == "/" {
		p.Defaults.SessionsPath = ""
	}
}

// EmbeddedKinds returns the bytes of an embedded profile's k8s/kinds.json,
// or (nil, nil) if absent. Used by the launcher to extract the file to a
// per-session location for --crds-file.
func EmbeddedKinds(profileName string) ([]byte, error) {
	p := path.Join("profiles", profileName, "k8s", "kinds.json")
	data, err := fs.ReadFile(embedded, p)
	if err != nil {
		// fs.ErrNotExist → caller treats as "no kinds override"
		return nil, nil
	}
	return data, nil
}

// applyBase shallow-merges the override profile on top of an embedded
// base, when the override declares `base: <name>`. Top-level fields and
// arrays follow replace-on-presence semantics: if a field is non-zero
// (or a slice non-nil), the override wins; otherwise the base's value
// fills in.
//
// "Non-zero" is determined by Go's zero value for the field type. For
// strings: "" → take base. For ints: 0 → take base. For slices: nil →
// take base (an empty-but-non-nil slice counts as a deliberate clear).
func applyBase(override *Profile) (*Profile, error) {
	if override.Base == "" {
		return override, nil
	}
	base, err := LoadEmbedded(override.Base)
	if err != nil {
		return nil, fmt.Errorf("load base profile %q: %w", override.Base, err)
	}

	if override.Name == "" {
		override.Name = base.Name
	}
	if override.Description == "" {
		override.Description = base.Description
	}
	if override.Auth.Kind == "" {
		override.Auth = base.Auth
	}
	if override.Playbooks.Entrypoint == "" {
		override.Playbooks.Entrypoint = base.Playbooks.Entrypoint
	}
	if override.Playbooks.Closing == "" {
		override.Playbooks.Closing = base.Playbooks.Closing
	}
	if override.Defaults.PlaybooksRepo == "" {
		override.Defaults.PlaybooksRepo = base.Defaults.PlaybooksRepo
	}
	if override.Defaults.PlaybooksPath == "" {
		override.Defaults.PlaybooksPath = base.Defaults.PlaybooksPath
	}
	if override.Defaults.WikiRepo == "" {
		override.Defaults.WikiRepo = base.Defaults.WikiRepo
	}
	if override.Defaults.WikiPath == "" {
		override.Defaults.WikiPath = base.Defaults.WikiPath
	}
	if override.Defaults.SessionsRepo == "" {
		override.Defaults.SessionsRepo = base.Defaults.SessionsRepo
	}
	if override.Defaults.SessionsPath == "" {
		override.Defaults.SessionsPath = base.Defaults.SessionsPath
	}
	if override.Defaults.Prometheus.Service == "" {
		override.Defaults.Prometheus.Service = base.Defaults.Prometheus.Service
	}
	if override.Defaults.Prometheus.Namespace == "" {
		override.Defaults.Prometheus.Namespace = base.Defaults.Prometheus.Namespace
	}
	if override.Defaults.Prometheus.Port == 0 {
		override.Defaults.Prometheus.Port = base.Defaults.Prometheus.Port
	}
	if !override.Defaults.Auto {
		override.Defaults.Auto = base.Defaults.Auto
	}
	if !override.Defaults.Offline {
		override.Defaults.Offline = base.Defaults.Offline
	}
	override.Paths = mergePaths(base.Paths, override.Paths)
	if override.Slack.ChannelPrefix == "" {
		override.Slack.ChannelPrefix = base.Slack.ChannelPrefix
	}
	if override.LinkedRepos == nil {
		override.LinkedRepos = base.LinkedRepos
	}
	if override.ExtraMCPs == nil {
		override.ExtraMCPs = base.ExtraMCPs
	}
	if override.InvestigationInputs == nil {
		override.InvestigationInputs = base.InvestigationInputs
	}

	// Prompts: per-file fallback. If override is missing a key, fall back
	// to base's content for that key.
	if override.Prompts == nil {
		override.Prompts = map[string]string{}
	}
	for k, v := range base.Prompts {
		if _, ok := override.Prompts[k]; !ok {
			override.Prompts[k] = v
		}
	}

	if override.KindsPath == "" && base.KindsPath != "" {
		override.KindsPath = base.KindsPath
	}

	return override, nil
}
