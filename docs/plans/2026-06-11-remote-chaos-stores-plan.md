# Remote Chaos Stores Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `store.FaultStore` and `store.ScenarioStore` on the DynamoDB and MongoDB backends (Cosmos inherits free), so chaos profiles/scenarios work with persisted remote stores instead of returning 501.

**Architecture:** Mirror the existing rules/simulations storage pattern in each backend — a separate Dynamo table / Mongo collection per entity, each item a JSON/BSON blob keyed by id. Fault/scenario writes call the existing `bumpVersion()` so the version-poll reloader rebuilds the pipeline and the FaultStage picks up changes. Admin API, import/export and FaultStage already type-assert these optional capabilities, so they light up automatically once the backends implement them.

**Tech Stack:** Go 1.26, aws-sdk-go-v2 (DynamoDB), mongo-driver (+ mtest), testify, dynamodb-local for integration.

**Spec:** `docs/specs/2026-06-11-remote-chaos-stores-design.md`.

---

### Task 1: DynamoDB — FaultStore

**Files:**
- Modify: `internal/adapters/out/dynamodb/store.go`
- Test: `internal/adapters/out/dynamodb/store_test.go`

The store currently has `Config{RulesTable, SimsTable, Region, Endpoint}` and a `Store{client, rulesTable, simsTable}`. The mock `DynamoClient` test type already records `putItems`/`delItems` and serves `scanOut`/`getOut` keyed by table name.

- [ ] **Step 1: Write failing tests** (append to `internal/adapters/out/dynamodb/store_test.go`; follow the existing mock-client test style — inspect `mockClient` and how `TestStore_SaveRule`/`TestStore_GetSimulation` set up `scanOut`/`getOut` first)

```go
func TestStore_FaultProfileCRUD(t *testing.T) {
	client := &mockClient{
		getOut: map[string]*dynamodb.GetItemOutput{},
		scanOut: map[string]*dynamodb.ScanOutput{},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
		FaultsTable: "faults", ScenariosTable: "scenarios",
	})

	// Save → PutItem to faults table + version bump (UpdateItem on rules table)
	p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}}
	require.NoError(t, s.SaveFaultProfile(p))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "faults", aws.ToString(client.putItems[0].TableName))

	// Get hit
	data, _ := json.Marshal(p)
	client.getOut["faults"] = &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: "fp1"},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}}
	got, err := s.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "p", got.Name)

	// Get miss → (nil, nil)
	client.getOut["faults"] = &dynamodb.GetItemOutput{Item: nil}
	got, err = s.GetFaultProfile("missing")
	require.NoError(t, err)
	assert.Nil(t, got)

	// List → Scan
	client.scanOut["faults"] = &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
		{"id": &types.AttributeValueMemberS{Value: "fp1"}, "data": &types.AttributeValueMemberS{Value: string(data)}},
	}}
	list, err := s.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "fp1", list[0].ID)

	// Delete → DeleteItem on faults table
	require.NoError(t, s.DeleteFaultProfile("fp1"))
	assert.Equal(t, "faults", aws.ToString(client.delItems[len(client.delItems)-1].TableName))
}
```

Check the actual field names on `mockClient` (it may be `scanOut`/`getOut`/`putItems`/`delItems` or similar) and adapt the literals. If the mock's UpdateItem isn't recorded, the version-bump assertion can be omitted — `SaveFaultProfile` calling `bumpVersion()` is still exercised (no error from the mock).

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/adapters/out/dynamodb/ -run TestStore_FaultProfileCRUD -v`
Expected: FAIL (undefined Config.FaultsTable / methods)

- [ ] **Step 3: Implement** — add to `Config`:

```go
	FaultsTable    string // DynamoDB table for fault profiles (PK: "id")
	ScenariosTable string // DynamoDB table for scenarios (PK: "id")
```

Add to `Store` struct: `faultsTable string`, `scenariosTable string`. Set them in `NewStoreFromClient`:

```go
	return &Store{
		client:         client,
		rulesTable:     cfg.RulesTable,
		simsTable:      cfg.SimsTable,
		faultsTable:    cfg.FaultsTable,
		scenariosTable: cfg.ScenariosTable,
	}
