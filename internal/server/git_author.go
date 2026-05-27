package server

import (
	"context"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

// resolveGitAuthor reads `git config user.name` / `user.email`. When git is
// unset, falls back to the OS username + "<user>@unknown". Bounded wallclock
// so a stuck git config can't block session creation.
func resolveGitAuthor() persistedAuthor {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	name := strings.TrimSpace(runGitConfig(ctx, "user.name"))
	email := strings.TrimSpace(runGitConfig(ctx, "user.email"))
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	if email == "" {
		if u, err := user.Current(); err == nil {
			email = u.Username + "@unknown"
		} else {
			email = "unknown@unknown"
		}
	}
	return persistedAuthor{Name: name, Email: email}
}

func runGitConfig(ctx context.Context, key string) string {
	out, err := exec.CommandContext(ctx, "git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
