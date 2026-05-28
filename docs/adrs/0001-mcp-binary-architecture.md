# ADR-0001: MCP binary with `--kind` subcommand

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

MCPs live in `pkg/mcp/<kind>/` and are exposed via `cmd/triagent-mcp/serve.go`. One `triagent-mcp` binary takes a `--kind=<server>` subcommand (`git`, `wiki`, `slack`, `incidentio`, `k8s`, `sessions`, `strategies`, `meta`, `agent-operator`, `parallel`, `teleport`, `signal-ingest`, etc.). Adding a new MCP = new `pkg/mcp/<name>/` package + one `case` in `cmd/triagent-mcp/serve.go`.

Each MCP package exposes `New(Options) (*Server, error)` + `(*Server).Run(ctx)` and a sibling `specs.go` with `ToolSpecs() []toolspec.ToolSpec`. The catalog feeds `triagent-mcp dump-meta`; keep it in sync with handler registrations or the wire test fails.

## Context

Each MCP could have been its own binary, but that creates N release artifacts to version-pin and N copies of shared infrastructure (sub-agent runner, telemetry, citation validator). A single binary with subcommands keeps the release surface small, makes shared code naturally shared, and lets a single launcher mcp.json entry point at every MCP capability.

The interface contract (`New` / `Run` / `ToolSpecs`) is mechanical so adding an MCP is one package plus one case statement — no plumbing copy-paste.

## Consequences

- Single release artifact bundles every MCP capability.
- Adding an MCP is mechanical (one case statement); the wire test catches drift between handler registration and the `ToolSpecs` catalog.
- Bug in any one MCP ships in every kind binary — accept the coupling as the price of shared infrastructure.
- MCP server descriptions and tool prose stay product-neutral; consumer framing (e.g. "incident response") happens at the consumer side via description passthrough.
- Prefer small, configurable tool surfaces over per-type tool families. Curated tools beat raw-query exposure for vast telemetry namespaces (Prom, k8s) — complementary, not contradictory.