```

Add the compile-time check beside the existing ones:

```go
	_ store.FaultStore = (*Store)(nil)
```

Add the four methods (mirror SaveSimulation/GetSimulation/ListSimulations/DeleteSimulation against `faultsTable`):

```go
func (s *Store) ListFaultProfiles() ([]domain.FaultProfile, error) {
	out, err := s.client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(s.faultsTable),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: scan fault profiles: %w", err)
	}
	profiles := make([]domain.FaultProfile, 0, len(out.Items))
	for _, item := range out.Items {
		dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var p domain.FaultProfile
		if err := json.Unmarshal([]byte(dataAttr.Value), &p); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal fault profile: %w", err)
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

func (s *Store) GetFaultProfile(id string) (*domain.FaultProfile, error) {
	out, err := s.client.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(s.faultsTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: get fault profile %q: %w", id, err)
	}
	if out.Item == nil {
		return nil, nil
	}
	dataAttr, ok := out.Item["data"].(*types.AttributeValueMemberS)
	if !ok {
		return nil, nil
	}
	var p domain.FaultProfile
	if err := json.Unmarshal([]byte(dataAttr.Value), &p); err != nil {
		return nil, fmt.Errorf("dynamodb: unmarshal fault profile: %w", err)
	}
	return &p, nil
}

func (s *Store) SaveFaultProfile(p domain.FaultProfile) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal fault profile: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.faultsTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: p.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	if err := wrapErr(err, "put fault profile %q", p.ID); err != nil {
		return err
	}
	return s.bumpVersion()
}

func (s *Store) DeleteFaultProfile(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.faultsTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err := wrapErr(err, "delete fault profile %q", id); err != nil {
		return err
	}
	return s.bumpVersion()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/adapters/out/dynamodb/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/dynamodb/store.go internal/adapters/out/dynamodb/store_test.go
git commit -m "feat(dynamodb): FaultStore implementation"
```

---

### Task 2: DynamoDB — ScenarioStore

**Files:**
- Modify: `internal/adapters/out/dynamodb/store.go`
- Test: `internal/adapters/out/dynamodb/store_test.go`

- [ ] **Step 1: Write failing test** (append; same mock-client style as Task 1)

```go
func TestStore_ScenarioCRUD(t *testing.T) {
	client := &mockClient{
		getOut:  map[string]*dynamodb.GetItemOutput{},
		scanOut: map[string]*dynamodb.ScanOutput{},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
		FaultsTable: "faults", ScenariosTable: "scenarios",
	})
	sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}

	require.NoError(t, s.SaveScenario(sc))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "scenarios", aws.ToString(client.putItems[0].TableName))

	data, _ := json.Marshal(sc)
	client.getOut["scenarios"] = &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: "sc1"},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}}
	got, err := s.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "n", got.Name)

	client.getOut["scenarios"] = &dynamodb.GetItemOutput{Item: nil}
	got, err = s.GetScenario("missing")
	require.NoError(t, err)
	assert.Nil(t, got)

	client.scanOut["scenarios"] = &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
		{"id": &types.AttributeValueMemberS{Value: "sc1"}, "data": &types.AttributeValueMemberS{Value: string(data)}},
	}}
	list, err := s.ListScenarios()
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, s.DeleteScenario("sc1"))
	assert.Equal(t, "scenarios", aws.ToString(client.delItems[len(client.delItems)-1].TableName))
}
```

- [ ] **Step 2: Run, verify failure** — FAIL (undefined methods).

- [ ] **Step 3: Implement** — add compile-time check `_ store.ScenarioStore = (*Store)(nil)` and four methods identical in shape to Task 1 but operating on `s.scenariosTable` with `domain.Scenario` ("scenario" in error strings). Copy the Task 1 method bodies, substituting type `domain.Scenario`, table `s.scenariosTable`, error nouns "scenario".

- [ ] **Step 4: Run tests** — `go test ./internal/adapters/out/dynamodb/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/dynamodb/store.go internal/adapters/out/dynamodb/store_test.go
git commit -m "feat(dynamodb): ScenarioStore implementation"
```

---

### Task 3: MongoDB — FaultStore + ScenarioStore

**Files:**
- Modify: `internal/adapters/out/mongodb/store.go`
- Test: `internal/adapters/out/mongodb/store_test.go`

The store has `colRules = "rules"`, `colSims = "simulations"`, doc shapes `ruleDoc`/`simDoc`, collection fields `rules`/`sims` set in `NewStoreFromClient`, and `bumpVersion()`.

- [ ] **Step 1: Write failing mtest tests** (append to `internal/adapters/out/mongodb/store_test.go`; study an existing mtest case — e.g. the SaveRule/GetSimulation mtest — and mirror its `mt.AddMockResponses(...)` setup. mtest needs the mongo find/update command responses mocked.)

```go
func TestStore_SaveFaultProfile(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateSuccessResponse(), // UpdateOne (fault_profiles upsert)
			mtest.CreateSuccessResponse(), // bumpVersion UpdateOne
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		err := s.SaveFaultProfile(domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}})
		require.NoError(mt, err)
	})
}

