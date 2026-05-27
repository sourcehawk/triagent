# Prometheus MCP: bounded, scope-enforced, scalar-first query surface

## Problem

A prior, deployment-specific Prometheus MCP was removed during the OSS
restructure because its tool surface and prompt context were entangled
with one operator's metric namespace. The capability is still wanted —
operators investigating production incidents routinely need to ask
metric-shaped questions ("is anything throttled?", "what's the p99 for
service X right now?", "is partition health below threshold?") — but a
naive re-introduction has two failure modes that any general-purpose
Prom MCP must solve up front:

1. **Result-payload blowup.** A single `query_range` over a wide
   selector can return tens of thousands of points across hundreds of
   series. Per the tool-result token budget spec, a fat early-iteration
   tool result rides the cache for the rest of the loop — a 500 KB Prom
   response is a ~125k-token tax on every subsequent iteration.
2. **Discovery blowup.** Real Prom deployments index 1,000–10,000+
   metric names. A flat `list_metrics()` form, or even a prefix-bucketed
   one, hands the agent a payload that is both useless (raw names are
   opaque) and expensive. The agent's natural fallback — grep through
   the dump for keywords — burns context to do what server-side search
   should do.

A generalised Prom MCP needs to be safe to attach by default in any
deployment that has a Prom endpoint reachable, without the agent ever
being able to ask "what's going on?" in a way that returns megabytes,
and without baking in any operator's metric vocabulary.

## Goals

- **No path exists to enumerate the full metric set.** Discovery is
  always either keyword search or name-known describe. The agent never
  receives a flat dump.
- **No query may return without scope.** Any PromQL referencing a
  high-cardinality metric without a label matcher beyond `__name__` is
  rejected with a structured "scope required" error — not silently
  truncated.
- **Scalar-first output bias.** The default `query` tool expects PromQL
  that already collapses to a scalar or small labelled vector
  (`count(... > 0.9)`, `topk(5, ...)`, `max_over_time(...)`). The
  range-returning escape hatch is a second tool, not the default.
- **Output ceilings are hard, not heuristic.** Series count and (for
  ranges) per-series point count are capped at the tool layer; over-cap
  responses are rejected with the corrective hint, never trimmed in
  place. Same pattern as the citations runner's corrective retry.
- **Deployment-neutral tool prose.** No "incident", "investigator",
  "production" framing in tool descriptions. Same rule applied to every
  other MCP.

## Non-goals

- **Multi-Prom federation.** v1 takes one `--prom-url` per launch. A
  multi-cluster investigation switching Prom targets is handled the
  same way k8s does it — via launcher-managed URL swap with
  `atomic.Pointer[snapshot]`, not by stacking endpoints in one MCP.
- **Alertmanager integration.** Out of scope for v1; alert state lands
  through the signal-watches subsystem, not via Prom MCP tools.
- **Custom PromQL parsing / validation.** We trust Prom to parse;
  enforcement at the MCP layer is on the *response shape* (series count,
  point count, scalar vs. vector), not on the query AST.
- **Embeddings / semantic search over HELP text.** A token-AND match
  across name + HELP is sufficient for the cases we've reasoned
  through. Revisit if real session traces show it isn't.
- **Auth beyond bearer / basic.** TLS client certs and OAuth flows are
  YAGNI for v1.

## Architecture

### Binary

One new MCP kind: `triagent-mcp --kind=prom`. Sources at
`pkg/mcp/prom/`:

```
pkg/mcp/prom/
  server.go         // New(Options), (*Server).Run(ctx), atomic snapshot
  specs.go          // ToolSpecs() for the catalog
  catalog.go        // metric-name + HELP cache, prefix index
  search.go         // list_metrics implementation (token+facet)
  query.go          // query / recent_value / query_range
  cardinality.go    // per-metric cardinality estimates
  *_test.go
```

Dispatched from `cmd/triagent-mcp/serve.go` via `case "prom":
runProm(ctx, f)`, alongside the existing kinds.

### Configuration

The MCP receives its endpoint via flag/env (in priority order):

1. `--prom-url=<url>` flag.
2. `TRIAGENT_PROM_URL` env.

Auth (optional, mutually exclusive):

- `TRIAGENT_PROM_BEARER=<token>` — `Authorization: Bearer …`
- `TRIAGENT_PROM_BASIC=<user:pass>` — HTTP basic.

Profile `defaults.prometheus.{service,namespace,port}` continues to
drive the launcher-managed port-forward; the resulting loopback URL is
what the launcher passes to the MCP. The MCP itself doesn't know
anything about k8s — it sees a URL.

### Hot-swap on `switch_context` (v1)

In teleport-auth deployments the agent routinely calls
`switch_context` on the k8s MCP mid-investigation to swap clusters.
Any Prom MCP that does not follow the swap is *worse than absent*: it
silently returns metrics from the wrong cluster with no error to flag
the mismatch. v1 must ship with the rebind path wired end-to-end —
this is not a follow-up.

Mechanism (mirrors how the launcher already manages the Prom
port-forward reactively to k8s telemetry):

1. The k8s MCP emits a `switch_context` telemetry event to the
   launcher (existing).
2. The launcher's port-forward manager tears down the old Prom
   port-forward and starts a new one against the
   profile-`defaults.prometheus.{service,namespace,port}` resolved
   inside the *new* cluster context (existing pattern, scoped to Prom).
3. Once the new port-forward is healthy, the launcher POSTs to the
   prom MCP at `POST /api/internal/prom/rebind` on the prom MCP's
   loopback callback URL — bearer auth via
   `TRIAGENT_MCP_TELEMETRY_TOKEN`. Body: `{"url": "http://127.0.0.1:<new-port>"}`.
4. The prom MCP atomically swaps its `endpoint` pointer, refreshes the
   catalog against the new URL, and clears the cardinality cache.
   In-flight tool calls complete against the old endpoint; the next
   call uses the new one.

Failure modes the MCP handles explicitly:

- **Rebind arrives mid-catalog-refresh.** The latest rebind wins; an
  in-progress refresh against a superseded URL is cancelled.
- **New endpoint returns no metrics.** `prom://info` reflects "0
  metrics indexed at <url>" and every tool returns "catalog empty —
  switch context may not have a Prom endpoint, or the port-forward
  is still establishing". The agent gets a loud error, not silence.
- **Rebind never arrives after `switch_context`.** The launcher logs
  the port-forward failure; the prom MCP keeps serving the previous
  catalog. There is no agent-visible inconsistency *because the URL
  hasn't changed* — but operators will see the failed-port-forward
  telemetry in the activity panel.

Note that the prom MCP exposes only the loopback endpoint to the
launcher; it does not bind to anything externally reachable. The
bearer token is the same `TRIAGENT_MCP_TELEMETRY_TOKEN` used by
launcher-bound MCPs to call *up* to the launcher (the prom MCP is the
inverted direction — the launcher calls *down* to it). Token
rotation, when added, follows the same lifecycle.

### Startup catalog

On bind (and on rebind), one pull:

```
GET <url>/api/v1/label/__name__/values
GET <url>/api/v1/metadata
```

Stored in memory as:

```go
type catalog struct {
    names     []string                       // sorted
    metadata  map[string]MetricMetadata       // help, type, unit
    cardEst   map[string]int                  // ~rough cardinality (see below)
    prefixIdx map[string]int                  // top-prefix → count
}
```

The metric set is typically ~1k–10k names — a few hundred KB resident,
trivially serializable for tests. Refresh on rebind; no periodic
refresh in v1 (metric namespaces don't churn during an investigation).

### Cardinality estimation

For each metric, we want an *order-of-magnitude* sense of cardinality
to drive the scope-required rule (§ "Scope enforcement"). Two options:

- **Eager `/api/v1/series` per metric** — accurate but slow on large
  Proms (~one HTTP round-trip per metric × 5,000 metrics = minutes).
- **Lazy + cached** — first scope-check against a given metric triggers
  a `/api/v1/series?match[]=<name>&limit=K` (K=200) probe; if K is
  returned, we mark "high cardinality"; else we record the actual
  count. Cached for the life of the catalog binding.

v1: lazy + cached. The first query against a fresh metric pays one
extra round-trip; subsequent queries are free. The probe response also
yields the label keys for `describe_metric` (lazily-filled).

## Tool surface

Five tools, in two tiers.

### Discovery tier

#### `prom_list_metrics`

```
prom_list_metrics(query string, limit int = 30) → SearchResult
```

- `query` is **required**, non-empty, non-wildcard.
- Tokenised on whitespace; tokens AND-match against metric `name` OR
  its `HELP` text (case-insensitive substring per token). Ranking:
  metrics matching all tokens > matching all-but-one > … . Names sort
  alphabetically within a tier.
- Output cap = `limit`, hard cap 50.
- **Facet fallback on overflow.** If more than `limit` metrics match,
  the response is not the first N — it's a *sub-prefix breakdown* of
  the matches, computed by grouping on the next path segment after the
  longest common prefix shared by the match set:

  ```json
  {
    "matches": null,
    "overflow": {
      "total": 54,
      "facets": [
        {"prefix": "zeebe_partition_",          "count": 18},
        {"prefix": "zeebe_broker_",             "count": 9},
        {"prefix": "zeebe_exporter_",           "count": 8},
        {"prefix": "zeebe_job_",                "count": 6},
        {"prefix": "zeebe_stream_processor_",   "count": 5}
      ],
      "hint": "Refine: add tokens (e.g. 'zeebe partition') or pick a sub-prefix and search again."
    }
  }
  ```

- Result item shape (`{name, type}` — no HELP, no labels):

  ```json
  { "name": "zeebe_partition_health", "type": "gauge" }
  ```

  ~60 bytes per item × 30 items ≈ 2 KB worst case.

#### `prom_describe_metric`

```
prom_describe_metric(name string) → MetricDescription
```

Returns:

```json
{
  "name": "zeebe_partition_health",
  "type": "gauge",
  "help": "Health of the Zeebe partition (0 healthy, 1 unhealthy, 2 dead)",
  "unit": "",
  "labels": [
    {"key": "namespace", "cardinality": 3, "sample_values": ["zeebe-prod", "zeebe-stg", "zeebe-dev"], "typical_scope": true},
    {"key": "pod",       "cardinality": 24, "sample_values": ["zeebe-0", "zeebe-1", "zeebe-2"]},
    {"key": "partition", "cardinality": 8,  "sample_values": ["1","2","3","4","5"]}
  ],
  "related": [
    "zeebe_partition_role",
    "zeebe_partition_term",
    "zeebe_partition_leader"
  ],
  "cardinality_total": 192
}
```

- `labels` is derived from a `/api/v1/series?match[]=<name>` probe (top
  10 distinct values per key by lexical order, not by frequency — Prom
  does not expose value frequency cheaply).
- `typical_scope: true` is heuristic: pick the label key with the
  lowest cardinality that appears in ≥80% of series; `namespace` and
  `service` are nudged ahead if present.
- `related` lists sibling metrics in the longest common prefix with the
  named metric (cap 10).

### Query tier

#### `prom_query`

```
prom_query(promql string, time *string = nil) → QueryResult
```

The default. `time` is optional ISO-8601; default `now`.

**Output cap: ≤ 50 series in the returned vector.** Over-cap responses
are rejected:

```
{
  "error": "query returned 312 series; cap is 50. Wrap in topk(N, ...), aggregate, or add a scope matcher."
}
```

**Scope enforcement (see § Scope enforcement) runs first**: a query
against a high-cardinality metric without a non-`__name__` matcher is
rejected before the round-trip to Prom.

Result shape — preserves the vector but no shape-over-time:

```json
{
  "result_type": "vector",
  "samples": [
    {"labels": {"namespace": "payments", "pod": "api-0"}, "value": 0.93, "timestamp": "2026-05-17T14:31:02Z"},
    ...
  ],
  "truncated": false
}
```

Scalar results pass through as `{"result_type": "scalar", "value": 7,
"timestamp": "..."}`. Empty vector is `samples: []`.

#### `prom_recent_value`

```
prom_recent_value(metric string, labels map[string]string) → ValueResult
```

Convenience wrapper for "what is the current value of `metric` for the
exact label set `labels`?". The MCP builds the instant query
internally, requires `labels` to be non-empty, runs the scope check,
returns a single value (or "no data" / "multiple series matched —
narrow the label set").

Returned shape:

```json
{ "value": 0.93, "timestamp": "2026-05-17T14:31:02Z" }
```

This exists because the most common agent question — "what is X for
pod Y right now?" — should not require composing PromQL.

#### `prom_query_range`

```
prom_query_range(promql string, range string, end *string = nil,
                 max_series int = 10, max_points int = 100) → RangeResult
```

The opt-in escape hatch for when shape-over-time genuinely matters.
Description copy explicitly nudges the agent toward `prom_query` first.

- `range` is a duration string (`"15m"`, `"1h"`, …, cap 24h).
- `end` defaults to `now`.
- `step` is **not** an input — the MCP computes it as
  `max(1s, range_seconds / max_points)` so the response stays inside
  the point budget. The actual `step` used is returned in the response.
- Scope enforcement + series cap (`max_series`, hard ceiling 25) apply.

**Output shape — per-series summary stats, not raw points:**

```json
{
  "step": "9s",
  "series": [
    {
      "labels": {"namespace": "payments", "pod": "api-0"},
      "stats": {
        "min": 0.41, "max": 0.97, "mean": 0.78,
        "p50": 0.79, "p95": 0.94, "p99": 0.96,
        "first": 0.45, "last": 0.93
      },
      "sparkline": "▁▂▄▅▇▇▆▅▆▇▇█▇▇▆▇▇▇▇▇"
    }
  ]
}
```

Sparkline is a 20-character Unicode block-element rendering of the
series, downsampled by min-max bucketing. Cheap to compute, cheap on
tokens (~60 bytes), preserves the rough shape the agent needs to
correlate with timestamps.

**Raw-points mode** is an opt-in flag `raw bool = false`. When true,
the response carries raw `[ts, value]` pairs, still capped to
`max_points` per series. Used when the agent has decided summary stats
aren't enough.

## Scope enforcement

A query is *scoped* if every metric reference in the PromQL has at
least one label matcher with a key other than `__name__`. We do not
parse PromQL; we substring-scan for metric names from the catalog and
require that each occurrence is followed (within a small window of
characters, before any breaking token) by a `{...}` block containing
at least one `=` not preceded by `__name__`.

When the matched metric has a **catalog cardinality estimate ≥ 50**
(the scope-required threshold), an unscoped reference is rejected:

```
{
  "error": "scope required for high-cardinality metric 'container_cpu_usage_seconds_total' (≥200 series). Add at least one label matcher, e.g. namespace=\"...\", service=\"...\". See prom_describe_metric for typical scope keys."
}
```

`recent_value` enforces this trivially — `labels` is required, and the
resulting matcher set is checked against the cardinality estimate.

Low-cardinality metrics (e.g. `up`, `prometheus_build_info`,
single-instance scalars) are exempt — the cardinality estimate gates
the rule.

## INFO resource

The MCP exposes one MCP resource at URI `prom://info` returning a short
human-readable map of the indexed namespace:

```
1,847 metrics indexed at http://localhost:9090.

Top prefixes (≥ 20 metrics):
  container_*     312
  apiserver_*     127
  node_*           89
  zeebe_*          54
  http_*           54
  go_*             41
  ...

Discovery:
  - prom_list_metrics(query)         search by name or HELP text
  - prom_describe_metric(name)       labels, sample values, related metrics

Query:
  - prom_recent_value(metric, labels)        current value for exact labels
  - prom_query(promql)                       instant; scalar or ≤50-series vector
  - prom_query_range(promql, range)          time-shape; summary stats per series

Conventions:
  - Always pass at least one scope matcher (namespace, service, job, …).
  - Prefer threshold checks and topk() to broad selections.
```

Generated at catalog-bind time from the prefix index; rebuilds on
rebind. Resource read is ~1 KB. The agent reads it once per attach.

## Companion playbook

A new system playbook `system/prom_lookup.yaml` codifies the flow.
Short — three steps, single non-handoff terminal — and is referenced
from the existing investigation playbooks as a `delegate_to` target
when the operator chooses Prom as a signal source.

Step shape:

1. **`info`** — call the `prom://info` resource if you haven't already
   this session; identify the prefix family closest to the question.
2. **`discover`** — `prom_list_metrics` with one or two keywords; if
   the response is a facet breakdown, pick a sub-prefix and search
   again; once you have a short list, `prom_describe_metric` on the
   most relevant metric to confirm labels and scope keys.
3. **`query`** — build a *scoped* query. Prefer `prom_recent_value`
   when you know the exact label set; prefer `prom_query` with a
   threshold/aggregation/topk when you want a yes-no or worst-offender
   answer; reach for `prom_query_range` only when shape-over-time is
   needed.

`terminal_advice` on success carries the findings forward to the
parent playbook.

## Tests

- `pkg/mcp/prom/catalog_test.go` — startup catalog from a `httptest`
  Prom stub (label-values + metadata fixtures); rebind invalidates.
- `pkg/mcp/prom/rebind_test.go` —
  - `POST /api/internal/prom/rebind` with the bearer-token header
    swaps the atomic endpoint pointer and rebuilds the catalog
    against the new stub.
  - Wrong / missing bearer token → 401, no swap.
  - Rebind during an in-flight catalog refresh against the old URL:
    the old refresh is cancelled, the new one wins, no torn state in
    the catalog.
  - New endpoint returns an empty `__name__` set: subsequent tool
    calls return the explicit "catalog empty" error rather than a
    silent empty success.
- `pkg/mcp/prom/search_test.go` — token-AND ranking; facet fallback
  shape; overflow threshold; required-query rejection.
- `pkg/mcp/prom/cardinality_test.go` — lazy probe; `limit=K` reached →
  "high cardinality" sentinel; cached on second call.
- `pkg/mcp/prom/query_test.go`:
  - Scope-required rejection on unscoped reference to a high-card
    metric; allow on scoped reference; allow on low-card metric.
  - 50-series cap rejection with corrective message.
  - `recent_value` with multiple-matching labels → "narrow" error.
  - `query_range`: step computed for point budget; sparkline shape on
    a synthetic ramp; `raw=true` returns points up to cap.
- `pkg/mcp/prom/specs_test.go` — `ToolSpecs()` matches handler
  registration; description prose contains no deployment-specific
  vocabulary.

Race-clean (`go test -race -count=1 ./pkg/mcp/prom/...`) — the snapshot
swap is the only concurrent surface.

## Open questions / follow-ups

- **Profile field for Prom URL passthrough.** Today
  `defaults.prometheus.{service,namespace,port}` drives the
  port-forward. We may want an alternate `defaults.prometheus.url`
  branch for deployments where Prom is reachable directly (no
  port-forward needed). Handled in a follow-up; doesn't change the MCP
  itself.
- **Series-frequency-aware sample values in `describe_metric`.** Today
  we return lexically-first sample values per label. A "frequent first"
  ordering would be more useful but requires either a server-side
  histogram or a client-side `/api/v1/series` pull with bucketing —
  expensive on large metrics. Defer until session traces show the
  lexical ordering misleads agents.
- **Conditional attachment.** The prom MCP attaches when the
  investigation's sources block declares a Prom signal source (TBD —
  the mcpconfig layer decides). Definition of "has Prom" lives in
  `internal/server/mcpconfig.go` alongside the slack-MCP conditional.
- **Multi-Prom federation.** If real usage shows operators routinely
  want to point one investigation at two Proms (e.g. infra-Prom vs
  app-Prom), revisit. Likely shape: a second `--prom-url-secondary` +
  a `target` arg on tools. Not built until the second consumer asks.
