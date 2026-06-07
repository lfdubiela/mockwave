# In-Memory Data + Version-Poll Reloader Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve rules and simulations from in-memory snapshots (zero store reads per request) and keep multi-pod deployments converged via a version-poll ticker (default 15s) plus immediate local reload on writes.

**Architecture:** Add an opt-in `store.VersionedStore` capability whose remote implementations bump an atomic config-version marker on every write. A `internal/reload` ticker reads the cheap marker each interval and calls `server.Rebuild()` only when it changed. `rebuild()` snapshots all simulations into a map the pipeline reads from, eliminating per-request `GetSimulation` calls.

**Tech Stack:** Go (stdlib `context`, `time`, `sync`), AWS SDK v2 DynamoDB, mongo-driver. Cosmos reuses the Mongo `Store`.

---

## Notes for the implementer

- Cosmos (`internal/adapters/out/cosmos`) delegates to `mongodb.Store` via
  `NewStoreFromClient`, so Mongo changes cover Cosmos automatically — no separate
  Cosmos edits.
- DynamoDB `GetRules` already skips the marker item (it has no `data` string
  attribute, so the existing `item["data"].(*types.AttributeValueMemberS)` check
  `continue`s). Mongo `GetRules` must explicitly exclude the marker `_id`.
- JSON store stays a plain `DataStore` (no `VersionedStore`) → reloader never
  engages for it.

---

## Task 1: `store.VersionedStore` capability interface

**Files:**
- Modify: `store/store.go`

- [ ] **Step 1: Add the interface**

Append to `store/store.go`:

```go
// VersionedStore is an optional capability. Stores that can report a monotonic
// config version enable the periodic version-poll reloader: the reloader reads
// ConfigVersion cheaply each tick and triggers a full in-memory reload only when
// the value changed. Stores that do not implement it (e.g. the JSON store) are
// never polled.
type VersionedStore interface {
	// ConfigVersion returns a monotonically increasing marker that changes on
	// every rule/simulation write. Returns 0 when no marker exists yet.
	ConfigVersion() (int64, error)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./store/`
Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add store/store.go
git commit -m "feat(store): add optional VersionedStore capability interface"
```

---

## Task 2: DynamoDB config version + bump on writes

**Files:**
- Modify: `internal/adapters/out/dynamodb/store.go`
- Test: `internal/adapters/out/dynamodb/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/out/dynamodb/store_test.go`:

```go
func TestStore_ConfigVersion_AndBumpOnWrite(t *testing.T) {
	m := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(m, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
	})

	// Each mutating call must issue exactly one UpdateItem bump on the marker.
	require.NoError(t, s.SaveRule(domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/x"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}}}))
	require.NoError(t, s.SaveSimulation(domain.Simulation{ID: "s1", Protocol: "http"}))
	require.NoError(t, s.DeleteRule("r1"))
	require.NoError(t, s.DeleteSimulation("s1"))
	assert.Equal(t, 4, m.updateCount, "expected one version bump per write")

	// ConfigVersion reads the marker number.
	m.versionValue = 7
	v, err := s.ConfigVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(7), v)
}
```

- [ ] **Step 2: Extend the mock to support UpdateItem + version GetItem**

In `internal/adapters/out/dynamodb/store_test.go`, add an `UpdateItem` method and
version fields to `mockDynamo`. Add to the struct:

```go
	updateCount  int
	versionValue int64
```

Add the method (place beside the other mock methods):

```go
func (m *mockDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateCount++
	return &dynamodb.UpdateItemOutput{}, nil
}
```

In the existing `GetItem` mock, return the version marker when queried for it. At
the top of the `GetItem` method body add:

```go
	if idAttr, ok := in.Key["id"].(*types.AttributeValueMemberS); ok && idAttr.Value == "__mockwave_config_version__" {
		if m.versionValue == 0 {
			return &dynamodb.GetItemOutput{}, nil
		}
		return &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
			"id":      &types.AttributeValueMemberS{Value: "__mockwave_config_version__"},
			"version": &types.AttributeValueMemberN{Value: strconv.FormatInt(m.versionValue, 10)},
		}}, nil
	}
