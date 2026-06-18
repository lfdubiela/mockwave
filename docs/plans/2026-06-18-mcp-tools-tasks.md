# MCP Tools — Task Plan (P0 → P4)

Expose the full admin surface through the `mockwave mcp` server so Claude Code can manage AWS event interception, inspect captures, and drive chaos — not just HTTP rules/simulations. Closes the gap found in the MCP audit.

**Source:** MCP gap audit (this session). **Goal:** add ~30 MCP tools across event-rules, event-captures, matched, faults, scenarios, chaos, import/export, and dev-ergonomics groups, mirroring the existing `rule`/`simulation` tool pattern.

## Established pattern (verified)

Each tool = (1) a typed client method in `internal/mcp/client.go` using `c.get/c.post/c.put/c.doDelete`; (2) a handler `handleX(c *Client) func(ctx, req)(*mcpsdk.CallToolResult, error)` in `internal/mcp/tools.go` using `jsonResult`/`stringParam`/`jsonParam`; (3) registration `s.AddTool(mcpsdk.NewTool("name", WithDescription, WithString/WithObject(...Required())), handleX(c))` in `internal/mcp/server.go`; (4) help text in `cmd/mockwave/mcp.go`; (5) tests in `internal/mcp/*_test.go` (httptest admin stub).

## Structural decision (enables parallelism)

`client.go` / `tools.go` / `server.go` are already large and shared by every group — editing them per-group serializes all work and risks merge conflicts. **Split each tool group into its own files** and a per-group `register<Group>(s, c)` func:
- `internal/mcp/client_<group>.go` — that group's client methods.
- `internal/mcp/tools_<group>.go` — that group's handlers + `register<Group>(s *server.MCPServer, c *Client)`.
- `internal/mcp/tools_<group>_test.go` — tests.

`NewServer` (server.go) then just calls `registerEventRuleTools(s, c)` etc. This makes the group tasks **non-overlapping** (own files) → genuinely `[P]`-able; the only serialized touch-points are the one-line `register*` calls in `NewServer` and the help text in `mcp.go` (Phase 3).

---

## Phase 0: Setup / Foundation

### TASK-001 — Capture-filter plumbing (shared helper)
**Phase:** Foundation
**Dependencies:** none
**Files:** `internal/mcp/client.go` (add `getWithQuery(path string, q url.Values, dst any) error`), `internal/mcp/tools.go` (add `captureFilterArgs(req) url.Values`), `internal/mcp/tools_test.go`
**Description:** Add a client helper that issues a GET with an encoded query string, and a tools helper that reads the optional capture-filter args into `url.Values`. The filter args are passed as **objects** (LLM-friendly): `body` `{"$.correlation_id":"abc"}`, `attr` `{"tenant":"acme"}`, `query` `{"source":"billing"}` → each key/value emitted as a repeated `body=$.x:v` / `attr=k:v` / `query=k:v` param; plus scalar `method`/`path`/`status`/`limit`/`cursor`. Reused by event-captures (TASK-011) and matched (TASK-012).
**Criteria:** unit test: an args map with `body`/`attr`/`method`/`limit` produces the correct `url.Values` (repeated `body=$.x:v`, `attr=tenant:acme`, etc.); `getWithQuery` appends `?<encoded>` and decodes the JSON response.

### TASK-002 — Per-group register hook convention [P]
**Phase:** Foundation
**Dependencies:** none
**Files:** `internal/mcp/server.go` (no behavior change — confirm `NewServer` can call `register*(s, c)` funcs; extract the existing rules/sims registrations into `registerRuleTools`/`registerSimulationTools` for consistency, optional but recommended)
**Description:** Establish the `register<Group>(s *server.MCPServer, c *Client)` convention so subsequent group tasks add a file + one call. Keep existing tools working (extraction is behavior-preserving).
**Criteria:** `go test ./internal/mcp/ -race` green; existing server_test tool-registration tests unchanged in behavior.

---

## Phase 2: Core Implementation (tool groups)

> Each group below lands its handlers/client methods in its OWN files and a `register<Group>` func. They do not touch each other's files, so TASK-010/012/013/014/015/016/017 are mutually `[P]` once their foundation deps are met. (TASK-011 depends on TASK-001.) The single shared serialization point is wiring the `register*` calls into `NewServer` (TASK-020).

