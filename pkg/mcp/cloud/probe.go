package cloud

import "context"

// Probe runs the read-only whoami for one provider: which pinned identity is
// active and whether it is valid. It is the single probe the launcher's
// connections panel, the session preflight gate, and the session_status tool
// all call, so those surfaces can never disagree.
//
// Probe never returns a Go error for an unreachable or invalid identity — that
// is a degrade, reported through IdentityStatus.Valid and Hint, so a stale cloud
// credential surfaces visibly instead of failing the caller. A Go error is
// reserved for a caller contract violation (a nil provider).
func Probe(ctx context.Context, p Provider) (IdentityStatus, error) {
	env := minimalEnv(p.EnvPassthrough())
	run := func(ctx context.Context, argv []string) (CLIResult, error) {
		return execCLI(ctx, p.Binary(), argv, env, defaultOutputLimit)
	}

	st, err := p.Identity(ctx, run)
	if err != nil {
		return IdentityStatus{
			Provider: p.Name(),
			Valid:    false,
			Hint:     err.Error(),
		}, nil
	}
	if st.Provider == "" {
		st.Provider = p.Name()
	}
	if st.AssumedIdentity == "" {
		// A whoami that resolved no identity is not a valid session, whatever
		// the provider reported.
		st.Valid = false
	}
	return st, nil
}