func TestStore_GetFaultProfile_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("miss returns nil,nil", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.fault_profiles", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetFaultProfile("nope")
		require.NoError(mt, err)
		assert.Nil(mt, got)
	})
}

func TestStore_GetFaultProfile_Hit(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("hit decodes data", func(mt *mtest.T) {
		p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}}
		doc := bson.D{{Key: "_id", Value: "fp1"}, {Key: "data", Value: p}}
		mt.AddMockResponses(mtest.CreateCursorResponse(1, "mockwave.fault_profiles", mtest.FirstBatch, doc))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetFaultProfile("fp1")
		require.NoError(mt, err)
		require.NotNil(mt, got)
		assert.Equal(mt, "p", got.Name)
	})
}

func TestStore_ListScenarios(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("list", func(mt *mtest.T) {
		sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
			Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}
		doc := bson.D{{Key: "_id", Value: "sc1"}, {Key: "data", Value: sc}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.scenarios", mtest.FirstBatch, doc))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		list, err := s.ListScenarios()
		require.NoError(mt, err)
		require.Len(mt, list, 1)
		assert.Equal(mt, "sc1", list[0].ID)
	})
}

func TestStore_DeleteScenario(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateSuccessResponse(), // DeleteOne
			mtest.CreateSuccessResponse(), // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteScenario("sc1"))
	})
}
```

Adapt mock-response ordering/cursor namespaces to the existing mtest cases' conventions if they differ. Add imports (`mtest`, `bson`) matching the existing test file.

- [ ] **Step 2: Run, verify failure** — `go test ./internal/adapters/out/mongodb/ -run 'FaultProfile|Scenario' -v` → FAIL (undefined methods).

- [ ] **Step 3: Implement** — add constants and doc shapes:

```go
const (
	colFaults    = "fault_profiles"
	colScenarios = "scenarios"
)

type faultDoc struct {
	ID   string             `bson:"_id"`
	Data domain.FaultProfile `bson:"data"`
}

type scenarioDoc struct {
	ID   string          `bson:"_id"`
	Data domain.Scenario `bson:"data"`
}
```

Add `faults *mongo.Collection`, `scenarios *mongo.Collection` to `Store`; set in `NewStoreFromClient`:

```go
	faults:    db.Collection(colFaults),
	scenarios: db.Collection(colScenarios),
```

Add compile-time checks:

```go
	_ store.FaultStore    = (*Store)(nil)
	_ store.ScenarioStore = (*Store)(nil)
```

Add the eight methods mirroring rules/sims (`Find`/`UpdateOne` upsert/`DeleteOne`, each write calling `bumpVersion()`, `Get*` returning `(nil, nil)` on empty). Example pair (replicate for scenarios with `scenarioDoc`/`s.scenarios`/`domain.Scenario`):

```go
func (s *Store) ListFaultProfiles() ([]domain.FaultProfile, error) {
	ctx := context.Background()
	cur, err := s.faults.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find fault profiles: %w", err)
	}
	defer cur.Close(ctx)
	var docs []faultDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode fault profiles: %w", err)
	}
	out := make([]domain.FaultProfile, len(docs))
	for i, d := range docs {
		out[i] = d.Data
	}
	return out, nil
}