### TASK-010 — Event-rule CRUD tools (P0) [P]
**Phase:** Core
**Dependencies:** TASK-002
**Files:** `internal/mcp/client_events.go`, `internal/mcp/tools_events.go`, `internal/mcp/tools_events_test.go`
**Description:** Client methods `ListEventRules`/`GetEventRules`(or filter from list)/`CreateEventRule`/`UpdateEventRule`/`DeleteEventRule` against `/api/event-rules[/{id}]` returning `domain.EventRule`. Tools `list_event_rules`, `create_event_rule` (object `event_rule`), `update_event_rule` (id + object), `delete_event_rule` (id). Descriptions document `EventRule` schema (`id`, `name`, `match{service,target,source,detail_type,attributes,message}`, optional `forward{endpoint,region,credential,delay_ms}`). `register EventRuleTools(s,c)`.
**Criteria:** httptest admin stub: each tool hits the right method+path with the right body; create with a valid SNS event rule round-trips; `go test ./internal/mcp/ -race` green.

### TASK-011 — Event-capture inspection tools (P0)
**Phase:** Core
**Dependencies:** TASK-001, TASK-002
**Files:** `internal/mcp/client_captures.go`, `internal/mcp/tools_captures.go`, `internal/mcp/tools_captures_test.go`
**Description:** Client methods `ListEventCaptures(rule string, q url.Values) (matched.Page, error)`, `GetEventCapture(rule, id) (*matched.FullRequest, error)`, `ClearEventCaptures(rule string) error` against `/api/event-captures/{rule}[/{id}]`. Tools `list_event_captures` (rule + optional `body`/`attr`/`query`/`method`/`status`/`limit`/`cursor` via TASK-001 helper), `get_event_capture` (rule + id), `clear_event_captures` (rule). Description highlights the body/attr filters and the captured fields (`identity`, `forwarded`, `forward_target`, request body).
**Criteria:** httptest stub returning a `matched.Page`: `list_event_captures` with `body={"$.correlation_id":"abc"}` produces `GET /api/event-captures/orders?body=$.correlation_id:abc`; detail + clear hit the right routes; tests green.

### TASK-012 — Matched HTTP-capture inspection tools (P1)
**Phase:** Core
**Dependencies:** TASK-001, TASK-002
**Files:** `internal/mcp/client_matched.go`, `internal/mcp/tools_matched.go`, `internal/mcp/tools_matched_test.go`
**Description:** Client methods `ListMatched(rule, q)`, `GetMatched(rule, id)`, `ClearMatched(rule)` against `/api/matched/{rule}[/{id}]`. Tools `list_matched`, `get_matched`, `clear_matched` (same filter helper from TASK-001; `attr` omitted — HTTP has no message attributes, `query`/`body` apply). `registerMatchedTools(s,c)`.
**Criteria:** httptest stub: list with `body`/`query`/`headers` filters builds the right query string; detail returns full request incl. bodies; tests green.

### TASK-013 — Fault-profile CRUD tools (P2) [P]
**Phase:** Core
**Dependencies:** TASK-002
**Files:** `internal/mcp/client_chaos.go`, `internal/mcp/tools_chaos.go`, `internal/mcp/tools_chaos_test.go`
**Description:** Client methods + tools for `/api/faults[/{id}]`: `list_faults`, `get_fault`, `create_fault` (object `fault_profile` → `domain.FaultProfile`), `update_fault`, `delete_fault`. Description documents fault types (jitter/error/hang/reset/halfResponse/slowBody/retryStorm). `registerFaultTools(s,c)`. (Shares the `client_chaos.go`/`tools_chaos.go` files with TASK-015 — so TASK-013 and TASK-015 are NOT mutually `[P]`; sequence them.)
**Criteria:** httptest stub: CRUD round-trips a fault profile; tests green.

### TASK-014 — Scenario tools (P2) [P]
**Phase:** Core
**Dependencies:** TASK-002
**Files:** `internal/mcp/client_scenarios.go`, `internal/mcp/tools_scenarios.go`, `internal/mcp/tools_scenarios_test.go`
**Description:** Client methods + tools for `/api/scenarios[/{id}]` + start/stop: `list_scenarios`, `get_scenario`, `create_scenario` (object → `domain.Scenario`), `update_scenario`, `delete_scenario`, `start_scenario` (id → POST `/api/scenarios/{id}/start`), `stop_scenario` (id → `/stop`). `registerScenarioTools(s,c)`.
**Criteria:** httptest stub: CRUD + start (202) + stop hit the right routes; tests green.

