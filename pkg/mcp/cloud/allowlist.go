package cloud

// Command is one entry in the command allowlist. Path is the normalized
// subcommand path the allowlist matches against (for example "projects list" or
// "compute firewall-rules list"). Description carries the investigative axis the
// command serves (prose only). Redact marks output that needs secret-scrubbing.
type Command struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Redact      bool   `json:"redact,omitempty"`
}

// CommandAllowlist is the decoded allowlist document: the positive set of
// subcommand paths run_cli permits.
type CommandAllowlist struct {
	Commands []Command `json:"commands"`
}

// DenyFloor is the always-on set of subcommands, flags, and argument-value
// prefixes that the config can never re-enable. The base floor lives in this
// package; a Provider contributes provider-specific additions through
// DenyFloorAdditions, mirroring how k8s.LoadAllowlist always drops Secret.
type DenyFloor struct {
	Subcommands []string `json:"subcommands,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	ArgPrefixes []string `json:"arg_prefixes,omitempty"`
}
