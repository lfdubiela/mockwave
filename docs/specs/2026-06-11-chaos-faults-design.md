# Chaos Fault Injection — Design

**Date:** 2026-06-11
**Status:** Approved pending review

## Goal

Bring Gremlin-style chaos experiments to mockwave at the API/dependency level: configurable fault injection (latency jitter, error rates, hangs, connection resets, partial responses, bandwidth throttling, retry storms) applied to mock and forwarded traffic, with reusable fault profiles, a global kill switch, and timed scenarios. No host agent, no root — chaos at the boundary the client actually sees.

Mapping to Gremlin attack types:

| Gremlin | mockwave fault |
|---|---|
| Latency attack | `jitter` (base delay + random variance) |
| Packet loss | `error` (probabilistic 5xx), `halfResponse` (truncated body) |
| Blackhole | `hang` (response never arrives) |
| DNS / Shutdown | `reset` (connection refused/reset semantics) |
| Process kill | `halfResponse` (connection cut mid-body) |
| Resource exhaustion (observable symptom) | `slowBody` (bytes/sec throttle) |
| Attack templates | FaultProfile entity |
| Halt button | global kill switch |
| Scenarios | timed Scenario entity |
| — (mockwave-only) | `retryStorm` (fail first N, then succeed) |

## Scope

- Faults apply to **both** simulate and forward buckets.
- Protocols: HTTP-based (REST, GraphQL, SOAP) in this iteration. **gRPC is roadmap** (see Roadmap section).
- Surfaces: admin API + admin UI + CLI (CLI talks to the admin API).

## Data model

New top-level entity **FaultProfile**, persisted via `store.DataStore` (same lifecycle as rules/simulations, included in import/export and hot reload):

```json
{
  "id": "flaky-db",
  "name": "Flaky database",
  "description": "Simulates a database under pressure",
  "enabled": true,
  "faults": [
    {"type": "jitter",       "probability": 1.0,  "params": {"baseDelayMs": 200, "jitterMs": 300}},
    {"type": "error",        "probability": 0.3,  "params": {"statusCode": 503, "body": "{\"error\":\"unavailable\"}", "headers": {"Content-Type": "application/json"}}},
    {"type": "hang",         "probability": 0.05, "params": {"maxMs": 30000}},
    {"type": "reset",        "probability": 0.05},
    {"type": "halfResponse", "probability": 0.0,  "params": {"fraction": 0.5}},
    {"type": "slowBody",     "probability": 0.0,  "params": {"bytesPerSec": 1024}},
    {"type": "retryStorm",   "probability": 1.0,  "params": {"failFirst": 3, "statusCode": 503, "keyBy": "path", "windowSec": 60}}
  ]
}
```

- `probability` ∈ [0,1], rolled independently per fault, per request.
- At most one *terminal* fault fires per request (`error`, `hang`, `reset`, `halfResponse`, `retryStorm` failure): evaluated in declaration order, first hit wins. `jitter` and `slowBody` are *modifiers* and combine with anything.
- `enabled: false` makes the whole profile a no-op without detaching it from buckets.

`domain.WeightedBucket` gains optional `faultProfileId`. Effective blast radius = bucket weight × fault probability.

## Execution

New pipeline stage **FaultStage**, after the percentile router, before simulation/forward:

1. Bucket has `faultProfileId`? Resolve profile from store.
2. Kill switch active or profile disabled → no-op.
3. Roll probabilities, produce a `FaultDirective` on `PipelineContext`.

Where directives execute:

- **Response-level** (`jitter` delay, `error` short-circuit): handled in the pipeline — `error` skips simulate/forward and writes the configured status/body; `jitter` adds to the existing delay path.
- **Connection-level** (`hang`, `reset`, `halfResponse`, `slowBody`): handled in the httprest adapter, which needs the `http.ResponseWriter`/`net.Conn`:
  - `hang`: block until `maxMs` (or client disconnect), send nothing.
  - `reset`: hijack the connection and close with `SO_LINGER 0` (TCP RST) — client sees connection reset, indistinguishable from a dead upstream.
  - `halfResponse`: write headers + `fraction` of the body, then close abruptly.
  - `slowBody`: chunked writes throttled to `bytesPerSec` with periodic flush.
- **Stateful** (`retryStorm`): in-memory counter keyed by `keyBy` (`path` or a named header), TTL `windowSec`. First `failFirst` requests per key get `statusCode`, then pass through normally. Counters are per-instance and reset on restart (documented limitation).

