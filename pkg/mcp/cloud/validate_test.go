package cloud

import "testing"

func TestValidateArgvRejectsDenyFloorAndScope(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	scope := ScopeAllowlist{Projects: []string{"prod"}, Regions: []string{"us-central1"}}
	cases := []struct {
		name string
		argv []string
		ok   bool
	}{
		{"allowed", []string{"compute", "instances", "list", "--project", "prod"}, true},
		{"allowed-region", []string{"compute", "instances", "list", "--project", "prod", "--region", "us-central1"}, true},
		{"bad-scope", []string{"compute", "instances", "list", "--project", "other"}, false},
		{"bad-region", []string{"compute", "instances", "list", "--project", "prod", "--region", "eu-west1"}, false},
		{"impersonate", []string{"compute", "instances", "list", "--impersonate-service-account", "x"}, false},
		{"account-flag", []string{"compute", "instances", "list", "--account", "evil"}, false},
		{"profile-flag", []string{"compute", "instances", "list", "--profile", "evil"}, false},
		{"endpoint-flag", []string{"compute", "instances", "list", "--endpoint-url", "http://evil"}, false},
		{"file-prefix", []string{"compute", "instances", "list", "--filter", "@/etc/passwd"}, false},
		{"fileurl-prefix", []string{"compute", "instances", "list", "--filter", "file:///etc/passwd"}, false},
		{"httpurl-prefix", []string{"compute", "instances", "list", "--filter", "https://evil"}, false},
		{"metachar-semicolon", []string{"compute", "instances", "list", ";", "rm", "-rf", "/"}, false},
		{"metachar-pipe", []string{"compute", "instances", "list", "|", "cat"}, false},
		{"metachar-subshell", []string{"compute", "instances", "list", "$(whoami)"}, false},
		{"not-allowed", []string{"iam", "service-accounts", "create"}, false},
		{"empty", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgv(tc.argv, al, scope)
			if tc.ok && err != nil {
				t.Fatalf("expected argv to validate, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestValidateArgvEqualsFormFlag(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	scope := ScopeAllowlist{Projects: []string{"prod"}}
	// --project=other in equals form must be caught by the scope check, and a
	// deny-floored flag in equals form must be caught by the floor.
	if err := validateArgv([]string{"compute", "instances", "list", "--project=other"}, al, scope); err == nil {
		t.Fatal("expected --project=other (equals form) to fail the scope check")
	}
	if err := validateArgv([]string{"compute", "instances", "list", "--impersonate-service-account=x"}, al, scope); err == nil {
		t.Fatal("expected --impersonate-service-account=x (equals form) to be denied")
	}
	if err := validateArgv([]string{"compute", "instances", "list", "--project=prod"}, al, scope); err != nil {
		t.Fatalf("expected --project=prod (equals form, in scope) to validate, got: %v", err)
	}
}

func TestValidateArgvEmptyScopeAllowsAnyTarget(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "projects list"}}}
	// An empty scope means the deployment did not constrain targets; the scope
	// check must not reject a --project then.
	if err := validateArgv([]string{"projects", "list", "--project", "anything"}, al, ScopeAllowlist{}); err != nil {
		t.Fatalf("empty scope should not reject a target, got: %v", err)
	}
}
