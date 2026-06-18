# AWS Event Interception — Phase 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist AWS event interception across restarts and remote stores: implement `EventRuleStore` on the DynamoDB / MongoDB / Cosmos backends, and wire event-capture write-behind persistence + startup hydration (reusing the matched-capture machinery), with native TTL expiry.

**Architecture:** Event rules get `GetEventRules`/`SaveEventRule`/`DeleteEventRule` on each cloud backend, mirroring the existing rule CRUD (a `data`-blob item per backend, version-bumped so admin edits hot-reload). Event captures reuse the existing `matched.Buffer` + `matched.Syncer` + `store.MatchedStore` — they share the matched table, distinguished by the `aws-*` protocol prefix; hydration filters by that prefix so HTTP captures and event captures don't leak into each other's in-memory view.

**Tech Stack:** Go 1.26, aws-sdk-go-v2 dynamodb, mongo-driver. Tests: `make test` (unit, mock clients) + `make test-integration` (`-tags integration`, DynamoDB Local via docker). Coverage gate ≥80%.

**Spec/index:** [`2026-06-17-aws-event-interception-index.md`](2026-06-17-aws-event-interception-index.md) Phase 4 row.

---

## Context an implementer needs (verified current code)

- `store.EventRuleStore` interface exists (`GetEventRules() ([]domain.EventRule, error)`, `SaveEventRule(domain.EventRule) error`, `DeleteEventRule(id string) error`). Only `jsonfile` implements it today.
- `server.rebuild()` already loads event rules via `if ers, ok := s.cfg.Store.(store.EventRuleStore); ok { ers.GetEventRules() }` and **returns the error** if `GetEventRules` fails. So a cloud backend's `GetEventRules` MUST NOT error when the event-rules table/collection is absent (existing deployments that don't use events would otherwise fail to start). DynamoDB must treat `ResourceNotFoundException` as "no event rules" (return `nil, nil`); MongoDB `Find` on a missing collection already returns an empty cursor (no error).
- Event capture is currently in-memory only (`server.New()`: `if ec := resolveEventConfig(...); ec.Enabled { s.eventCaptureBuf = matched.NewBuffer(...) }` — no syncer/hydrate).
- HTTP matched capture (the template) does: resolve sink via `matchedSink`, `ms.ListMatched(ctx, "", {Limit})` → `Hydrate`, start `matched.NewSyncer(...).Run`, else memory-only `runMatchedSweep`. (`server/server.go` ~lines 163–179.)
- `matched.Request` already has `Identity`/`Forwarded`/`ForwardTarget` — they marshal into the store's JSON `data` blob, so no backend schema change for those.
- The matched store keys captures by `rule_id`. Event rule IDs and HTTP rule IDs are separate spaces; per-rule queries are homogeneous. Only the bulk hydration `ListMatched(ctx, "", ...)` mixes them → fixed by the `aws-*` filter.
- DynamoDB rule CRUD pattern (`store.go`): `Scan` the table, extract `data` (string) attr → `json.Unmarshal`; `PutItem` `{id, data}`; `DeleteItem` by `id`; each write calls `bumpVersion()`. Helper `wrapErr(err, "fmt %q", id)` wraps errors. Mongo pattern: `db.Collection("event_rules")`, doc `{_id, data}`, `UpdateOne(... $set ..., SetUpsert(true))`, `DeleteOne`, `bumpVersion()`.
- Integration tests: build tag `//go:build integration`, gated by `DYNAMO_TEST_ENDPOINT` (test calls a helper that `t.Skip`s when unset). Run via `make test-integration` (docker compose brings up DynamoDB Local at :8000). The matched e2e at `internal/adapters/out/dynamodb/e2e_dynamo_matched_test.go` is the template (create tables → SaveRule → server.New → drive → assert buffer → poll store → restart → assert hydration).

## File Structure