```

Add `"strconv"` to the test file imports if missing.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/out/dynamodb/ -run TestStore_ConfigVersion_AndBumpOnWrite`
Expected: FAIL — `UpdateItem` not in the `DynamoClient` interface / `ConfigVersion` undefined.

- [ ] **Step 4: Implement in the store**

In `internal/adapters/out/dynamodb/store.go`:

Add `UpdateItem` to the `DynamoClient` interface (beside the existing methods):

```go
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
```

Add the marker constant near the top (after the imports):

```go
const versionItemID = "__mockwave_config_version__"
```

Add a private bump helper and the public `ConfigVersion`:

```go
// bumpVersion atomically increments the config-version marker in the rules table.
func (s *Store) bumpVersion() error {
	_, err := s.client.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName: aws.String(s.rulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: versionItemID}},
		UpdateExpression:          aws.String("ADD #v :one"),
		ExpressionAttributeNames:  map[string]string{"#v": "version"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}},
	})
	return wrapErr(err, "bump config version")
}

// ConfigVersion returns the current config-version marker (0 if absent).
func (s *Store) ConfigVersion() (int64, error) {
	out, err := s.client.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(s.rulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: versionItemID}},
	})
	if err != nil {
		return 0, wrapErr(err, "get config version")
	}
	if out.Item == nil {
		return 0, nil
	}
	n, ok := out.Item["version"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	v, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dynamodb: parse config version: %w", err)
	}
	return v, nil
}
```

Add `"strconv"` to the store imports.

Call `s.bumpVersion()` at the end of each mutating method (`SaveRule`,
`SaveSimulation`, `DeleteRule`, `DeleteSimulation`), replacing their final
`return ...` so the bump runs after a successful write. For `SaveRule`:

```go
func (s *Store) SaveRule(r domain.Rule) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal rule: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.rulesTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: r.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	if err := wrapErr(err, "put rule %q", r.ID); err != nil {
		return err
	}
	return s.bumpVersion()
}
```

Apply the same pattern to `SaveSimulation`, `DeleteRule`, `DeleteSimulation`:
keep their existing operation, capture/return its error first, then
`return s.bumpVersion()` on success.

- [ ] **Step 5: Add the compile-time assertion**

Below the existing `var _ store.DataStore = (*Store)(nil)` add:

```go
var _ store.VersionedStore = (*Store)(nil)
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/adapters/out/dynamodb/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/out/dynamodb/store.go internal/adapters/out/dynamodb/store_test.go
git commit -m "feat(dynamodb): atomic config-version marker bumped on writes"
```

---

## Task 3: MongoDB (and Cosmos) config version + bump + marker exclusion

**Files:**
- Modify: `internal/adapters/out/mongodb/store.go`
- Test: `internal/adapters/out/mongodb/store_test.go`

- [ ] **Step 1: Implement marker constant + version methods**

In `internal/adapters/out/mongodb/store.go` add near the `const (...)` block:

```go
const versionDocID = "__mockwave_config_version__"
```

Add the bump helper and `ConfigVersion`:

```go
// bumpVersion atomically increments the config-version marker in the rules collection.
func (s *Store) bumpVersion() error {
	ctx := context.Background()
	_, err := s.rules.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: versionDocID}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "version", Value: 1}}}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongodb: bump config version: %w", err)
	}
	return nil
}

// ConfigVersion returns the current config-version marker (0 if absent).
func (s *Store) ConfigVersion() (int64, error) {
	ctx := context.Background()
	var doc struct {
		Version int64 `bson:"version"`
	}
	err := s.rules.FindOne(ctx, bson.D{{Key: "_id", Value: versionDocID}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("mongodb: get config version: %w", err)
	}
	return doc.Version, nil
}
```

- [ ] **Step 2: Exclude the marker from GetRules**

In `GetRules`, change the `Find` filter from `bson.D{}` to exclude the marker:

```go
	cur, err := s.rules.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$ne", Value: versionDocID}}}})
```

- [ ] **Step 3: Call bumpVersion after each write**