Metrics: `RecordRequest` attrs gain fault info (profile id, fault type fired) so the dashboard can show injected-fault counts.

## Kill switch

Global in-memory flag on the server (default: chaos active).

- `POST /api/chaos/halt` — all FaultStages become no-ops instantly; running scenarios abort.
- `POST /api/chaos/resume`
- `GET /api/chaos/status` → `{"halted": bool, "activeScenario": {...}|null}`
- UI: persistent badge when faults exist; one-click halt.
- Not persisted — restart resumes normal (non-halted) state.

## Scenarios (timed phases)

New entity **Scenario**:

```json
{
  "id": "db-degradation",
  "name": "DB degradation drill",
  "ruleIds": ["rule-1", "rule-2"],
  "phases": [
    {"durationSec": 300, "faultProfileId": "mild-latency"},
    {"durationSec": 300, "faultProfileId": "flaky-db"},
    {"durationSec": 120, "faultProfileId": null}
  ]
}
```

- `POST /api/scenarios/{id}/start` launches a runner goroutine: for each phase, it overrides the fault profile on all buckets of the target rules (in-memory overlay — stored rules are not mutated), waits `durationSec`, advances. `faultProfileId: null` = recovery phase (no faults).
- One scenario active at a time (`409` otherwise). `POST /api/scenarios/{id}/stop` and the kill switch abort it, removing the overlay.
- Scenario definitions persist in the store; run state is in-memory.
- UI shows active scenario, current phase, time remaining (via the existing SSE metrics stream).

## Admin API

```
GET    /api/faults            list profiles
POST   /api/faults            create
GET    /api/faults/{id}       get
PUT    /api/faults/{id}       update
DELETE /api/faults/{id}       delete (409 if referenced by a bucket or scenario)
POST   /api/chaos/halt | resume
GET    /api/chaos/status
GET/POST/PUT/DELETE /api/scenarios[/{id}]
POST   /api/scenarios/{id}/start | stop
```

Validation: unknown `faultProfileId` on a bucket → rule save fails `422`; fault `type` must be known; `probability` ∈ [0,1].

## CLI

New cobra commands, all hitting the admin API (`--admin-url`, default `http://localhost:9090`):

```
mockwave fault list | get <id> | create -f profile.json | delete <id>
mockwave chaos halt | resume | status
mockwave scenario list | start <id> | stop <id>
```

## Admin UI

New "Chaos" tab:

- FaultProfile CRUD with per-fault-type forms, enable/disable toggle.
- Kill switch button + halted badge in the header.
- Scenario CRUD, start/stop, live phase indicator.
- Bucket editor (existing rule form) gains a fault-profile selector.

## Docs

Two independent deliverables:

1. **Now (pre-implementation):** README gains a "Chaos Testing" section documenting what already exists today — per-simulation `delayMs`, forward `delayMs`, weighted traffic splitting as blast-radius control, conditional errors via match criteria, JS scripting for dynamic failures. No future features mentioned.
2. **With the feature:** extend that section with fault profiles, kill switch, scenarios, CLI usage.

## Roadmap (out of scope for this iteration)

- **gRPC fault support**: map faults to gRPC semantics — `error` → status codes (`UNAVAILABLE`, `DEADLINE_EXCEEDED`, `RESOURCE_EXHAUSTED`), `reset` → HTTP/2 stream reset (`RST_STREAM`), `hang` → no response until deadline, `slowBody`/`halfResponse` → throttled/truncated streaming messages. Requires fault execution hooks in the gRPC adapter.
- Health-check-driven auto-abort (halt when an observed SLO degrades).
- Experiment reports (hypothesis, affected requests, observed latencies; exportable).
- MCP chaos tools (agent-driven fault toggling) and declarative CI mode (YAML experiment, non-zero exit on SLO breach).

## Implementation phases

1. README "Chaos Testing" section (current capabilities) — shippable immediately.
2. FaultProfile entity + store + FaultStage + `jitter`/`error` + kill switch + admin API + CLI + UI.
3. Connection-level faults: `hang`, `reset`, `halfResponse`, `slowBody`.
4. `retryStorm`.
5. Scenarios (entity, runner, API/CLI/UI).

## Testing

- Unit: probability rolls (seeded RNG), directive resolution, terminal-fault ordering, retryStorm counter/TTL, scenario phase advancement (fake clock).
- Integration (per existing `tests/integration` pattern): each fault type observable from a real HTTP client — jitter delays, 503 rates, connection reset errors, truncated body reads, throttled download time, halt-switch immediacy.
