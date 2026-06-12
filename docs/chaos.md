# Chaos Testing Guide

Mockwave injects failures at the boundary your client actually sees — mock
responses and forwarded upstream traffic — instead of breaking real
infrastructure the way host-agent tools (Gremlin et al.) do. No agent, no root,
runs on your laptop and in CI.

This guide is the end-to-end walkthrough: every fault type, how to drive it from
the UI, the CLI, and the raw API, plus scenarios, the kill switch, and the full
admin API reference.

- [Concepts](#concepts)
- [Fault types](#fault-types)
- [Walkthrough: your first chaos test](#walkthrough-your-first-chaos-test)
- [Step-by-step per fault type](#step-by-step-per-fault-type)
- [Kill switch](#kill-switch)
- [Scenarios](#scenarios)
- [Backends & persistence](#backends--persistence)
- [Import / export](#import--export)
- [Admin API reference](#admin-api-reference)
- [Limitations](#limitations)

---

## Concepts

| Term | Meaning |
|---|---|
| **Fault** | A single failure mode (jitter, error, hang, …) with a `probability` and type-specific `params`. |
| **Fault profile** | A named, reusable list of faults. Attach it to a rule bucket via `fault_profile_id`. |
| **Bucket** | One weighted branch of a rule (simulate or forward). Faults apply to both. |
| **Kill switch** | Global on/off that suppresses *all* fault injection instantly, without editing rules/profiles. |
| **Scenario** | A timed sequence of phases; each phase applies a profile to target rules for a fixed duration, then advances. |

**How a fault profile is evaluated, per request:**

1. The request matches a rule and the router picks a bucket.
2. If the bucket has a `fault_profile_id` (or a scenario overlays one), the profile's faults are rolled **independently**, in declaration order, each against its own `probability`.
3. **At most one *terminal* fault fires** (`error`, `hang`, `reset`, `halfResponse`, `retryStorm` failure) — first hit wins and short-circuits the request.
4. **Modifiers combine** with anything: `jitter` (extra latency) and `slowBody` (throttle) apply alongside a terminal fault or a normal response.
5. The kill switch (or a disabled profile) makes the whole thing a no-op.

> **Blast radius = bucket weight × fault probability.** A bucket at `weight: 20`
> with a fault at `probability: 0.5` hits ~10% of the rule's traffic.

---

## Fault types

| Type | Class | Params | Effect | Gremlin analog |
|---|---|---|---|---|
| `jitter` | modifier | `base_delay_ms`, `jitter_ms` | Adds `base_delay_ms` + random `[0, jitter_ms)` latency | Latency attack |
| `error` | terminal | `status_code`, `body`, `headers` | Short-circuits with the given HTTP status/body | Packet loss (app-level) |
| `hang` | terminal | `max_ms` | Blocks up to `max_ms` then closes with no response (blackhole) | Blackhole |
| `reset` | terminal | — | TCP RST — client sees connection reset (dead upstream) | DNS / shutdown |
| `halfResponse` | terminal | `fraction` (0–1) | Writes headers + `fraction` of the body, then cuts the connection | Process kill mid-response |
| `slowBody` | modifier | `bytes_per_sec` | Streams the response body throttled to `bytes_per_sec` | Resource exhaustion (observable) |
| `retryStorm` | terminal (stateful) | `fail_first`, `status_code`, `key_by`, `window_sec` | Fails the first `fail_first` requests per key, then passes | — (mockwave-only) |

**Protocol support:** `jitter` and `error` work on every HTTP-based protocol
(REST, GraphQL, SOAP). The connection-level faults (`hang`, `reset`,
`halfResponse`, `slowBody`) also work on REST/GraphQL/SOAP. **gRPC is not yet
supported for any fault type** — it is on the roadmap.

### Param reference (JSON keys are snake_case)

```jsonc
// jitter
{"type": "jitter", "probability": 1.0, "params": {"base_delay_ms": 200, "jitter_ms": 300}}
// error
{"type": "error", "probability": 0.3, "params": {"status_code": 503, "body": "{\"error\":\"unavailable\"}", "headers": {"Content-Type": "application/json"}}}
// hang
{"type": "hang", "probability": 0.05, "params": {"max_ms": 30000}}
// reset
{"type": "reset", "probability": 0.05}
// halfResponse
{"type": "halfResponse", "probability": 0.1, "params": {"fraction": 0.5}}
// slowBody
{"type": "slowBody", "probability": 1.0, "params": {"bytes_per_sec": 1024}}
// retryStorm
{"type": "retryStorm", "probability": 1.0, "params": {"fail_first": 3, "status_code": 503, "key_by": "path", "window_sec": 60}}
```

Validation rules (enforced on save, `422` otherwise): `probability ∈ [0,1]`;
`status_code ∈ [100,599]`; jitter requires `base_delay_ms` or `jitter_ms > 0`
(no negatives); `halfResponse.fraction ∈ (0,1)`; `slowBody.bytes_per_sec > 0`;
`hang.max_ms > 0`; `retryStorm` needs `fail_first > 0`, `window_sec > 0`, and
`key_by` is `path` or `header:<Name>`.

---

## Walkthrough: your first chaos test

Goal: 30% of requests to `/orders` get a 503. Three ways to do it.

### Via the admin UI

1. Start mockwave and open the admin UI: `http://localhost:9090`.
2. Make sure you have a rule serving `/orders` (Rules tab → **+ Add Rule**, simulate bucket → a 200 simulation).
3. Go to the **Chaos** tab → **+ Add Profile**.
   - `id`: `flaky-orders`, `name`: `Flaky orders`, check **enabled**.
   - **+ Add Fault** → type **Error** → `status_code` `503`, `body` `{"error":"boom"}`, **probability** `0.3`.
   - **Save Profile**.
4. Rules tab → edit the `/orders` rule → on the bucket, set **Fault profile** → `flaky-orders` → **Save Rule**.
5. Hit it: `for i in $(seq 1 10); do curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/orders; done` — roughly 3 of 10 are `503`.

### Via the CLI

```bash
cat > flaky-orders.json <<'EOF'
{"id":"flaky-orders","name":"Flaky orders","enabled":true,
 "faults":[{"type":"error","probability":0.3,"params":{"status_code":503,"body":"{\"error\":\"boom\"}"}}]}
EOF

mockwave fault create -f flaky-orders.json     # POST /api/faults
mockwave fault list                            # confirm
# attach via the rule editor (UI) or by PUT-ing the rule with fault_profile_id on the bucket
```

### Via the raw API

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"flaky-orders","name":"Flaky orders","enabled":true,
  "faults":[{"type":"error","probability":0.3,"params":{"status_code":503,"body":"{\"error\":\"boom\"}"}}]
}'
# then attach fault_profile_id:"flaky-orders" to the rule's bucket (PUT /api/rules/<id>)
```

Attaching to a bucket means adding `fault_profile_id` to it:

```json
{"weight": 100, "action": "simulate", "simulation_id": "orders-ok", "fault_profile_id": "flaky-orders"}
```

A rule that references an unknown profile is rejected with `422`; deleting a
profile still referenced by a bucket returns `409`.

---

## Step-by-step per fault type

All examples: create the profile (API shown; UI is the same fields under
**Chaos → + Add Profile → + Add Fault**), attach `fault_profile_id` to a bucket,
then drive traffic. The mock port is `:8080`, admin is `:9090`.

### jitter — latency

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"slow","name":"Slow","enabled":true,
  "faults":[{"type":"jitter","probability":1,"params":{"base_delay_ms":200,"jitter_ms":300}}]}'
```

Test: `curl -o /dev/null -s -w "%{time_total}s\n" localhost:8080/orders` →
every response takes ≥ 0.2s, up to ~0.5s.

### error — HTTP error injection

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"err","name":"Err","enabled":true,
  "faults":[{"type":"error","probability":1,"params":{"status_code":503,"body":"{\"error\":\"down\"}","headers":{"X-Chaos":"1"}}}]}'
```

Test: `curl -i localhost:8080/orders` → `503` with the custom body and header.

### hang — blackhole

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"blackhole","name":"Blackhole","enabled":true,
  "faults":[{"type":"hang","probability":1,"params":{"max_ms":5000}}]}'
```

Test: `time curl -m 10 localhost:8080/orders` → the request blocks ~5s then the
connection closes with no response. Use a client timeout (`-m`) so you are
testing your client's timeout handling.

### reset — connection reset

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"deadsvc","name":"Dead service","enabled":true,
  "faults":[{"type":"reset","probability":1}]}'
```

Test: `curl localhost:8080/orders` → `curl: (56) Recv failure: Connection reset
by peer` (exact message is OS-dependent). Simulates an upstream that abruptly
dropped the socket.

### halfResponse — truncated body

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"truncate","name":"Truncate","enabled":true,
  "faults":[{"type":"halfResponse","probability":1,"params":{"fraction":0.5}}]}'
```

Test: `curl -s localhost:8080/orders | wc -c` → roughly half the expected bytes;
the connection is cut mid-body (the response advertises the full
`Content-Length`, so the client sees a truncated/short read).

### slowBody — bandwidth throttle

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"throttle","name":"Throttle","enabled":true,
  "faults":[{"type":"slowBody","probability":1,"params":{"bytes_per_sec":1024}}]}'
```

Test: `curl -o /dev/null -s -w "%{time_total}s\n" localhost:8080/orders` → a 4 KB
body takes ~4s. `slowBody` is a *modifier* — combine it with `reset` (list
`slowBody` first) to model a slow link that also drops.

### retryStorm — fail-first then recover

```bash
curl -X POST localhost:9090/api/faults -d '{
  "id":"storm","name":"Retry storm","enabled":true,
  "faults":[{"type":"retryStorm","probability":1,
    "params":{"fail_first":2,"status_code":503,"key_by":"path","window_sec":60}}]}'
```

Test: three requests to the same path →

```bash
for i in 1 2 3; do curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/orders; done
# 503
# 503
# 200   ← recovers after fail_first
```

This validates client retry backoff and idempotency. Use `key_by:
header:X-Request-Id` to give each request id its own independent fail-first
budget.

### Combining faults

List faults in one profile; modifiers combine, the first terminal wins:

```json
{
  "id": "flaky-network", "name": "Flaky network", "enabled": true,
  "faults": [
    {"type": "slowBody", "probability": 1, "params": {"bytes_per_sec": 2048}},
    {"type": "reset", "probability": 0.2}
  ]
}
```

→ every response is throttled to 2 KB/s, and 20% of them also get reset
mid-stream. (Put `slowBody` before `reset` so it is applied before the terminal
fault returns.)

---

## Kill switch

A global switch that suppresses all injection instantly — your panic button when
an experiment misbehaves. It does **not** touch any rule or profile.

```bash
mockwave chaos halt      # POST /api/chaos/halt    → 204, all faults become no-ops
mockwave chaos status    # GET  /api/chaos/status  → {"halted":true, "active_scenario":null}
mockwave chaos resume    # POST /api/chaos/resume  → 204
```

In the UI, the **Chaos** tab header shows a **Halt Chaos** button and a red
**CHAOS HALTED** badge while halted; status is polled every 10s, so a CLI halt
shows up in an open UI within ~10s. The switch is **not persisted** — a restart
comes up un-halted.

---

## Scenarios

A **scenario** runs a timed sequence of fault phases against a set of target
rules — e.g. "degrade payments for 5 min, hard-fail for 2 min, then recover".
Each phase applies one profile to every targeted rule for `duration_sec`, then
the runner advances. A phase with an empty `fault_profile_id` is a **recovery
phase** (no faults).

While a scenario runs, its current phase profile **overrides** each targeted
rule's own `fault_profile_id` (an in-memory overlay — stored rules are never
mutated).

```json
{
  "id": "payments-drill",
  "name": "Payments degradation drill",
  "rule_ids": ["pay-charge", "pay-refund"],
  "phases": [
    { "duration_sec": 300, "fault_profile_id": "mild-latency" },
    { "duration_sec": 120, "fault_profile_id": "errors-503" },
    { "duration_sec": 60,  "fault_profile_id": "" }
  ]
}
```

Drive it:

```bash
mockwave scenario list           # GET  /api/scenarios
mockwave scenario start payments-drill   # POST /api/scenarios/payments-drill/start → 202
mockwave scenario stop  payments-drill   # POST /api/scenarios/payments-drill/stop  → 204
```

Live status is on `GET /api/chaos/status`:

```json
{ "halted": false,
  "active_scenario": { "scenario_id": "payments-drill", "scenario_name": "Payments degradation drill",
                       "phase_index": 1, "phase_count": 3, "phase_profile_id": "errors-503" } }
```

In the UI, the **Chaos** tab lists scenarios with **Start/Stop** buttons and
shows a live **SCENARIO RUNNING** banner with the current phase.

Key properties:

- **One scenario at a time.** Starting a second returns `409`.
- **The kill switch aborts the active scenario** (halting stops the run so it
  does not silently resume on `resume`).
- **Run state is per-process and in-memory.** Restarting clears any active run;
  scenario *definitions* are persisted, their live execution is not.
- Creating/updating a scenario validates that every `rule_id` exists and every
  non-empty phase `fault_profile_id` resolves (`422` otherwise).

---

## Backends & persistence

Fault profiles and scenarios are persisted entities and work on **all** store
backends.

| Backend | Setup |
|---|---|
| **JSON file** | `fault_profiles` and `scenarios` arrays in the config file; loaded at startup, hot-reloaded on change. |
| **DynamoDB** | Two extra tables, created out-of-band like the rules/simulations tables: `mockwave-fault-profiles` and `mockwave-scenarios` (PK `id`, on-demand billing). Override names with `--dynamo-faults-table` / `--dynamo-scenarios-table` or `MOCKWAVE_DYNAMO_FAULTS_TABLE` / `MOCKWAVE_DYNAMO_SCENARIOS_TABLE`. |
| **MongoDB / Cosmos** | Collections `fault_profiles` and `scenarios` auto-create on first write. |

When a backend that does not support these capabilities is in use, the fault and
scenario endpoints return `501`. (All built-in backends support them; `501`
would only appear for a custom `store.DataStore` that does not implement
`store.FaultStore` / `store.ScenarioStore`.)

A write to a profile or scenario bumps the store's config version, so the
version-poll reloader rebuilds the pipeline and picks up the change — same
mechanism rules/simulations use.

---

## Import / export

`GET /api/export` includes the fault profiles referenced by the exported rules'
buckets, plus any scenario whose **every** `rule_id` is in the exported set (and
that scenario's referenced profiles). `POST /api/import` upserts incoming
profiles alongside the rules that reference them and upserts **all** payload
scenarios (they are independent top-level entities). Validation:

- A rule referencing a profile that exists in neither the payload nor the store → `422`.
- A scenario referencing an unknown `rule_id` or non-empty phase `fault_profile_id` → `422`.
- Payload carries scenarios but the store lacks `ScenarioStore` → `422`.

Import preview reports profile and scenario id conflicts in
`fault_profile_conflicts` and `scenario_conflicts` arrays.

---

## Admin API reference

All on the admin port (default `:9090`). Bodies are JSON.

### Fault profiles

| Method | Path | Success | Errors |
|---|---|---|---|
| `GET` | `/api/faults` | `200` `[FaultProfile]` | `501` (store unsupported) |
| `POST` | `/api/faults` | `201 FaultProfile` | `400` bad JSON · `409` id exists · `422` invalid · `501` |
| `GET` | `/api/faults/{id}` | `200 FaultProfile` | `404` · `501` |
| `PUT` | `/api/faults/{id}` | `200 FaultProfile` | `400` · `404` · `422` · `501` |
| `DELETE` | `/api/faults/{id}` | `204` | `404` · `409` (referenced by a bucket) · `501` |

### Kill switch

| Method | Path | Success | Notes |
|---|---|---|---|
| `POST` | `/api/chaos/halt` | `204` | suppress all faults; aborts any active scenario |
| `POST` | `/api/chaos/resume` | `204` | resume injection |
| `GET` | `/api/chaos/status` | `200 ChaosStatus` | `{halted, active_scenario}` |

### Scenarios

| Method | Path | Success | Errors |
|---|---|---|---|
| `GET` | `/api/scenarios` | `200 [Scenario]` | `501` |
| `POST` | `/api/scenarios` | `201 Scenario` | `409` id exists · `422` invalid/unknown ref · `501` |
| `GET` | `/api/scenarios/{id}` | `200 Scenario` | `404` · `501` |
| `PUT` | `/api/scenarios/{id}` | `200 Scenario` | `404` · `422` · `501` |
| `DELETE` | `/api/scenarios/{id}` | `204` | `404` · `501` |
| `POST` | `/api/scenarios/{id}/start` | `202` | `404` · `409` (already running) · `501` |
| `POST` | `/api/scenarios/{id}/stop` | `204` | `501` |

### Schemas

**FaultProfile**

```json
{
  "id": "string",
  "name": "string",
  "description": "string (optional)",
  "enabled": true,
  "faults": [
    {"type": "jitter|error|hang|reset|halfResponse|slowBody|retryStorm",
     "probability": 0.0,
     "params": { "base_delay_ms": 0, "jitter_ms": 0, "status_code": 0, "body": "", "headers": {},
                 "max_ms": 0, "fraction": 0.0, "bytes_per_sec": 0,
                 "fail_first": 0, "key_by": "path", "window_sec": 0 }}
  ]
}
```

**Scenario**

```json
{
  "id": "string",
  "name": "string",
  "rule_ids": ["rule-1", "rule-2"],
  "phases": [
    {"duration_sec": 300, "fault_profile_id": "profile-id"},
    {"duration_sec": 60,  "fault_profile_id": ""}
  ]
}
```

**ChaosStatus**

```json
{
  "halted": false,
  "active_scenario": {
    "scenario_id": "string", "scenario_name": "string",
    "phase_index": 0, "phase_count": 3, "phase_profile_id": "string"
  }
}
```

`active_scenario` is `null` when no scenario is running.

### CLI ↔ API mapping

```
mockwave fault list                       GET    /api/faults
mockwave fault get <id>                    GET    /api/faults/<id>
mockwave fault create -f profile.json      POST   /api/faults
mockwave fault delete <id>                 DELETE /api/faults/<id>
mockwave chaos halt | resume               POST   /api/chaos/halt | resume
mockwave chaos status                      GET    /api/chaos/status
mockwave scenario list                     GET    /api/scenarios
mockwave scenario start <id>               POST   /api/scenarios/<id>/start
mockwave scenario stop <id>                POST   /api/scenarios/<id>/stop
```

All CLI commands accept `--admin-url` (default `http://localhost:9090`).

---

## Limitations

- **gRPC** has no fault support yet (any fault type). Chaos applies to
  REST/GraphQL/SOAP only.
- **`retryStorm` counters are per-process** and reset on restart. Behind a load
  balancer with N mockwave instances the effective `fail_first` budget is ~N×.
- **Scenario run state is in-memory.** A restart clears the active run (the
  definition persists).
- **Connection-level faults need an HTTP/1.1 connection** (hijackable socket).
  Behind an HTTP/2 terminator they degrade to a best-effort/no-op.