In `SaveRule`, `SaveSimulation`, `DeleteRule`, `DeleteSimulation`, replace the
trailing `return nil` with `return s.bumpVersion()`. Example for `SaveRule`:

```go
	_, err := s.rules.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("mongodb: upsert rule %q: %w", r.ID, err)
	}
	return s.bumpVersion()
```

Apply identically to the other three (keep their error wrapping, swap the final
`return nil` for `return s.bumpVersion()`).

- [ ] **Step 4: Add the compile-time assertion**

Below `var _ store.DataStore = (*Store)(nil)` add:

```go
var _ store.VersionedStore = (*Store)(nil)
```

- [ ] **Step 5: Build (mtest-based tests run under their own harness)**

Run: `go build ./internal/adapters/out/mongodb/ ./internal/adapters/out/cosmos/`
Expected: success. Cosmos compiles against the updated Mongo `Store` and inherits
`ConfigVersion`.

- [ ] **Step 6: Run package tests**

Run: `go test ./internal/adapters/out/mongodb/ ./internal/adapters/out/cosmos/`
Expected: `ok` (or cached). If mtest mocks a write, ensure the test expects the
extra `bumpVersion` update; if a pre-existing test now fails because it counts
exact mongo calls, update that test to account for the marker `UpdateOne`.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/out/mongodb/store.go internal/adapters/out/mongodb/store_test.go
git commit -m "feat(mongodb): config-version marker bumped on writes; exclude from GetRules"
```

---

## Task 4: In-memory simulation snapshot in the pipeline

**Files:**
- Modify: `internal/domain/simulation/loader.go`
- Modify: `server/server.go` (`rebuild`)
- Test: `internal/domain/simulation/loader_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/domain/simulation/loader_test.go`:

```go
func TestSimulationStage_ReadsFromSnapshot_NoStoreCalls(t *testing.T) {
	sims := map[string]domain.Simulation{
		"s1": {ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 201}},
	}
	stage := simulation.NewSimulationStage(sims)

	pctx := &pipeline.PipelineContext{SimulationID: "s1"}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 201, pctx.Response.Status)
}

func TestSimulationStage_MissingSimulation(t *testing.T) {
	stage := simulation.NewSimulationStage(map[string]domain.Simulation{})
	pctx := &pipeline.PipelineContext{SimulationID: "nope"}
	err := stage.Execute(context.Background(), pctx)
	require.Error(t, err)
}
```

(Adjust the existing `loader_test.go` tests that construct `NewSimulationStage`
with a store — they must now pass a `map[string]domain.Simulation`. Convert each
store fixture to the equivalent map.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/simulation/ -run TestSimulationStage_ReadsFromSnapshot`
Expected: FAIL — `NewSimulationStage` still wants a `store.DataStore`.

- [ ] **Step 3: Change SimulationStage to hold a snapshot map**

In `internal/domain/simulation/loader.go`, replace the store dependency:

```go
type SimulationStage struct {
	sims map[string]domain.Simulation
}

func NewSimulationStage(sims map[string]domain.Simulation) *SimulationStage {
	return &SimulationStage{sims: sims}
}
```

In `Execute`, replace the `s.store.GetSimulation(pctx.SimulationID)` lookup with:

```go
	sim, ok := s.sims[pctx.SimulationID]
	if !ok {
		return fmt.Errorf("simulation: not found: %q", pctx.SimulationID)
	}
```

Then use `sim` (value, not pointer) for the rest of the method — replace the
former `sim.Protocol`, `sim.Response...`, `sim.SOAPEnvelope`, etc. references
(they work the same on the value). Remove the now-unused `store` import and the
`s.store` field. Add `"github.com/mockwave/mockwave/domain"` to imports if not
present.

- [ ] **Step 4: Update server.rebuild to build + pass the snapshot**

In `server/server.go` `rebuild()`, after loading rules, load simulations and
build the map; pass it to the stage and reuse it in the script closure:

