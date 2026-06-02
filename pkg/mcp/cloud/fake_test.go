package cloud

import "context"

// fakeProvider is the in-package test double for the Provider interface.
// Providers (gcp, aws) implement the same contract in their own subpackages;
// this fake exercises the parent package's harness, tools, and probe without
// shelling any real cloud CLI.
type fakeProvider struct {
	name           string
	binary         string
	allowlist      *CommandAllowlist
	denyFloor      DenyFloor
	inventory      Inventory
	identity       IdentityStatus
	identityErr    error
	envPassthrough []string
	targets        []Target
	expectedFor    map[string]string
}

func (f *fakeProvider) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

func (f *fakeProvider) Binary() string {
	if f.binary == "" {
		return "/bin/true"
	}
	return f.binary
}

func (f *fakeProvider) DefaultAllowlist() *CommandAllowlist {
	if f.allowlist == nil {
		return &CommandAllowlist{}
	}
	return f.allowlist
}

func (f *fakeProvider) DenyFloorAdditions() DenyFloor { return f.denyFloor }

func (f *fakeProvider) EnvPassthrough() []string { return f.envPassthrough }

func (f *fakeProvider) Inventory(context.Context, RunFunc) (Inventory, error) {
	return f.inventory, nil
}

func (f *fakeProvider) Identity(context.Context, RunFunc, string) (IdentityStatus, error) {
	return f.identity, f.identityErr
}

func (f *fakeProvider) ConfiguredTargets() []Target { return f.targets }

func (f *fakeProvider) ActiveTargetEnv(id string) []string { return []string{"FAKE_TARGET=" + id} }

func (f *fakeProvider) ExpectedIdentity(id string) (string, bool) {
	exp, ok := f.expectedFor[id]
	return exp, ok
}