func (s *Store) GetFaultProfile(id string) (*domain.FaultProfile, error) {
	ctx := context.Background()
	cur, err := s.faults.Find(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find fault profile %q: %w", id, err)
	}
	defer cur.Close(ctx)
	var docs []faultDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode fault profile: %w", err)
	}
	if len(docs) == 0 {
		return nil, nil
	}
	p := docs[0].Data
	return &p, nil
}

func (s *Store) SaveFaultProfile(p domain.FaultProfile) error {
	ctx := context.Background()
	filter := bson.D{{Key: "_id", Value: p.ID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: p}}}}
	if _, err := s.faults.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("mongodb: upsert fault profile %q: %w", p.ID, err)
	}
	return s.bumpVersion()
}

func (s *Store) DeleteFaultProfile(id string) error {
	ctx := context.Background()
	if _, err := s.faults.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}}); err != nil {
		return fmt.Errorf("mongodb: delete fault profile %q: %w", id, err)
	}
	return s.bumpVersion()
}
```

(Replicate the four for scenarios: `ListScenarios`/`GetScenario`/`SaveScenario`/`DeleteScenario` using `scenarioDoc`, `s.scenarios`, `domain.Scenario`.)

- [ ] **Step 4: Run tests** — `go test ./internal/adapters/out/mongodb/ -v` → PASS. Cosmos delegates, so `go test ./internal/adapters/out/cosmos/ -v` should still pass with no changes.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/mongodb/store.go internal/adapters/out/mongodb/store_test.go
git commit -m "feat(mongodb): FaultStore and ScenarioStore implementations"
```

---

### Task 4: Wiring — CLI flags + env vars

**Files:**
- Modify: `cmd/mockwave/main.go`
- Modify: `server/store.go`
- Test: `cmd/mockwave/store_factory_test.go` (only if it asserts the dynamo Config)

