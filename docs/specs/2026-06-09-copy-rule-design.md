# Copy Rule — Design

Date: 2026-06-09
Status: Approved

## Goal

Add a "Copy" button to each rule in the rules table. Clicking it opens the rule
modal pre-filled with the source rule's data so the user can edit and save a new
rule. Saving creates a brand-new rule (never mutates the source). Duplicate rules
(identical full match criteria) are rejected on every save.

## Requirements

1. Copy button per rule row opens a modal to edit a new record.
2. If a simulation is changed in the copy modal, a new simulation is created;
   unchanged simulations stay shared with the source rule (its `simulation_id` is
   reused, original simulation never mutated).
3. Save is blocked if another rule already has the same full match criteria
   (protocol + method + path/url + headers + query + body). Applies to all rule
   saves (create, edit, copy), not just copy.

## Data Model (existing, for reference)

`domain/model.go`:

- `Rule { ID, Name, Match MatchCriteria, Buckets []WeightedBucket }`
- `MatchCriteria { Protocol, Method, Path string; Headers, Query, Body map[string]string }`
- `WeightedBucket { Weight, Action, SimulationID, DelayMs, ForwardURL }`
- `Simulation { ID, Protocol, Response, Script, ... }`
- `Config { Rules []Rule; Simulations []Simulation }`

Rules and simulations stored as parallel arrays in a JSON file. Buckets reference
simulations by `SimulationID`.

## Behavior

### Copy button + modal

- New "Copy" button in the action column of the rules table
  (`internal/adapters/cfg/restapi/static/index.html`, rules-tbody render in
  `loadRules()`), beside Edit and Delete.
- `copyRule(id)`:
  - Fetch source rule and its referenced simulations (same path as edit flow).
  - Pre-fill all form fields: match criteria, header/query/body matchers, buckets,
    and inline simulation bodies.
  - Set modal mode = `copy`.
  - Generate a fresh `uuid()` for the new rule ID and store it internally. The ID
    field is hidden/readonly in copy mode — name identifies the rule in the table.
  - Snapshot each `simulate` bucket's original simulation (`simulation_id` + body
    fields) for later change detection.
  - Modal title = "Copy Rule". Save performs a POST (new rule).

### Save — copy mode

For each bucket on save in copy mode:

- `forward` bucket: unchanged.
- `simulate` bucket: compare current simulation fields against the snapshot.
  - Unchanged → keep the original `simulation_id`. No simulation write.
  - Changed → POST a new simulation with a new `uuid()`; bucket points to the new
    id. The original simulation is never PUT/mutated.

Then POST the new rule.

### Dedup — all saves

A rule is a duplicate if another rule has identical full `MatchCriteria`:
protocol + method + path + headers + query + body, by deep equality (map compare
is order-independent). On edit, the rule being saved is excluded from the check by
its own ID.

Enforced in two layers:

- **Server (authoritative):** dedup check in the store's `SaveRule`
  (`internal/adapters/out/jsonfile/store.go`). On conflict, return a conflict
  error; `server.go` maps it to HTTP 409.
- **Client (UX):** `saveRule()` runs a pre-check against the already-loaded rules
  list and shows an inline error before POSTing. Server remains the source of
  truth.

## Files Touched

- `internal/adapters/cfg/restapi/static/index.html`
  - Copy button in `loadRules()` row render.
  - `copyRule(id)` function.
  - Copy-mode branch + simulation change-detection in `saveRule()`.
  - Client-side dedup pre-check + inline error.
- `domain/model.go`
  - `MatchCriteria.Equal(other)` (deep, order-independent map compare).
  - Dedup helper to find a conflicting rule excluding a given ID.
- `internal/adapters/out/jsonfile/store.go`
  - Dedup enforcement in `SaveRule`; return conflict error.
- `internal/adapters/cfg/restapi/server.go`
  - Map conflict error → HTTP 409.

## Edge Cases

- Copy with no edits = identical match criteria → dedup blocks the save. Expected:
  the user must change a matcher, or the copy is genuinely redundant.
- Two rules differing only by buckets/simulations but with identical match criteria
  are blocked. Intended.
- Empty maps vs nil maps in `MatchCriteria` must compare equal in `Equal()`.

## Testing

- `MatchCriteria.Equal`: equal, differing-by-each-field, nil-vs-empty-map cases.
- Store dedup: save conflicting rule rejected; save with self-ID (edit) allowed;
  non-conflicting save allowed.
- Server: POST duplicate → 409.
- Manual UI: copy a rule, edit a simulation, save → new rule + new simulation,
  source rule and its simulation unchanged; copy without changes → blocked.
