package server

import (
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
)

func TestPlaybooksRepoFor(t *testing.T) {
	prof := &profile.Profile{
		Defaults: profile.Defaults{
			PlaybooksRepo: "test/pb",
			WikiRepo:      "test/wiki",
			SessionsRepo:  "test/sessions",
		},
	}
	if got := PlaybooksRepoFor(prof, ""); got != "test/pb" {
		t.Errorf("playbooks default = %q, want test/pb", got)
	}
	if got := PlaybooksRepoFor(prof, "override/x"); got != "override/x" {
		t.Errorf("playbooks override = %q, want override/x", got)
	}
	if got := WikiRepoFor(prof, ""); got != "test/wiki" {
		t.Errorf("wiki = %q", got)
	}
	if got := SessionsRepoFor(prof, ""); got != "test/sessions" {
		t.Errorf("sessions = %q", got)
	}
}