```go
func (s *Server) rebuild() error {
	rules, err := s.cfg.Store.GetRules()
	if err != nil {
		return fmt.Errorf("server: load rules: %w", err)
	}
	simList, err := s.cfg.Store.ListSimulations()
	if err != nil {
		return fmt.Errorf("server: load simulations: %w", err)
	}
	simMap := make(map[string]domain.Simulation, len(simList))
	for _, sim := range simList {
		simMap[sim.ID] = sim
	}

	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(simMap)
	scriptStage := pipeline.NewScriptStage(s.engine, func(pctx *pipeline.PipelineContext) string {
		if pctx.SimulationID == "" {
			return ""
		}
		return simMap[pctx.SimulationID].Script
	})
	fwdStage := httprest.NewForwardStage(nil)
	p := pipeline.New(matchStage, routeStage, simStage, scriptStage, fwdStage)
	s.mu.Lock()
	s.pipeline = p
	s.mu.Unlock()
	return nil
}
```

Add `"github.com/mockwave/mockwave/domain"` to `server/server.go` imports if not
already present.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/domain/simulation/ ./server/`
Expected: `ok` for both.

- [ ] **Step 6: Build the whole module**

Run: `go build ./...`
Expected: success (catches any other `NewSimulationStage` callers).

- [ ] **Step 7: Commit**

```bash
git add internal/domain/simulation/loader.go internal/domain/simulation/loader_test.go server/server.go
git commit -m "feat(pipeline): serve simulations from in-memory snapshot (zero per-request store reads)"
```

---

## Task 5: `internal/reload` ticker package

**Files:**
- Create: `internal/reload/reload.go`
- Create: `internal/reload/reload_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/reload/reload_test.go`:

```go
package reload_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/reload"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

type fakeVersioned struct {
	mu      sync.Mutex
	version int64
	err     error
}

func (f *fakeVersioned) ConfigVersion() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.err
}
func (f *fakeVersioned) set(v int64) { f.mu.Lock(); f.version = v; f.mu.Unlock() }
func (f *fakeVersioned) fail(e error) { f.mu.Lock(); f.err = e; f.mu.Unlock() }

func TestReloader_ReloadsOnFirstTickAndOnChangeOnly(t *testing.T) {
	fv := &fakeVersioned{version: 1}
	var mu sync.Mutex
	calls := 0
	r := reload.New(fv, 10*time.Millisecond, func() error {
		mu.Lock(); calls++; mu.Unlock(); return nil
	}, observability.NoopLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	time.Sleep(35 * time.Millisecond) // a few ticks, version unchanged
	mu.Lock(); first := calls; mu.Unlock()
	assert.Equal(t, 1, first, "reload only on first tick when version is stable")

	fv.set(2)
	time.Sleep(25 * time.Millisecond)
	mu.Lock(); second := calls; mu.Unlock()
	cancel()
	assert.Equal(t, 2, second, "reload again after version changed")
}

func TestReloader_SkipsTickOnVersionError(t *testing.T) {
	fv := &fakeVersioned{version: 1, err: errors.New("boom")}
	var mu sync.Mutex
	calls := 0
	r := reload.New(fv, 10*time.Millisecond, func() error {
		mu.Lock(); calls++; mu.Unlock(); return nil
	}, observability.NoopLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	time.Sleep(35 * time.Millisecond)
	cancel()
	mu.Lock(); defer mu.Unlock()
	assert.Equal(t, 0, calls, "no reload while ConfigVersion errors")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reload/`
Expected: FAIL — package `internal/reload` does not exist.

- [ ] **Step 3: Implement the reloader**

Create `internal/reload/reload.go`:

```go
// Package reload polls a VersionedStore and triggers a reload when the store's
// config version changes, keeping in-memory snapshots fresh across pods.
package reload

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/observability"
	"github.com/mockwave/mockwave/store"
)

// Reloader periodically checks a store's config version and invokes reload when
// it changes (and once on the first tick).
type Reloader struct {
	store    store.VersionedStore
	interval time.Duration
	reload   func() error
	log      observability.Logger
}

// New creates a Reloader. reload is typically server.Rebuild.
func New(s store.VersionedStore, interval time.Duration, reload func() error, log observability.Logger) *Reloader {
	return &Reloader{store: s, interval: interval, reload: reload, log: log}
}

// Run blocks until ctx is cancelled, polling every interval.
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
			if err != nil {
				r.log.Warn("reload: config version read failed", "error", err)
				continue
			}
			if v == last {
				continue
			}
			if err := r.reload(); err != nil {
				r.log.Error("reload: rebuild failed", "error", err)
				continue // do not advance last; retry next tick
			}
			last = v
		}
	}
}
```

(If `observability.Logger`'s methods differ from `Warn(msg, kv...)` / `Error(msg,
kv...)`, match the actual signatures — check `observability` package.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/reload/ -race`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/reload/
git commit -m "feat(reload): version-poll ticker that reloads only on config change"
```

---

## Task 6: Wire the reloader into the server

**Files:**
- Modify: `server/server.go` (`Config`, `Server`, `New`)
- Modify: `server/admin.go` (`Shutdown`)
- Test: `server/server_test.go`

- [ ] **Step 1: Add config field, helper, and server field**

In `server/server.go`, add to `Config`:

```go
	// ReloadInterval controls the version-poll reloader cadence for remote
	// (VersionedStore) backends. 0 resolves to 15s. Ignored for stores that do
	// not implement store.VersionedStore (e.g. the JSON store).
	ReloadInterval time.Duration
