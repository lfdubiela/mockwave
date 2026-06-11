# Remote Chaos Stores — Design

**Date:** 2026-06-11
**Status:** Approved pending review

## Goal

Implement `store.FaultStore` and `store.ScenarioStore` on the DynamoDB and MongoDB backends (Cosmos inherits via MongoDB delegation), so chaos fault profiles and scenarios work with persisted remote stores — today they are jsonfile-only and remote stores return `501`.

## Scope

- DynamoDB: implement both optional capabilities.
- MongoDB: implement both. Cosmos delegates entirely to MongoDB → free.
- Out of scope: automatic table/collection creation (app never auto-creates; documented), data migration.

## Storage layout

Mirror the existing rules/simulations split — separate table (Dynamo) / collection (Mongo) per entity, each item a JSON/BSON blob keyed by `id`/`_id`.

| Entity | Dynamo table (default) | Mongo collection |
|---|---|---|
| Fault profiles | `mockwave-fault-profiles` | `fault_profiles` |
| Scenarios | `mockwave-scenarios` | `scenarios` |

## Version marker / hot reload

Writes to fault profiles and scenarios MUST call the existing `bumpVersion()` (which lives in the rules table/collection). This makes a remote-store edit bump the config version, so the `VersionedStore` poll-reloader rebuilds the pipeline and the `FaultStage` picks up fresh profiles/scenarios — same mechanism rules/simulations already use.

## DynamoDB

`dynamostore.Config` gains `FaultsTable string`, `ScenariosTable string`. Store struct gains `faultsTable`, `scenariosTable`. New methods, mirroring the rules/sims pattern (`data` string attribute holds the JSON blob; PK `id`):

- `ListFaultProfiles` → Scan faults table, unmarshal each `data`.
- `GetFaultProfile(id)` → GetItem; `out.Item == nil` → `(nil, nil)`.
- `SaveFaultProfile(p)` → PutItem `{id, data}` + `bumpVersion()`.
- `DeleteFaultProfile(id)` → DeleteItem + `bumpVersion()`.
- Same four for Scenario.

Compile-time checks: `var _ store.FaultStore = (*Store)(nil)`, `var _ store.ScenarioStore = (*Store)(nil)`.

## MongoDB

Add `colFaults = "fault_profiles"`, `colScenarios = "scenarios"`. New collection fields `faults`, `scenarios` on Store, set in `NewStoreFromClient`. Doc shapes `faultDoc{ID string bson:"_id"; Data domain.FaultProfile bson:"data"}`, `scenarioDoc{...domain.Scenario}`. Methods mirror rules/sims (`Find`/`UpdateOne` upsert/`DeleteOne`), each write calling `bumpVersion()`. `Get*` returns `(nil, nil)` when no document. Compile-time checks added. Cosmos: no code (delegates).

## Wiring

- `cmd/mockwave/main.go`: flags `--dynamo-faults-table` (default `mockwave-fault-profiles`), `--dynamo-scenarios-table` (default `mockwave-scenarios`); thread into `dynamostore.Config` in `buildStore`. `storeOpts` gains the two fields.
- `server/store.go` `buildStoreFromEnv`: env `MOCKWAVE_DYNAMO_FAULTS_TABLE`, `MOCKWAVE_DYNAMO_SCENARIOS_TABLE` (same defaults), threaded into the dynamo Config.
- Admin API, import/export, FaultStage already type-assert the optional capabilities — no changes there; they light up automatically once the backends implement the interfaces.

## Testing

- DynamoDB: unit tests with the existing mock `DynamoClient` (assert table names + marshal/unmarshal + version bump on writes) and integration tests against dynamodb-local (create the two new tables, full CRUD round-trip). Mirror the rules/sims tests.
- MongoDB: mtest-based unit tests mirroring the rules/sims mtest cases.
- Update `store_factory_test.go` if it asserts the dynamo Config shape.

## Docs

- README "Chaos Testing": remove the "jsonfile only / 501 on remote stores" caveat; note remote stores now support chaos and that the two new Dynamo tables must be created (PK `id`, `PayPerRequest`), Mongo collections auto-create.
- `docs/extending.md`: update the FaultStore/ScenarioStore notes to list all implementing backends.

## Roadmap (not now)

- Auto-create tables on startup (would change the no-auto-create convention).
