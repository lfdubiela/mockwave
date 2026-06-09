# Rule-Owned Simulations — Design

Date: 2026-06-09
Status: Approved

## Goal

Make simulations conceptually owned by a single rule. A rule has many
simulations (one per simulate bucket); a simulation belongs to exactly one rule.
Cross-rule reuse is dropped (rarely used in practice). The standalone
Simulations tab is removed; simulations are created/edited only inside the rule
editor.

## Decisions

- Storage shape unchanged: `Config.Simulations[]` array + `WeightedBucket.SimulationID`
  reference. Ownership enforced by handler convention, not by struct change.
- Runtime unchanged: simulations still looked up by ID from the shared map
  (`internal/domain/simulation/loader.go`). No model/matching refactor.
- Remove the Simulations tab entirely.

## Behavior

### 1. Remove Simulations tab (UI)

In `internal/adapters/cfg/restapi/static/index.html`:

- Remove the Simulations tab nav button and its `#tab-simulations` content.
- Remove the standalone simulation modal markup.
- Remove JS: `openSimModal`, `saveSim`, `deleteSim`, `loadSims`, and every
  `loadSims()` call site.
- Simulations are created and edited only through rule-editor buckets (already
  the existing inline flow).

### 2. Cascade-delete owned simulations (backend, backend-agnostic)

In `internal/adapters/cfg/restapi/server.go`, enforce ownership on rule writes by
iterating the store's own delete (works for any DataStore):

- `DELETE /api/rules/{id}`: collect the rule's simulate-bucket `simulation_id`s
  and delete each simulation, then delete the rule.
- `DELETE /api/rules` (clean all): delete every rule's simulations, then the
  rules — a true blank slate.
- `PUT /api/rules/{id}` (edit): compute `removed = oldSimIDs − newSimIDs` and
  delete those simulations, so removing a bucket leaves no orphan. Compute from
  the rule's previous state (fetched via `GetRules`) before saving.

Deletion of a simulation that is already absent is treated as non-fatal (ignore
not-found), so cascade is idempotent.

### 3. Copy clones simulations

In `static/index.html`, `copyRule` / `saveRule`:

- Drop the shared-simulation path (`sharedSimID`, `simSnapshot`,
  `bucketSimSnapshot`, and the copy-mode short-circuit in `saveRule`).
- A copied rule always creates fresh simulations with new IDs (the existing
  "new simulate bucket" POST path). No cross-rule sharing remains.

### 4. Model / runtime

Unchanged. `Simulations[]` and `simulation_id` stay. Ownership is a handler-level
invariant.

## Files Touched

- `internal/adapters/cfg/restapi/static/index.html` — remove sims tab + sim JS;
  simplify `copyRule`/`saveRule` to clone simulations.
- `internal/adapters/cfg/restapi/server.go` — cascade-delete in `ruleByID`
  (DELETE + PUT) and bulk `rules` DELETE.
- `internal/adapters/cfg/restapi/server_test.go` — cascade + copy-clone coverage.

## Out of Scope

- Pre-existing orphan simulations in old config files remain (harmless, now
  invisible). No migration.
- No structural embedding of simulations into buckets.

## Testing

- DELETE rule removes the rule and its owned simulations; leaves unrelated
  simulations untouched.
- DELETE all rules clears all simulations.
- PUT rule that drops a simulate bucket deletes the orphaned simulation; keeps
  retained ones.
- Cascade is idempotent when a referenced simulation is already missing.
- Manual UI: Simulations tab gone; copy a rule, confirm new simulations created
  with new IDs and the source rule's simulations unchanged.