```

Add a helper:

```go
func (c Config) reloadInterval() time.Duration {
	if c.ReloadInterval <= 0 {
		return 15 * time.Second
	}
	return c.ReloadInterval
}
```

Add a field to `Server`:

```go
	reloadCancel context.CancelFunc
```

Add `"time"` to imports if missing.

- [ ] **Step 2: Start the reloader in New (after rebuild succeeds)**

In `New`, after the `if err := s.rebuild(); err != nil { ... }` block and before
the admin start, add:

```go
	if vs, ok := s.cfg.Store.(store.VersionedStore); ok {
		rl := reload.New(vs, s.cfg.reloadInterval(), s.Rebuild, s.cfg.Logger)
		rctx, rcancel := context.WithCancel(context.Background())
		s.reloadCancel = rcancel
		go rl.Run(rctx)
	}
```

Add imports `"github.com/mockwave/mockwave/internal/reload"` and
`"github.com/mockwave/mockwave/store"` to `server/server.go` (store may already be
imported).

- [ ] **Step 3: Stop the reloader on Shutdown**

In `server/admin.go` `Shutdown`, cancel the reloader alongside the broker:

```go
func (s *Server) Shutdown(ctx context.Context) error {
	if s.brokerCancel != nil {
		s.brokerCancel()
	}
	if s.reloadCancel != nil {
		s.reloadCancel()
	}
	if s.adminSrv != nil {
		return s.adminSrv.Shutdown(ctx)
	}
	return nil
}
```

- [ ] **Step 4: Write a server test for reloader engagement**

Append to `server/server_test.go`:

```go
type versionedStub struct {
	*stubStore
	version int64
}

func (v *versionedStub) ConfigVersion() (int64, error) { return v.version, nil }

