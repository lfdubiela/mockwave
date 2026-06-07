# In-Memory Data + Version-Poll Reloader — Design

**Date:** 2026-06-07
**Status:** Approved (design)

## Goal

Serve all rule and simulation data from in-memory snapshots (zero store reads on
the request hot path) and keep multiple pods converged on the latest config via a
periodic version-poll reloader, with immediate local reload on admin writes.

## Motivation

Today, rule matching uses an in-memory snapshot (loaded at boot / on admin
reload), but each matched request fetches its simulation **live** from the store —
two `GetSimulation` calls per request against DynamoDB (one in the simulation
stage, one in the script stage). There is no periodic sync: a pod only reloads on
its own admin mutation or an explicit `POST /api/reload`. In a multi-pod
deployment, a write handled by pod A leaves pods B/C stale indefinitely, and the
per-request simulation reads add latency and cost.

## Requirements

1. **All data in memory.** Rules and simulations both served from in-memory
   snapshots. A matched request performs **zero** store reads.
2. **Periodic reloader.** A ticker (default 15s, configurable) refreshes the
   in-memory snapshots so every pod converges within the interval.
3. **Cheap polling.** Use a store "config version" marker: each tick reads only
   the marker; a full reload runs only when the marker changed.
4. **Immediate local reload on writes** (unchanged): an admin/API mutation
   reloads the handling pod instantly and bumps the version so other pods reload
   on their next tick.
5. **Remote stores only.** Reloader engages for Dynamo/Mongo/Cosmos. The JSON
   store (CLI default) keeps today's behavior exactly.
6. **CLI behavior preserved.** `mockwave start --store json` is unchanged (no
   ticker). A CLI run against a remote store gets the reloader (correct).

## Non-Goals

- External pub/sub (Redis/NATS/SNS) for push invalidation.
- Auto-reload of the JSON file on external edits.
- Sub-second cross-pod convergence (bounded by the poll interval).

## Architecture

### 1. Store capability — `store.VersionedStore`

In `store/` (the public ports package), add an opt-in capability:

```go
// VersionedStore is an optional capability: stores that can report a monotonic
// config version enable the periodic version-poll reloader.
type VersionedStore interface {
    ConfigVersion() (int64, error)
}
```

The base `DataStore` interface is unchanged. Consumers use a type assertion
(`vs, ok := store.(VersionedStore)`); the JSON store does not implement it.

**Auto-bump on write.** Each remote store increments a reserved marker inside its
existing rules table/collection on every mutating method (`SaveRule`,
`SaveSimulation`, `DeleteRule`, `DeleteSimulation`):

- **DynamoDB:** reserved item `id = "__mockwave_config_version__"` in the rules
  table; bump via `UpdateItem` with `UpdateExpression: "ADD #v :one"`
  (`#v` = `version` attribute, `:one` = 1). `ConfigVersion()` = `GetItem` on that
  key, returns the `version` number (0 if absent).
- **MongoDB / Cosmos (Mongo API):** reserved document `_id =
  "__mockwave_config_version__"` in the rules collection; bump via
  `UpdateOne(..., {$inc: {version: 1}}, upsert=true)`. `ConfigVersion()` =
  `FindOne` returning `version` (0 if absent).

The reserved marker id is excluded from `GetRules()` results (filter by id prefix
`__mockwave_`) so it never parses as a rule.

Bumping is encapsulated in the store, so all write paths (admin REST, MCP via
admin REST) bump the version with no caller changes.

### 2. In-memory snapshots

`server.rebuild()` currently loads rules and builds the matching stage in memory.
Extend it to also load **all simulations** once and build a snapshot map:

```go
sims, err := s.cfg.Store.ListSimulations()   // []domain.Simulation
simMap := make(map[string]domain.Simulation, len(sims))
for _, sim := range sims { simMap[sim.ID] = sim }
```

Change the hot path to read from the snapshot instead of the store:

- `simulation.SimulationStage` takes a `map[string]domain.Simulation` (or a small
  `SimulationSource` lookup interface backed by the map) instead of a
  `store.DataStore`. `Execute` looks up `pctx.SimulationID` in the map; miss →
  same not-found error as today.
- The script-stage `getScript` closure reads `simMap[pctx.SimulationID].Script`
  instead of calling `store.GetSimulation`.

Net: a matched request makes **0** store calls. Admin REST GET endpoints continue
to read the store live (admin reads are not latency-critical and must reflect
writes immediately).

### 3. Reloader — `internal/reload`

New package with a single responsibility: poll the version and trigger reloads.