### TASK-015 — Chaos control tools (P2)
**Phase:** Core
**Dependencies:** TASK-013 (same files: `client_chaos.go`/`tools_chaos.go`)
**Files:** `internal/mcp/client_chaos.go`, `internal/mcp/tools_chaos.go`, `internal/mcp/tools_chaos_test.go`
**Description:** Tools `halt_chaos` (POST `/api/chaos/halt`), `resume_chaos` (POST `/api/chaos/resume`), `get_chaos_status` (GET `/api/chaos/status`). Fold into `registerFaultTools` or add `registerChaosControlTools`.
**Criteria:** httptest stub: halt/resume/status hit the right routes; tests green.

### TASK-016 — Import/export tools (P3) [P]
**Phase:** Core
**Dependencies:** TASK-002
**Files:** `internal/mcp/client_transfer.go`, `internal/mcp/tools_transfer.go`, `internal/mcp/tools_transfer_test.go`
**Description:** Client methods + tools for `/api/export` (GET → full config JSON) and `/api/import` + `/api/import/preview` (POST): `export_config`, `import_config` (object `config` + optional bool `preview` → routes to `/import/preview` vs `/import`). `registerTransferTools(s,c)`.
**Criteria:** httptest stub: export returns config; import with `preview=true` hits the preview route; tests green.

### TASK-017 — Dev-ergonomics tools (P4) [P]
**Phase:** Core
**Dependencies:** TASK-002
**Files:** `internal/mcp/client_dev.go`, `internal/mcp/tools_dev.go`, `internal/mcp/tools_dev_test.go`
**Description:** `eval_script` (POST `/api/script/eval` with a script + sample request) and `get_metrics_history` (GET `/api/metrics/history`). `registerDevTools(s,c)`.
**Criteria:** httptest stub: script eval posts the body; history returns the series; tests green.

---

## Phase 3: Integration

### TASK-020 — Wire register hooks into NewServer + MCP e2e test
**Phase:** Integration
**Dependencies:** TASK-010 … TASK-017 (all group tasks)
**Files:** `internal/mcp/server.go` (call every `register*(s, c)`), `cmd/mockwave/mcp.go` (extend help text with the new tool groups), `internal/mcp/server_test.go` or a new `internal/mcp/integration_test.go`
**Description:** Add the `register*` calls to `NewServer`; update the `mcp` command help. Add an integration test that boots a real admin server (`server.New` + `srv.AdminMux()` on httptest), points `mcp.NewClient` at it, and exercises one representative tool from each new group end-to-end (e.g. create_event_rule → publish via the aws handler → list_event_captures with a body filter → get_event_capture).
**Criteria:** `make test` green; the e2e asserts a real round-trip for event-rule create + event-capture list-by-body; `go run ./cmd/mockwave mcp --help` shows the new tools; coverage gate ≥80%.

---

## Phase 4: Polish

### TASK-030 — Docs
**Phase:** Polish
**Dependencies:** TASK-020
**Files:** `README.md` (MCP section — list the new tool groups), `docs/event-capture.md` (note: event rules + captures are manageable via MCP), `CLAUDE.md` tip if present
**Description:** Document the new MCP tools and example Claude prompts ("intercept SNS to the orders topic and forward to real AWS", "what did the app publish with correlation_id abc-123?").
**Criteria:** README MCP tool list updated; links resolve; `grep` confirms the new tool names are documented.

---

## Dependency / parallelism summary

```
TASK-001 ─┐
TASK-002 ─┼─► TASK-010 [P] ─┐
          ├─► TASK-011      │
          ├─► TASK-012      │
          ├─► TASK-013 ─► TASK-015   (same files → serial)
          ├─► TASK-014 [P] │
          ├─► TASK-016 [P] │
          └─► TASK-017 [P] ─┴─► TASK-020 ─► TASK-030
```

- **`[P]` groups (own files, no overlap):** TASK-010, TASK-012*, TASK-013, TASK-014, TASK-016, TASK-017 can proceed concurrently after Phase 0. (*TASK-011/012 additionally depend on TASK-001's filter helper.)
- **Serial pairs:** TASK-013 → TASK-015 (share `*_chaos.go`).
- **Fan-in:** TASK-020 needs all group tasks (it wires `NewServer`). **Note:** the one shared edit each group makes is a single `register*` line in `NewServer` — to keep groups truly non-conflicting, defer ALL `NewServer` edits to TASK-020 (groups only define their `register*` func; TASK-020 adds the calls).

## Validation
- Every audit gap (P0–P4) maps to a task: event-rules→010, event-captures→011, matched→012, faults→013, scenarios→014, chaos→015, import/export→016, script-eval/metrics-history→017.
- Each task has httptest-stub unit criteria; TASK-020 adds a live-admin e2e.
- Coverage gate ≥80% enforced at TASK-020.
