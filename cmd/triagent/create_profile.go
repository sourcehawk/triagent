package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/spf13/cobra"
)

// profileNamePattern restricts the new profile's name (and directory
// name) to a conservative slug. The same shape downstream loaders accept
// without quoting in YAML or escaping in shell history.
var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type createProfileFlags struct {
	Name string
	Dir  string // parent directory the new profile dir lands inside; defaults to CWD
}

// createProfileCmd returns the `triagent create-profile <name>` subcommand:
// copies the embedded `default` profile into <CWD>/<name>/ so the
// operator can edit `defaults.*_repo` and prompts in place, then point
// `--profile ./<name>` at the fork. The lighter-weight alternative —
// writing a tiny overlay with `base: default` — is documented in the
// README's "Connecting upstream repos" section; this command exists for
// the heavier "I want to customise prompts and kinds.json inline" case.
func createProfileCmd() *cobra.Command {
	f := createProfileFlags{}
	cmd := &cobra.Command{
		Use:   "create-profile <name>",
		Short: "Materialise a copy of the embedded `default` profile in the current directory",
		Long: `Copy the embedded default profile (profile.yaml + prompts/) into
./<name>/, rewriting the top-level name: field to match <name>. The
result is a standalone profile ready for editing — open
./<name>/profile.yaml, fill in defaults.playbooks_repo / wiki_repo /
sessions_repo (and any prompt overrides you want), then start the
launcher with:

    triagent start --profile ./<name>

Refuses to overwrite an existing ./<name>/ directory.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			f.Name = args[0]
			return createProfile(f)
		},
	}
	return cmd
}

// createProfile is the runE body factored out for testing.
func createProfile(f createProfileFlags) error {
	if !profileNamePattern.MatchString(f.Name) {
		return fmt.Errorf("invalid profile name %q: must match [a-z0-9][a-z0-9-]*", f.Name)
	}
	dir := f.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve current dir: %w", err)
		}
		dir = cwd
	}
	target := filepath.Join(dir, f.Name)
	if err := profile.CopyEmbeddedProfile("default", target); err != nil {
		return err
	}
	if err := rewriteProfileName(filepath.Join(target, "profile.yaml"), f.Name); err != nil {
		return fmt.Errorf("rewrite name in profile.yaml: %w", err)
	}
	fmt.Printf("created profile at %s\n", target)
	fmt.Printf("edit %s/profile.yaml (esp. defaults.*_repo) and run:\n", target)
	fmt.Printf("  triagent start --profile %s\n", target)
	return nil
}

// rewriteProfileName edits the top-level `name:` line of profile.yaml in
// place. Line-stream rewrite (not yaml.Marshal) so the comments that
// document each field — the whole reason someone copies the default
// instead of writing an overlay — survive the copy.
//
// Only the FIRST top-level (column 0) `name:` line is touched. Nested
// `name:` fields (e.g. in linked_repos entries) are left alone because
// their indentation puts them past column 0.
func rewriteProfileName(path, newName string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	var (
		out      strings.Builder
		replaced bool
	)
	scanner := bufio.NewScanner(in)
	// Default scan buffer is 64KB; profile.yaml is well under that.
	for scanner.Scan() {
		line := scanner.Text()
		if !replaced && strings.HasPrefix(line, "name:") {
			out.WriteString("name: ")
			out.WriteString(newName)
			out.WriteByte('\n')
			replaced = true
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("no top-level `name:` line found")
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}