- [ ] **Step 1: Inspect** `cmd/mockwave/store_factory_test.go` — if it constructs/asserts a `dynamostore.Config`, note which fields it checks (you'll extend it). If it doesn't touch dynamo Config, no test change is needed; the build + existing tests cover wiring.

- [ ] **Step 2: Add flags + opts** in `cmd/mockwave/main.go`. In the `storeOpts` struct add:

```go
	DynamoFaultsTable    string
	DynamoScenariosTable string
```

Register flags next to the existing dynamo ones:

```go
	cmd.Flags().StringVar(&opts.DynamoFaultsTable, "dynamo-faults-table", "mockwave-fault-profiles", "DynamoDB table for fault profiles")
	cmd.Flags().StringVar(&opts.DynamoScenariosTable, "dynamo-scenarios-table", "mockwave-scenarios", "DynamoDB table for scenarios")
```

Thread into `buildStore`'s dynamodb case:

```go
		return dynamostore.NewStore(dynamostore.Config{
			RulesTable:     opts.DynamoRulesTable,
			SimsTable:      opts.DynamoSimsTable,
			FaultsTable:    opts.DynamoFaultsTable,
			ScenariosTable: opts.DynamoScenariosTable,
			Region:         opts.DynamoRegion,
			Endpoint:       opts.DynamoEndpoint,
		})
```

- [ ] **Step 3: Add env vars** in `server/store.go` `buildStoreFromEnv` dynamodb case:

```go
			FaultsTable:    envOr("MOCKWAVE_DYNAMO_FAULTS_TABLE", "mockwave-fault-profiles"),
			ScenariosTable: envOr("MOCKWAVE_DYNAMO_SCENARIOS_TABLE", "mockwave-scenarios"),
```

(insert into the `dynamostore.Config{...}` literal there).

- [ ] **Step 4: Build + test** — `go build ./... && go test ./cmd/mockwave/ ./server/ -count=1` → PASS. If `store_factory_test.go` asserts Config fields, extend it to expect the new defaults.

- [ ] **Step 5: Commit**

```bash
git add cmd/mockwave/main.go server/store.go cmd/mockwave/store_factory_test.go
git commit -m "feat(cli): dynamo faults/scenarios table flags and env vars"
```

---

### Task 5: DynamoDB integration test (dynamodb-local)

**Files:**
- Modify: `internal/adapters/out/dynamodb/store_integration_test.go`

This file already has `createTable`, `rulesTable`/`simsTable` constants, `dynamoEndpoint(t)` (skips when no local endpoint), and `newLocalClient(t, endpoint)`. Add a chaos round-trip test guarded the same way.

- [ ] **Step 1: Write the integration test** (append; reuse the existing helpers — read the file first for the exact constant/helper names and the build tag/skip mechanism)

```go
func TestDynamoLocal_FaultAndScenarioCRUD(t *testing.T) {
	endpoint := dynamoEndpoint(t) // skips if local dynamo not available
	client := newLocalClient(t, endpoint)
	faultsTable := "mockwave-fault-profiles-test"
	scenariosTable := "mockwave-scenarios-test"
	createTable(t, client, rulesTable) // bumpVersion target
	createTable(t, client, faultsTable)
	createTable(t, client, scenariosTable)

	store := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: rulesTable, SimsTable: simsTable,
		FaultsTable: faultsTable, ScenariosTable: scenariosTable,
	})

	// Fault profile round-trip
	p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}}
	require.NoError(t, store.SaveFaultProfile(p))
	got, err := store.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "p", got.Name)
	list, err := store.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, store.DeleteFaultProfile("fp1"))
	got, err = store.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.Nil(t, got)

	// Scenario round-trip
	sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "fp1"}}}
	require.NoError(t, store.SaveScenario(sc))
	gotSc, err := store.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, gotSc)
	require.Equal(t, "n", gotSc.Name)
	require.NoError(t, store.DeleteScenario("sc1"))
	gotSc, err = store.GetScenario("sc1")
	require.NoError(t, err)
	require.Nil(t, gotSc)
}
```

Match the existing `createTable` signature (it takes `(t, client, name)` and uses PK `id`). If the existing integration tests use a build tag (e.g. `//go:build integration`) the file already has it — no change needed.

- [ ] **Step 2: Run against local dynamo** — start it if not running (`docker run -d -p 8000:8000 amazon/dynamodb-local:latest -jar DynamoDBLocal.jar -inMemory -port 8000`), then:

Run: `go test ./internal/adapters/out/dynamodb/ -run TestDynamoLocal -v` (with the env/endpoint the existing tests expect; if it skips without local dynamo that's acceptable — note it).
Expected: PASS (or SKIP when no local dynamo).

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/out/dynamodb/store_integration_test.go
git commit -m "test(dynamodb): integration coverage for fault/scenario stores"
```

---

### Task 6: Docs

**Files:**
- Modify: `README.md`
- Modify: `docs/extending.md`

- [ ] **Step 1: README** — in the "Chaos Testing" section, remove the caveat that chaos is jsonfile-only / returns 501 on remote stores. Replace with: chaos works on all backends; DynamoDB requires two additional tables (`mockwave-fault-profiles`, `mockwave-scenarios`, PK `id`, on-demand billing) created out-of-band like the rules/simulations tables; MongoDB/Cosmos collections (`fault_profiles`, `scenarios`) auto-create on first write. Mention the `--dynamo-faults-table` / `--dynamo-scenarios-table` flags and the `MOCKWAVE_DYNAMO_FAULTS_TABLE` / `MOCKWAVE_DYNAMO_SCENARIOS_TABLE` env vars.

- [ ] **Step 2: extending.md** — update the `FaultStore`/`ScenarioStore` capability notes to state they are implemented by jsonfile, DynamoDB, and MongoDB (Cosmos via MongoDB), no longer jsonfile-only.

- [ ] **Step 3: Full suite + vet** — `go test ./... -count=1 && go vet ./...` → PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/extending.md
git commit -m "docs: chaos works on remote stores (dynamo/mongo/cosmos)"
```

---

## Notes / risks
- The version-bump on fault/scenario writes targets the rules table/collection; that table must exist (it always does for a working store). No new bump mechanism.
- DynamoDB tables are NOT auto-created — the two new tables must exist or Scan/Put fail at runtime. Documented; mirrors the existing rules/sims requirement.
- No admin-API / FaultStage changes: they already type-assert `store.FaultStore`/`store.ScenarioStore` and will exercise the remote implementations unchanged. A quick manual check after Task 4: run `--store=dynamodb` with the new tables created and confirm `GET /api/faults` returns `200 []` instead of `501`.
