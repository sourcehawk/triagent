package cloud

import (
	"context"
	"fmt"
)

// Probe runs the read-only whoami for one provider: which pinned identity is
// active and whether it is valid. It is the single probe the launcher's
// connections panel, the session preflight gate, and the session_status tool
// all call, so those surfaces can never disagree.
//
// expected is the identity the launcher pinned for this session, threaded
// explicitly so the probe validates against it without reading process-global
// env; env is the exact subprocess environment the whoami exec runs under,
// passed in by the caller rather than read from os.Environ here.
//
// Probe never returns a Go error for an unreachable or invalid identity — that
// is a degrade, reported through IdentityStatus.Valid and Hint, so a stale cloud
// credential surfaces visibly instead of failing the caller. A Go error is
// reserved for a caller contract violation (a nil provider).
func Probe(ctx context.Context, p Provider, expected string, env []string) (IdentityStatus, error) {
	if p == nil {
		return IdentityStatus{}, fmt.Errorf("cloud: Probe requires a provider")
	}
	run := func(ctx context.Context, argv []string) (CLIResult, error) {
		return execCLI(ctx, p.Binary(), argv, env, defaultOutputLimit)
	}

	st, err := p.Identity(ctx, run, expected)
	if err != nil {
		return IdentityStatus{
			Provider:        p.Name(),
			AssumedIdentity: expected,
			Valid:           false,
			Hint:            err.Error(),
		}, nil
	}
	if st.Provider == "" {
		st.Provider = p.Name()
	}
	if st.AssumedIdentity == "" {
		// A whoami that resolved no identity is not a valid session, whatever
		// the provider reported. Report the pinned identity so the degraded
		// session names which credential the operator must fix instead of an
		// empty one.
		st.Valid = false
		st.AssumedIdentity = expected
	}
	return st, nil
}