func TestServer_ReloaderEngagesForVersionedStore(t *testing.T) {
	vs := &versionedStub{stubStore: newStubStore()}
	srv, err := server.New(server.Config{Store: vs, ReloadInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer srv.Shutdown(context.Background())
	// No panic / clean start is the assertion; the reloader goroutine is running.
	require.NotNil(t, srv)
}
```

(Use the existing `newStubStore()` helper in `server_test.go`. If `stubStore`'s
methods are value-receiver and embedding causes issues, give `versionedStub` its
own forwarding methods or wrap the stub explicitly. Ensure `time` and `context`
are imported in the test file.)

- [ ] **Step 5: Run tests**

Run: `go test ./server/ -race`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add server/server.go server/admin.go server/server_test.go
git commit -m "feat(server): start version-poll reloader for VersionedStore backends"
```

---

## Task 7: CLI flag + env for reload interval

**Files:**
- Modify: `cmd/mockwave/main.go`
- Modify: `server/store.go` (env parse)

- [ ] **Step 1: Add the CLI flag and pass it through**

In `cmd/mockwave/main.go`, add a variable beside the others:

```go
		reloadInterval time.Duration
```

Register the flag (beside the other `cmd.Flags()` calls):

```go
	cmd.Flags().DurationVar(&reloadInterval, "reload-interval", 15*time.Second, "version-poll reload interval for remote stores")
```

Pass it into the server config in the `RunE` `server.New(...)` call:

```go
			srv, err := server.New(server.Config{
				MockPort:       mockPort,
				AdminPort:      adminPort,
				Store:          s,
				ReloadInterval: reloadInterval,
			})
```

Add `"time"` to `cmd/mockwave/main.go` imports if missing.

- [ ] **Step 2: Add env override for the env-driven path**

In `server/store.go` (where `MOCKWAVE_*` vars are read), add a helper and apply it
so embedders using env-config get the override. Add:

```go
// reloadIntervalFromEnv returns the parsed MOCKWAVE_RELOAD_INTERVAL, or 0 when
// unset/invalid (0 lets Config.reloadInterval fall back to the 15s default).
func reloadIntervalFromEnv() time.Duration {
	if v := os.Getenv("MOCKWAVE_RELOAD_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0
}
```

Add `"time"` to `server/store.go` imports if missing. (This helper is consumed in
Task 6's New if you choose to read it there; otherwise it documents the env knob.
To wire it: in `New`, when `cfg.ReloadInterval == 0`, set
`cfg.ReloadInterval = reloadIntervalFromEnv()` before starting the reloader.)

In `server/server.go` `New`, just before the reloader block, add:

```go
	if cfg.ReloadInterval == 0 {
		cfg.ReloadInterval = reloadIntervalFromEnv()
	}
```

(Place this after `cfg` is in scope; note `New` copies `cfg` by value, so update
the local `cfg` then `s.cfg` already references it — set `s.cfg.ReloadInterval`
too if `s` is already built. Simplest: compute the interval into a local and pass
to `reload.New` directly: `interval := cfg.reloadInterval()` after the env
fallback, then `reload.New(vs, interval, ...)`.)

- [ ] **Step 3: Build + run help to confirm the flag**

Run: `go build ./cmd/mockwave/ && ./mockwave start --help | grep reload-interval`
Expected: shows `--reload-interval` with default `15s`. Remove the built binary:
`rm -f mockwave`.

- [ ] **Step 4: Commit**

```bash
git add cmd/mockwave/main.go server/store.go server/server.go
git commit -m "feat(cli): --reload-interval flag and MOCKWAVE_RELOAD_INTERVAL env"
```

---

## Task 8: Integration test — two instances converge via version poll

**Files:**
- Modify: `internal/adapters/out/dynamodb/store_integration_test.go`

- [ ] **Step 1: Add the test**

Append to `internal/adapters/out/dynamodb/store_integration_test.go`:

```go
func TestDynamoLocal_VersionPropagates(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)

	a := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: rulesTable, SimsTable: simsTable})
	b := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: rulesTable, SimsTable: simsTable})

	v0, err := b.ConfigVersion()
	require.NoError(t, err)

	require.NoError(t, a.SaveSimulation(domain.Simulation{
		ID: "ok", Protocol: "http", Response: domain.HTTPResponse{Status: 200},
	}))

	v1, err := b.ConfigVersion()
	require.NoError(t, err)
	require.Greater(t, v1, v0, "write through instance A must raise the version seen by instance B")
}
```

- [ ] **Step 2: Run it**

Run: `DYNAMO_TEST_ENDPOINT=http://localhost:8000 go test -tags integration -run TestDynamoLocal_VersionPropagates ./internal/adapters/out/dynamodb/`
(Start DynamoDB Local first, e.g. via `make itest-up` or the standalone jar.)
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/out/dynamodb/store_integration_test.go
git commit -m "test(dynamodb): version marker propagates across store instances"
```

---

## Task 9: Full verification

- [ ] **Step 1: Vet + unit suite**

Run: `go vet ./... && go test ./...`
Expected: vet silent; all packages `ok`.

- [ ] **Step 2: Coverage gate**

Run: `make coverage`
Expected: total ≥ 80%.

- [ ] **Step 3: Integration suite (deps via docker-compose)**

Run: `make test-integration`
Expected: all `ok` (includes the new version-propagation test).
