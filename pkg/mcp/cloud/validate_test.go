package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateArgvRejectsDenyFloorAndScope(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	scope := ScopeAllowlist{Projects: []string{"prod"}, Regions: []string{"us-central1"}}
	cases := []struct {
		name string
		argv []string
		ok   bool
	}{
		{"allowed", []string{"compute", "instances", "list"}, true},
		{"allowed-region", []string{"compute", "instances", "list", "--region", "us-central1"}, true},
		{"project-flag-floored", []string{"compute", "instances", "list", "--project", "prod"}, false},
		{"bad-region", []string{"compute", "instances", "list", "--region", "eu-west1"}, false},
		{"impersonate", []string{"compute", "instances", "list", "--impersonate-service-account", "x"}, false},
		{"account-flag", []string{"compute", "instances", "list", "--account", "evil"}, false},
		{"profile-flag", []string{"compute", "instances", "list", "--profile", "evil"}, false},
		{"endpoint-flag", []string{"compute", "instances", "list", "--endpoint-url", "http://evil"}, false},
		{"flags-file", []string{"compute", "instances", "list", "--flags-file", "/tmp/evil.yaml"}, false},
		{"access-token-file", []string{"compute", "instances", "list", "--access-token-file", "/tmp/tok"}, false},
		{"log-http", []string{"compute", "instances", "list", "--log-http"}, false},
		{"debug", []string{"compute", "instances", "list", "--debug"}, false},
		{"file-prefix", []string{"compute", "instances", "list", "--filter", "@/etc/passwd"}, false},
		{"fileurl-prefix", []string{"compute", "instances", "list", "--filter", "file:///etc/passwd"}, false},
		{"httpurl-prefix", []string{"compute", "instances", "list", "--filter", "https://evil"}, false},
		{"metachar-semicolon", []string{"compute", "instances", "list", ";", "rm", "-rf", "/"}, false},
		{"metachar-pipe", []string{"compute", "instances", "list", "|", "cat"}, false},
		{"metachar-subshell", []string{"compute", "instances", "list", "$(whoami)"}, false},
		{"metachar-backtick", []string{"compute", "instances", "list", "`id`"}, false},
		{"metachar-redirect", []string{"compute", "instances", "list", ">", "/tmp/x"}, false},
		{"metachar-and", []string{"compute", "instances", "list", "&&", "rm"}, false},
		{"metachar-embedded", []string{"compute", "instances", "list", "--filter=a$(id)"}, false},
		{"not-allowed", []string{"iam", "service-accounts", "create"}, false},
		{"empty", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgv(tc.argv, al, scope)
			if tc.ok {
				assert.NoError(t, err, "expected argv to validate")
			} else {
				assert.Error(t, err, "expected validation error")
			}
		})
	}
}

func TestValidateArgvEqualsFormFlag(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	scope := ScopeAllowlist{Regions: []string{"us-central1"}}
	// An out-of-scope value in equals form must be caught by the scope check, and
	// a deny-floored flag in equals form must be caught by the floor.
	assert.Error(t, validateArgv([]string{"compute", "instances", "list", "--region=eu-west1"}, al, scope),
		"expected --region=eu-west1 (equals form) to fail the scope check")
	assert.Error(t, validateArgv([]string{"compute", "instances", "list", "--impersonate-service-account=x"}, al, scope),
		"expected --impersonate-service-account=x (equals form) to be denied")
	assert.Error(t, validateArgv([]string{"compute", "instances", "list", "--project=prod"}, al, scope),
		"expected --project=prod (equals form) to be denied by the floor")
	assert.NoError(t, validateArgv([]string{"compute", "instances", "list", "--region=us-central1"}, al, scope),
		"expected --region=us-central1 (equals form, in scope) to validate")
}

func TestValidateArgvAllowsResourceOperand(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances describe"}}}
	scope := ScopeAllowlist{Projects: []string{"prod"}}
	// describe/get verbs take a resource operand; the allowlisted verb chain
	// matches as a prefix, and the operand is an inert positional argument.
	assert.NoError(t, validateArgv(
		[]string{"compute", "instances", "describe", "my-vm"}, al, scope),
		"an allowlisted verb chain plus a resource operand must validate")
}

func TestValidateArgvRejectsMetacharInAnyPosition(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances describe"}}}
	scope := ScopeAllowlist{}
	for _, argv := range [][]string{
		{"compute", "instances", "describe", "my-vm", ";", "rm"},
		{"compute", "instances", "describe", ";", "my-vm"},
		{"compute", "instances", "describe", "my-vm|cat"},
		{"compute", "instances", "describe", "my-vm", "&&", "id"},
	} {
		assert.Errorf(t, validateArgv(argv, al, scope),
			"a metacharacter token in %v must be rejected", argv)
	}
	// A literal resource name and a key=value filter contain no shell-control
	// characters and must pass.
	assert.NoError(t, validateArgv(
		[]string{"compute", "instances", "describe", "my-vm", "--filter=name=foo"}, al, scope),
		"a plain resource name and a key=value filter must pass")
}

func TestValidateArgvEmptyScopeAllowsAnyTarget(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	// An empty scope means the deployment did not constrain the region axis; the
	// scope check must not reject a --region then.
	assert.NoError(t, validateArgv([]string{"compute", "instances", "list", "--region", "anything"}, al, ScopeAllowlist{}),
		"empty scope should not reject a target")
}
