package cloud

import (
	"fmt"
	"strings"
)

// ScopeAllowlist is the deployment's set of cloud targets any run_cli argv may
// reference. An empty field means that target axis is unconstrained. The agent
// cannot pivot to an un-allowlisted project, account, or region.
type ScopeAllowlist struct {
	Projects []string `json:"projects,omitempty"`
	Accounts []string `json:"accounts,omitempty"`
	Regions  []string `json:"regions,omitempty"`
}

// allowedFor maps a target-selecting flag to the ScopeAllowlist field whose
// membership a value of that flag must satisfy. The deny floor rejects identity
// flags (--account, --profile) before scope ever sees them, so scope governs
// only the location axes the agent is allowed to choose among.
func (s ScopeAllowlist) allowedFor(flag string) ([]string, bool) {
	switch flag {
	case "--project":
		return s.Projects, true
	case "--region", "--zone":
		return s.Regions, true
	default:
		return nil, false
	}
}

// validateArgv enforces the no-bypass contract on one argv before exec: no
// token carries a shell-control sequence, the positional subcommand path is on
// the allowlist, no token is a deny-floored flag or arg-prefix, and every
// target-selecting flag value is within scope. It runs entirely on argv tokens —
// there is no shell, so a shell-control token would be an inert positional
// anyway; the metachar check rejects it outright as defense in depth.
func validateArgv(argv []string, allow *CommandAllowlist, scope ScopeAllowlist) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	for _, tok := range argv {
		if isShellControlToken(tok) {
			return fmt.Errorf("argv token contains a shell-control character: %q", tok)
		}
	}
	if !allow.Allows(argv) {
		return fmt.Errorf("subcommand not on the allowlist: %q", strings.Join(subcommandPath(argv), " "))
	}

	floor := denyFloor // base floor; provider additions are filtered at load time.
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		flag, value, hasInlineValue := splitFlag(tok)

		if strings.HasPrefix(flag, "-") {
			if floorDeniesFlag(floor, flag) {
				return fmt.Errorf("flag is on the deny floor: %s", flag)
			}
			// Resolve the flag's value: inline (--flag=value) or the next token.
			val := value
			if !hasInlineValue && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				val = argv[i+1]
			}
			if val != "" {
				if err := checkArgPrefix(floor, val); err != nil {
					return err
				}
				if allowed, scoped := scope.allowedFor(flag); scoped {
					if err := checkScope(flag, val, allowed); err != nil {
						return err
					}
				}
			}
			continue
		}
		// Positional token: still subject to the arg-prefix floor.
		if err := checkArgPrefix(floor, tok); err != nil {
			return err
		}
	}
	return nil
}

// isShellControlToken reports whether tok is or contains a shell-control
// sequence. The harness never invokes a shell, so these are already inert; this
// is defense in depth, rejecting `;`, `|`, `&`, backtick, `$(`, `>`, `<`, and
// embedded newlines so an argv like ["...", "describe", ";", "rm"] is refused
// outright. A literal resource name ("my-vm") or a key=value flag
// ("--filter=name=foo") contains none of these and passes.
func isShellControlToken(tok string) bool {
	if strings.ContainsAny(tok, ";|&`<>\n") {
		return true
	}
	return strings.Contains(tok, "$(")
}

// splitFlag separates a "--flag=value" token into its flag and value. For a
// bare "--flag" or a non-flag token it returns the token unchanged with no
// inline value.
func splitFlag(tok string) (flag, value string, hasInlineValue bool) {
	if !strings.HasPrefix(tok, "-") {
		return tok, "", false
	}
	if eq := strings.IndexByte(tok, '='); eq >= 0 {
		return tok[:eq], tok[eq+1:], true
	}
	return tok, "", false
}

// floorDeniesFlag reports whether flag matches a deny-floored flag name.
func floorDeniesFlag(floor DenyFloor, flag string) bool {
	for _, f := range floor.Flags {
		if flag == f {
			return true
		}
	}
	return false
}

// checkArgPrefix rejects an argument value beginning with a deny-floored prefix
// (local-file read and SSRF vectors).
func checkArgPrefix(floor DenyFloor, val string) error {
	for _, p := range floor.ArgPrefixes {
		if strings.HasPrefix(val, p) {
			return fmt.Errorf("argument value has a denied prefix %q: %s", p, val)
		}
	}
	return nil
}

// checkScope rejects a target-selecting flag value outside the allowlist. An
// empty allowlist means the axis is unconstrained.
func checkScope(flag, val string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	for _, a := range allowed {
		if val == a {
			return nil
		}
	}
	return fmt.Errorf("%s %q is outside the allowed scope", flag, val)
}