- `internal/adapters/out/dynamodb/store.go` — MODIFY: `Config.EventRulesTable`, `Store.eventRulesTable`, constructor wiring.
- `internal/adapters/out/dynamodb/event_rules.go` — CREATE: `GetEventRules`/`SaveEventRule`/`DeleteEventRule` + assertion.
- `internal/adapters/out/mongodb/store.go` — MODIFY: `colEventRules`, `Store.eventRules`, constructor wiring.
- `internal/adapters/out/mongodb/event_rules.go` — CREATE: the three methods + `eventRuleDoc` + assertion.
- `internal/adapters/out/cosmos/event_rules.go` — CREATE: compile-time assertion only (delegates to mongodb.Store).
- `cmd/mockwave/main.go`, `server/store.go` — MODIFY: `EventRulesTable` flag + env, passed to dynamo Config.
- `server/events.go` — MODIFY: `EventConfig.Store`, `eventSink`, `awsCaptures`/`nonAWSCaptures` filters.
- `server/server.go` — MODIFY: event syncer/hydrate wiring; matched hydration filter; `runEventSweep`; Close flush; struct fields.
- `internal/adapters/out/dynamodb/e2e_dynamo_events_test.go` — CREATE: integration e2e (event-rule CRUD + capture persist/hydrate).
- docs.

---

## Task 1: DynamoDB EventRuleStore

**Files:**
- Modify: `internal/adapters/out/dynamodb/store.go`
- Create: `internal/adapters/out/dynamodb/event_rules.go`
- Test: `internal/adapters/out/dynamodb/event_rules_test.go`

- [ ] **Step 1: Wire the table into Config + Store.** In `internal/adapters/out/dynamodb/store.go`:
  - Add to the `Config` struct: `EventRulesTable string // DynamoDB table for AWS event rules (PK: "id")`
  - Add to the `Store` struct: `eventRulesTable string`
  - In `NewStoreFromClient`, add to the returned `&Store{...}`: `eventRulesTable: cfg.EventRulesTable,`
  - Add `store.EventRuleStore` to the compile-time assertion block (the `var (...)` list).

