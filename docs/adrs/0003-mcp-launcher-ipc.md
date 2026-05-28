# ADR-0003: Two URL spaces, two auth modes for MCP ↔ launcher IPC

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

The launcher exposes two URL spaces with distinct auth modes:

- `POST /api/internal/...` — MCP-to-launcher, bearer auth via `telemetryToken` from `TRIAGENT_MCP_TELEMETRY_TOKEN`.
- `POST /api/...` — browser-to-launcher, cookie auth.

Launcher-bound MCPs read `TRIAGENT_MCP_TELEMETRY_URL`, `TRIAGENT_MCP_TELEMETRY_TOKEN`, and `TRIAGENT_MCP_TRACE_ID` and POST to a loopback endpoint scoped by trace id. Same pattern for `meta`, `agent-operator`, and any future "agent talks back to launcher" surface.

Per-investigation MCP config is written once at preflight to a session-specific path. `mcpconfig.go` is the only file that decides which MCPs attach to a session — conditional attachment (e.g. `triagent-slack` only if Sources says so) lives there, not at the handler level.

## Context

Browsers and MCPs have fundamentally different security contexts. Cookies are the right credential for a browser tab (CSRF-protected, scoped to the origin, automatic by default) and the wrong credential for an MCP subprocess (no cookie store, would require leaking the session cookie to the subprocess env). Bearer tokens are the right credential for a subprocess (scoped, opaque, easy to rotate) and the wrong credential for a browser (no CSRF protection, easy to extract from JS).

Collapsing the two spaces into one would either force the browser to send a bearer (defeating CSRF) or the MCP to send a cookie (defeating subprocess isolation). Neither is acceptable.

MCP attachment lives in one file because conditional MCP wiring (Slack only if Sources says, k8s only if there's a cluster, etc.) is itself an architectural concern; scattering it across handlers turns "which MCPs are attached to this session" into a question only the runtime can answer.

## Consequences

- Never collapse `/api/internal/` and `/api/` into one space — the security models are intentionally different.
- New "agent talks back to launcher" surfaces use the bearer + trace-id pattern; don't invent a third auth mode.
- `mcpconfig.go` is the single source of truth for per-session MCP wiring. Conditional attachment logic stays here.
- Telemetry POSTs are scoped by `TRIAGENT_MCP_TRACE_ID` so the launcher can route them to the right session without trusting client-supplied session ids.