```go
type Reloader struct {
    store    store.VersionedStore
    interval time.Duration
    reload   func() error   // server.Rebuild
    log      observability.Logger
}

func (r *Reloader) Run(ctx context.Context) {
    last := int64(-1)
    t := time.NewTicker(r.interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            v, err := r.store.ConfigVersion()
            if err != nil { r.log.Warn("reload: version read failed", ...); continue }
            if v == last { continue }
            if err := r.reload(); err != nil { r.log.Error("reload failed", ...); continue }
            last = v
        }
    }
}
```

Started in `server.New()` only when `cfg.Store` satisfies `store.VersionedStore`:

```go
if vs, ok := s.cfg.Store.(store.VersionedStore); ok {
    rl := reload.New(vs, s.cfg.reloadInterval(), s.Rebuild, s.cfg.Logger)
    ctx, cancel := context.WithCancel(context.Background())
    s.reloadCancel = cancel
    go rl.Run(ctx)
}
```

- First tick reloads unconditionally (`last = -1`).
- Version read error → log + skip tick (keep serving the last good snapshot).
- `server.Shutdown()` calls `s.reloadCancel()` (mirrors `brokerCancel`).
- The existing admin `onReload` → `server.Rebuild()` path is unchanged, so a write
  reloads the local pod instantly; the version bump propagates to peers via their
  next tick.

### 4. Config, env, CLI

- `server.Config` gains `ReloadInterval time.Duration`. `0` resolves to the
  default **15s** via a `reloadInterval()` helper.
- Env: `MOCKWAVE_RELOAD_INTERVAL` (Go duration string, e.g. `15s`) read where the
  other `MOCKWAVE_*` store env vars are handled; invalid/empty → default.
- CLI `start`: add `--reload-interval` flag (default `15s`) mapped to
  `Config.ReloadInterval`. `--store json` → store is not `VersionedStore` → no
  reloader → unchanged behavior. `--store dynamodb|mongo|cosmos` → reloader runs.

## Data Flow

```
write (admin REST / MCP) ─▶ store.SaveRule/... ─▶ bump __mockwave_config_version__
                                              └▶ onReload() ─▶ server.Rebuild() (local pod)

each pod, every interval:
   store.ConfigVersion()  ── unchanged ─▶ skip
                          └─ changed ───▶ server.Rebuild() ─▶ reload rules+sims into memory

request ─▶ match (memory) ─▶ route (memory) ─▶ simulation (memory map) ─▶ response   // 0 store reads
```

## Error Handling

- `ConfigVersion()` error on a tick: logged at warn, tick skipped, last snapshot
  kept serving.
- `Rebuild()` error on a tick: logged at error, `last` NOT advanced (so the next
  tick retries the same version).
- Marker absent (fresh table): `ConfigVersion()` returns 0; first tick still
  reloads because `last = -1`.
- A simulation referenced by a rule but missing from the snapshot: same
  not-found/error response as today (no silent fallback).

## Testing

- **Store unit tests** (mock client): `ConfigVersion()` returns the marker;
  `SaveRule`/`SaveSimulation`/`DeleteRule`/`DeleteSimulation` each issue the
  atomic bump; `GetRules()` excludes the reserved marker id.
- **Reloader unit test** (fake `VersionedStore`): `Rebuild` called on first tick;
  not called when version unchanged; called again when version changes; a
  `ConfigVersion` error skips the tick without advancing `last`.
- **In-memory simulation test:** a matched request resolves its response from the
  snapshot with **0** `GetSimulation` calls (spy store asserts zero hot-path
  reads).
- **Integration (dynamodb-local, `integration` tag):** two `server` instances
  sharing one table; a write through instance A bumps the version; instance B
  reflects the change within the configured interval.

## File Touch List

- `store/store.go` — add `VersionedStore` interface.
- `internal/adapters/out/dynamodb/store.go` — marker bump on writes,
  `ConfigVersion()`, exclude marker from `GetRules`.
- `internal/adapters/out/mongodb/store.go` — same.
- `internal/adapters/out/cosmos/store.go` — same.
- `internal/domain/simulation/loader.go` — `SimulationStage` reads a snapshot map.
- `server/server.go` — `rebuild()` builds the sim snapshot; wire script-stage
  closure to the map; start/stop reloader; `ReloadInterval` + helper.
- `internal/reload/reload.go` (new) + `reload_test.go`.
- `server/config.go` (or wherever `Config` lives) — `ReloadInterval` field.
- `cmd/mockwave/main.go` — `--reload-interval` flag; env parse.
- Store `*_test.go`, simulation/pipeline tests, dynamo integration test.

## Backward Compatibility

- JSON store / CLI default: no interface change, no reloader, identical behavior.
- Remote stores: a new reserved marker id appears in the rules table/collection;
  excluded from `GetRules`. Pre-existing tables work (marker auto-created on first
  write; `ConfigVersion` returns 0 until then).
- No public API breaks; `VersionedStore` is additive and optional.