- [ ] **Step 2: Write the failing test.** First READ `internal/adapters/out/dynamodb/store_test.go` to see the existing mock `DynamoClient` (the fake with `ScanFunc`/`PutItemFunc`/`DeleteItemFunc`-style fields, or however it's structured) and the `GetRules`/`SaveRule` tests. Mirror that exact harness. Create `internal/adapters/out/dynamodb/event_rules_test.go` covering:
  - `GetEventRules` returns the unmarshaled rules from a Scan that yields `data`-blob items.
  - `GetEventRules` returns `(nil, nil)` (NOT an error) when Scan returns a `*types.ResourceNotFoundException` (missing table).
  - `SaveEventRule` issues a `PutItem` to `eventRulesTable` with `{id, data}` and bumps the version.
  - `DeleteEventRule` issues a `DeleteItem` by `id`.

  Use the same mock-client construction the existing tests use; assert on the captured `TableName`/`Item`/`Key`. (The exact mock type is established in store_test.go — reuse it; do not invent a new mock.)

- [ ] **Step 3: Run the test to confirm it fails.**

Run: `go test ./internal/adapters/out/dynamodb/ -run EventRule -v`
Expected: FAIL — methods undefined.

- [ ] **Step 4: Implement.** Create `internal/adapters/out/dynamodb/event_rules.go`:

```go
package dynamostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
)

// compile-time EventRuleStore check.
var _ store.EventRuleStore = (*Store)(nil)

// GetEventRules returns all AWS event rules. A missing table is treated as "no
// event rules configured" (nil, nil) so deployments that don't use event
// interception start cleanly.
func (s *Store) GetEventRules() ([]domain.EventRule, error) {
	out, err := s.client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(s.eventRulesTable),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("dynamodb: scan event rules: %w", err)
	}
	rules := make([]domain.EventRule, 0, len(out.Items))
	for _, item := range out.Items {
		dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var r domain.EventRule
		if err := json.Unmarshal([]byte(dataAttr.Value), &r); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal event rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// SaveEventRule upserts an event rule by id and bumps the config version.
func (s *Store) SaveEventRule(r domain.EventRule) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal event rule: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.eventRulesTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: r.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	if err := wrapErr(err, "put event rule %q", r.ID); err != nil {
		return err
	}
	return s.bumpVersion()
}

// DeleteEventRule removes an event rule by id and bumps the config version.
func (s *Store) DeleteEventRule(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.eventRulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err := wrapErr(err, "delete event rule %q", id); err != nil {
		return err
	}
	return s.bumpVersion()
}
```

> If `wrapErr` / `bumpVersion` have different names in this package, use the actual ones (confirm by reading `store.go`'s `SaveRule`/`DeleteRule`).

- [ ] **Step 5: Run tests + build.**

Run: `go test ./internal/adapters/out/dynamodb/ -v` then `go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/adapters/out/dynamodb/store.go internal/adapters/out/dynamodb/event_rules.go internal/adapters/out/dynamodb/event_rules_test.go
git commit -m "feat(events): DynamoDB EventRuleStore (tolerates missing table)"
```
No Co-Authored-By footer.

---

## Task 2: MongoDB + Cosmos EventRuleStore

**Files:**
- Modify: `internal/adapters/out/mongodb/store.go`
- Create: `internal/adapters/out/mongodb/event_rules.go`
- Create: `internal/adapters/out/cosmos/event_rules.go`
- Test: `internal/adapters/out/mongodb/event_rules_test.go`

- [ ] **Step 1: Wire the collection.** In `internal/adapters/out/mongodb/store.go`:
  - Add constant: `colEventRules = "event_rules"` (in the existing `const (...)` block).
  - Add to the `Store` struct: `eventRules *mongo.Collection`
  - In `NewStoreFromClient`, add: `eventRules: db.Collection(colEventRules),`
  - Add `store.EventRuleStore` to the compile-time assertion `var (...)` list.

- [ ] **Step 2: Write the failing test.** READ `internal/adapters/out/mongodb/store_test.go` for the existing `mtest` harness + the `GetRules`/`SaveRule` tests; mirror it. Create `internal/adapters/out/mongodb/event_rules_test.go` covering: `GetEventRules` decodes `{_id, data}` docs; `SaveEventRule` upserts; `DeleteEventRule` deletes by `_id`. Use the same `mtest.New(...)` mock-response pattern the existing tests use.

- [ ] **Step 3: Run to confirm fail.**

Run: `go test ./internal/adapters/out/mongodb/ -run EventRule -v`
Expected: FAIL — methods undefined.

- [ ] **Step 4: Implement.** Create `internal/adapters/out/mongodb/event_rules.go`:

```go
package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
)

// compile-time EventRuleStore check.
var _ store.EventRuleStore = (*Store)(nil)

type eventRuleDoc struct {
	ID   string           `bson:"_id"`
	Data domain.EventRule `bson:"data"`
}

// GetEventRules returns all AWS event rules. A missing collection yields an
// empty cursor (no error), so deployments without event rules start cleanly.
func (s *Store) GetEventRules() ([]domain.EventRule, error) {
	ctx := context.Background()
	cur, err := s.eventRules.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find event rules: %w", err)
	}
	defer cur.Close(ctx)
	var docs []eventRuleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode event rules: %w", err)
	}
	rules := make([]domain.EventRule, len(docs))
	for i, d := range docs {
		rules[i] = d.Data
	}
	return rules, nil
}

// SaveEventRule upserts an event rule by id and bumps the config version.
func (s *Store) SaveEventRule(r domain.EventRule) error {
	ctx := context.Background()
	filter := bson.D{{Key: "_id", Value: r.ID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: r}}}}
	if _, err := s.eventRules.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("mongodb: upsert event rule %q: %w", r.ID, err)
	}
	return s.bumpVersion()
}

// DeleteEventRule removes an event rule by id and bumps the config version.
func (s *Store) DeleteEventRule(id string) error {
	ctx := context.Background()
	if _, err := s.eventRules.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}}); err != nil {
		return fmt.Errorf("mongodb: delete event rule %q: %w", id, err)
	}
	return s.bumpVersion()
}
```

> Confirm `bumpVersion` exists on the mongo `Store` (it does — `SaveRule` calls it). Match the actual import paths used elsewhere in the package.

- [ ] **Step 5: Cosmos assertion.** Create `internal/adapters/out/cosmos/event_rules.go`:

```go
package cosmos

import (
	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/store"
)

// compile-time assertion: cosmos's Store (mongodb.Store) satisfies EventRuleStore.
var _ store.EventRuleStore = (*mongodb.Store)(nil)
```

- [ ] **Step 6: Run tests + build.**

Run: `go test ./internal/adapters/out/mongodb/ ./internal/adapters/out/cosmos/ -v` then `go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add internal/adapters/out/mongodb/store.go internal/adapters/out/mongodb/event_rules.go internal/adapters/out/mongodb/event_rules_test.go internal/adapters/out/cosmos/event_rules.go
git commit -m "feat(events): MongoDB + Cosmos EventRuleStore"
```
No Co-Authored-By footer.

---

## Task 3: Store factory wiring (event-rules table flag/env)

**Files:**
- Modify: `cmd/mockwave/main.go`
- Modify: `server/store.go`

- [ ] **Step 1: CLI flag + factory.** In `cmd/mockwave/main.go`:
  - Add to the `storeOpts` struct (in the DynamoDB group): `DynamoEventRulesTable string`
  - In `buildStore`'s `case "dynamodb":`, add `EventRulesTable: opts.DynamoEventRulesTable,` to the `dynamostore.Config{...}`.
  - Register the flag next to the other dynamo flags: `cmd.Flags().StringVar(&opts.DynamoEventRulesTable, "dynamo-event-rules-table", "mockwave-event-rules", "DynamoDB table for AWS event rules")`

- [ ] **Step 2: Env factory.** In `server/store.go` `buildStoreFromEnv`'s `case "dynamodb":`, add to the `dynamostore.Config{...}`: `EventRulesTable: envOr("MOCKWAVE_DYNAMO_EVENT_RULES_TABLE", "mockwave-event-rules"),`

- [ ] **Step 3: Verify build + help.**

Run: `go build ./...` then `go run ./cmd/mockwave start --help 2>&1 | grep event-rules-table`
Expected: build clean; the new flag appears with its default.

- [ ] **Step 4: Commit.**

```bash
git add cmd/mockwave/main.go server/store.go
git commit -m "feat(events): wire dynamo event-rules table flag + env"
```
No Co-Authored-By footer.

---

## Task 4: Event-capture persistence + hydration

**Files:**
- Modify: `server/events.go`
- Modify: `server/server.go`
- Test: `server/events_test.go`

- [ ] **Step 1: Write the failing test.** Append to `server/events_test.go` (unit-level: filters + a persisted-then-hydrated round trip with an in-memory fake sink). First add a fake sink near the top of the test file (a struct implementing `store.MatchedStore` backed by a slice). Then:

```go
func TestAWSCaptureFilters(t *testing.T) {
	items := []matched.Request{
		{ID: "1", Protocol: "aws-sns"},
		{ID: "2", Protocol: "http"},
		{ID: "3", Protocol: "aws-sqs"},
	}
	aws := awsCaptures(items)
	if len(aws) != 2 || aws[0].Protocol != "aws-sns" || aws[1].Protocol != "aws-sqs" {
		t.Fatalf("awsCaptures = %+v", aws)
	}
	non := nonAWSCaptures(items)
	if len(non) != 1 || non[0].Protocol != "http" {
		t.Fatalf("nonAWSCaptures = %+v", non)
	}
}
```

Run: `go test ./server/ -run TestAWSCaptureFilters -v` → FAIL (undefined).

- [ ] **Step 2: Implement the filters + sink helper + Store field.** In `server/events.go`:
  - Add to the `EventConfig` struct: `Store store.MatchedStore // BYO override; nil → derived from backend` (matches `MatchedConfig.Store`).
  - Add imports `"strings"`, `"github.com/mockwave/mockwave/internal/matched"`, `"github.com/mockwave/mockwave/store"` (if not present).
  - Add:

```go
// eventSink picks the MatchedStore for event-capture persistence: explicit
// override, else the backend when it implements MatchedStore, else nil.
func eventSink(cfg EventConfig, backend store.DataStore) matched.Sink {
	if cfg.Store != nil {
		return cfg.Store
	}
	if ms, ok := backend.(store.MatchedStore); ok {
		return ms
	}
	return nil
}

// awsCaptures keeps only aws-* protocol captures (intercepted events). Event
// captures share the matched table with HTTP captures; the protocol prefix
// separates the two on hydration.
func awsCaptures(items []matched.Request) []matched.Request {
	out := make([]matched.Request, 0, len(items))
	for _, r := range items {
		if strings.HasPrefix(r.Protocol, "aws-") {
			out = append(out, r)
		}
	}
	return out
}

// nonAWSCaptures keeps everything except aws-* (HTTP/GraphQL/SOAP/gRPC).
func nonAWSCaptures(items []matched.Request) []matched.Request {
	out := make([]matched.Request, 0, len(items))
	for _, r := range items {
		if !strings.HasPrefix(r.Protocol, "aws-") {
			out = append(out, r)
		}
	}
	return out
}
```

Run: `go test ./server/ -run TestAWSCaptureFilters -v` → PASS.

- [ ] **Step 3: Add Server fields + event persistence wiring.** In `server/server.go`:
  - Add to the `Server` struct, near the event fields: `eventSyncer *matched.Syncer` and `eventSweep context.CancelFunc`.
  - Replace the current event-capture init block in `New()` (the `if ec := resolveEventConfig(cfg.Event); ec.Enabled { ... }`) with:

```go
	if ec := resolveEventConfig(cfg.Event); ec.Enabled {
		s.cfg.Event = ec
		s.eventCaptureBuf = matched.NewBuffer(ec.BufferSize)
		if sink := eventSink(ec, s.cfg.Store); sink != nil {
			if ms, ok := sink.(store.MatchedStore); ok {
				if page, err := ms.ListMatched(context.Background(), "", store.MatchedQuery{Limit: ec.BufferSize}); err == nil {
					s.eventCaptureBuf.Hydrate(awsCaptures(page.Items))
				}
			}
			s.eventSyncer = matched.NewSyncer(s.eventCaptureBuf, sink, ec.SyncInterval)
			go s.eventSyncer.Run(context.Background())
		} else {
			sctx, scancel := context.WithCancel(context.Background())
			s.eventSweep = scancel
			go s.runEventSweep(sctx, ec.SyncInterval)
		}
	}
```

  - In the HTTP matched init block (a few lines above), change the hydration line `s.matchedBuf.Hydrate(page.Items)` to `s.matchedBuf.Hydrate(nonAWSCaptures(page.Items))` so HTTP matched does not absorb event captures from a shared store.

- [ ] **Step 4: Add `runEventSweep` + Close handling.** READ the existing `runMatchedSweep` method in `server/server.go` and the matched-syncer teardown in `Shutdown`/`Close`. Then:
  - Add `runEventSweep` mirroring `runMatchedSweep` but operating on `s.eventCaptureBuf` (ticker → `s.eventCaptureBuf.SweepExpired()` until ctx done). If `runMatchedSweep` is written generically (takes a `*matched.Buffer`), reuse it instead of adding a new method.
  - In `Shutdown`/`Close`, wherever the matched syncer is flushed/closed and `matchedSweep` is cancelled, mirror for the event syncer/sweep: `if s.eventSyncer != nil { _ = s.eventSyncer.Close() }` and `if s.eventSweep != nil { s.eventSweep() }`. (Match the exact pattern used for `matchedSyncer`/`matchedSweep`.)

- [ ] **Step 5: Verify build + full suite.**

Run: `go build ./...` then `go test ./... -race`
Expected: PASS. Non-AWS matched capture is unchanged except it now excludes aws-* on hydration; event capture now persists + hydrates when the backend implements MatchedStore.

- [ ] **Step 6: Commit.**

```bash
git add server/events.go server/server.go server/events_test.go
git commit -m "feat(events): event-capture write-behind persistence + hydration"
```
No Co-Authored-By footer.

---

## Task 5: DynamoDB integration e2e (persist + hydrate + event-rule CRUD)

**Files:**
- Create: `internal/adapters/out/dynamodb/e2e_dynamo_events_test.go`

This test is `//go:build integration` and only runs under `make test-integration` (DynamoDB Local). It must compile in normal builds and `t.Skip` when `DYNAMO_TEST_ENDPOINT` is unset.

- [ ] **Step 1: Write the test.** READ `internal/adapters/out/dynamodb/e2e_dynamo_matched_test.go` for the exact helpers (`dynamoEndpoint(t)`, `newLocalClient(t, endpoint)`, `createTable`, `createMatchedTable`, `headerCI`) and the build tag / package name (`dynamostore_test`). Mirror them. Create `internal/adapters/out/dynamodb/e2e_dynamo_events_test.go`:

```go
//go:build integration

package dynamostore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mockwave/mockwave/domain"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
)

func TestE2E_Dynamo_EventCaptureAndRules(t *testing.T) {
	endpoint := dynamoEndpoint(t) // skips when DYNAMO_TEST_ENDPOINT unset
	client := newLocalClient(t, endpoint)

	const (
		rulesTable      = "e2e-ev-rules"
		simsTable       = "e2e-ev-sims"
		faultsTable     = "e2e-ev-faults"
		scenariosTable  = "e2e-ev-scenarios"
		matchedTable    = "e2e-ev-matched"
		eventRulesTable = "e2e-ev-event-rules"
	)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)
	createTable(t, client, faultsTable)
	createTable(t, client, scenariosTable)
	createTable(t, client, eventRulesTable)
	createMatchedTable(t, client, matchedTable)

	dynStore := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable:      rulesTable,
		SimsTable:       simsTable,
		FaultsTable:     faultsTable,
		ScenariosTable:  scenariosTable,
		MatchedTable:    matchedTable,
		EventRulesTable: eventRulesTable,
	})

	// Event rule persisted in Dynamo (proves EventRuleStore round-trips).
	require.NoError(t, dynStore.SaveEventRule(domain.EventRule{
		ID:    "orders",
		Match: domain.EventMatch{Service: domain.EventServiceSNS},
	}))
	got, err := dynStore.GetEventRules()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "orders", got[0].ID)

	const syncInterval = 50 * time.Millisecond
	srv, err := server.New(server.Config{
		Store: dynStore,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: syncInterval},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	t.Cleanup(mock.Close)

	// Real SNS SDK publishes through Mockwave (the rule is loaded from Dynamo).
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })
	_, err = snsClient.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
		Message:  aws.String(`{"id":1}`),
	})
	require.NoError(t, err)

	// The capture is durably written to Dynamo within a couple of sync ticks.
	ctx := context.Background()
	assert.Eventually(t, func() bool {
		page, err := dynStore.ListMatched(ctx, "orders", matched.Query{})
		return err == nil && len(page.Items) == 1 && page.Items[0].Protocol == "aws-sns"
	}, 2*time.Second, 20*time.Millisecond, "event capture must persist to DynamoDB")

	// Restart: a second server hydrates the event capture from Dynamo.
	srv2, err := server.New(server.Config{
		Store: dynStore,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: syncInterval},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv2.Close() })

	admin2 := httptest.NewServer(srv2.AdminMux())
	t.Cleanup(admin2.Close)

	resp, err := http.Get(admin2.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	assert.GreaterOrEqual(t, len(page.Items), 1, "second server must hydrate event captures from DynamoDB")
}
```

- [ ] **Step 2: Verify it compiles + skips without docker.**

Run: `go build -tags integration ./...` then `go test -tags integration ./internal/adapters/out/dynamodb/ -run TestE2E_Dynamo_EventCapture -v`
Expected: compiles; the test SKIPs (DYNAMO_TEST_ENDPOINT unset) — output shows `--- SKIP`. Also run plain `make test` to confirm the non-tagged suite is unaffected.

  If Docker is available, run the full integration suite to confirm it actually passes: `make test-integration` (brings up DynamoDB Local, runs `-tags integration`). Report whether it ran green or only skipped.

- [ ] **Step 3: Commit.**

```bash
git add internal/adapters/out/dynamodb/e2e_dynamo_events_test.go
git commit -m "test(events): dynamo integration e2e for event rules + capture persistence"
```
No Co-Authored-By footer.

---

## Task 6: Docs + roadmap + index

**Files:**
- Modify: `docs/event-capture.md`
- Modify: `README.md`
- Modify: `docs/roadmap.md`
- Modify: `docs/plans/2026-06-17-aws-event-interception-index.md`

- [ ] **Step 1: `docs/event-capture.md`** — replace the "in-memory only" limitation with a **Persistence** section: event rules and event captures persist on DynamoDB / MongoDB / Cosmos (the same backends as HTTP rules/matched capture); event captures share the matched-capture store/table, distinguished by the `aws-*` protocol; native TTL expiry applies; captures hydrate into memory on restart. Document the dynamo flag `--dynamo-event-rules-table` (default `mockwave-event-rules`) and env `MOCKWAVE_DYNAMO_EVENT_RULES_TABLE`; note Mongo/Cosmos use the `event_rules` collection automatically. Note that with `--store json`, event rules live in the config file's `event_rules` array and captures stay in-memory (no separate persistence) — unchanged.

- [ ] **Step 2: README** — update the "Outgoing event capture (AWS)" bullet's trailing parenthetical from "(Cloud-store capture persistence on the roadmap.)" to note persistence is now supported, e.g. "Persists on DynamoDB/MongoDB/Cosmos like matched capture." Touch ONLY that bullet (not Roadmap/CLI sections).

- [ ] **Step 3: `docs/roadmap.md`** — in the "Outgoing event interception (AWS)" section, move cloud-store persistence to shipped (Phase 4 done). The AWS interception arc (Phases 1–4) is now complete; keep the genuinely-remaining items deferred (GCP Pub/Sub & Azure Service Bus, batch ops, consumer side, event fault injection, weighted/canary buckets, filter policies & fan-out, non-Go SDK fixtures, plus the Phase 3 forward-fidelity items).

- [ ] **Step 4: plan index** — mark Phase 4 **Delivered**, linking `2026-06-17-aws-event-interception-phase4-plan.md`, and add a closing note that the four-phase AWS interception arc is complete.

- [ ] **Step 5: Verify + commit.**

Run: `grep -n "persist\|Persist\|event_rules\|event-rules-table" docs/event-capture.md | head` and confirm links resolve.

```bash
git add docs/event-capture.md README.md docs/roadmap.md docs/plans/2026-06-17-aws-event-interception-index.md
git commit -m "docs(events): document cloud persistence (Phase 4)"
```
No Co-Authored-By footer.

---

## Self-Review

**Spec coverage (Phase 4 / index row):**
- `EventRuleStore` on DynamoDB → Task 1; MongoDB → Task 2; Cosmos (delegates) → Task 2. ✓
- Event-capture persistence on the cloud backends (reusing MatchedStore) → Task 4 (backend-agnostic; the dynamo/mongo/cosmos MatchedStore impls already exist and persist the full `matched.Request`, including the aws-* protocol + Identity/Forwarded/ForwardTarget). ✓
- Native TTL → reused from MatchedStore (dynamo `ttl` attr / mongo TTL index); event captures carry `TTL` already (`captureEvent` sets it). ✓
- Restart hydration → Task 4 (`ListMatched("")` + `awsCaptures` filter + `Hydrate`). ✓
- Per-backend integration tests → Task 5 (dynamo e2e: rule CRUD round-trip + capture persist + restart hydrate). ✓
- Not breaking existing deployments → Task 1 (dynamo `GetEventRules` tolerates missing table). ✓
- Flag/env wiring → Task 3. ✓

**Type consistency:** `Config.EventRulesTable`/`Store.eventRulesTable` (dynamo); `colEventRules`/`Store.eventRules`/`eventRuleDoc` (mongo); `EventConfig.Store`; `eventSink(EventConfig, store.DataStore) matched.Sink`; `awsCaptures`/`nonAWSCaptures([]matched.Request) []matched.Request`; `Server.eventSyncer`/`eventSweep`; `runEventSweep`. Names used identically across tasks. `GetEventRules`/`SaveEventRule`/`DeleteEventRule` signatures match the `store.EventRuleStore` interface in all three backends (compile-time assertions enforce it).

**Placeholder scan:** Production code is complete. Three tasks (1, 2, 5) instruct READing an existing test harness (the backend mock clients / mtest / integration helpers) and mirroring it for the new tests — this is reuse of an established, backend-specific pattern, not a vague "write tests" placeholder; the exact cases to cover are enumerated. Task 4 Step 4 instructs reading `runMatchedSweep` + the `Close`/`Shutdown` teardown to mirror — necessary because those exact bodies weren't reproduced here; the mirror target is precise.

**Risk notes:**
- The dynamo `GetEventRules` missing-table tolerance is the key safety property — without it, adding `EventRuleStore` to dynamo would break existing `--store dynamodb` deployments that lack the event-rules table. Task 1 covers it with a test.
- Sharing the matched table means enabling event capture requires the matched table to exist on the backend. Documented in Task 6. The two `aws-*` hydration filters keep the in-memory views separate; per-rule store queries are already homogeneous.
- Integration tests (Task 5) require Docker/DynamoDB Local; they skip cleanly otherwise and don't affect `make test`/coverage.
