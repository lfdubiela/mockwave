//go:build integration

package dynamostore_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mockwave/mockwave/domain"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/domain/routing"
	"github.com/mockwave/mockwave/internal/domain/simulation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rulesTable = "mockwave-rules"
	simsTable  = "mockwave-simulations"
)

// dynamoEndpoint returns the DynamoDB Local endpoint from DYNAMO_TEST_ENDPOINT,
// or skips the test if unset. Run a local instance first, e.g.:
//
//	java -Djava.library.path=./DynamoDBLocal_lib -jar DynamoDBLocal.jar -inMemory -port 8000
//	DYNAMO_TEST_ENDPOINT=http://localhost:8000 go test -tags integration ./...
func dynamoEndpoint(t *testing.T) string {
	t.Helper()
	ep := os.Getenv("DYNAMO_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("DYNAMO_TEST_ENDPOINT not set — start DynamoDB Local and set it")
	}
	return ep
}

// newLocalClient builds a DynamoDB client pointed at the local endpoint and
// waits until it answers.
func newLocalClient(t *testing.T, endpoint string) *dynamodb.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	require.NoError(t, err)
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := client.ListTables(context.Background(), &dynamodb.ListTablesInput{})
		if err == nil {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("dynamodb-local never became ready: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func createTable(t *testing.T, client *dynamodb.Client, name string) {
	t.Helper()
	// Drop any leftover table from a prior run so each test starts clean.
	_, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
		TableName: aws.String(name),
	})
	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String(name),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	require.NoError(t, err)
}

// TestDynamoLocal_5050Distribution reproduces the manual-test scenario:
// one rule with two 50/50 buckets (503 and 200). Drives the real pipeline
// against DynamoDB local and asserts BOTH statuses fire.
func TestDynamoLocal_5050Distribution(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)

	store := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: rulesTable,
		SimsTable:  simsTable,
	})

	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-200",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}},
	}))
	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-503",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 503, Body: map[string]any{"err": "down"}},
	}))
	require.NoError(t, store.SaveRule(domain.Rule{
		ID:    "rule-5050",
		Name:  "fifty-fifty",
		Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/health"},
		Buckets: []domain.WeightedBucket{
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-503"},
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-200"},
		},
	}))

	// Reload rules from the store exactly as the server does at boot.
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1, "expected exactly one rule")
	require.Len(t, rules[0].Buckets, 2, "rule must have two buckets")

	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(mustSimMap(t, store))
	p := pipeline.New(matchStage, routeStage, simStage)

	counts := map[int]int{}
	const n = 400
	for i := 0; i < n; i++ {
		pctx := &pipeline.PipelineContext{
			Request: pipeline.NormalizedRequest{
				Protocol: "http",
				Method:   "GET",
				Path:     "/health",
			},
		}
		require.NoError(t, p.Execute(context.Background(), pctx))
		require.NotNil(t, pctx.Response)
		counts[pctx.Response.Status]++
	}

	t.Logf("status distribution over %d runs: %v", n, counts)
	require.Greater(t, counts[503], 0, "503 bucket never fired — bug reproduced")
	require.Greater(t, counts[200], 0, "200 bucket never fired")
}

// TestDynamoLocal_PostFiftyFifty mirrors the manual test the user described:
// POST /some/url with a 50/50 split between 200 OK and 500 NOK. Drives the real
// DynamoDB-backed pipeline and asserts the weighted buckets actually distribute.
func TestDynamoLocal_PostFiftyFifty(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)

	store := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: rulesTable,
		SimsTable:  simsTable,
	})

	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-ok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"status": "ok"}},
	}))
	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-nok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 500, Body: map[string]any{"status": "nok"}},
	}))
	require.NoError(t, store.SaveRule(domain.Rule{
		ID:    "rule-post-5050",
		Name:  "post-fifty-fifty",
		Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/some/url"},
		Buckets: []domain.WeightedBucket{
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-ok"},
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-nok"},
		},
	}))

	// Reload rules from the store exactly as the server does at boot.
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1, "expected exactly one rule")
	require.Len(t, rules[0].Buckets, 2, "rule must have two buckets")

	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(mustSimMap(t, store))
	p := pipeline.New(matchStage, routeStage, simStage)

	counts := map[int]int{}
	const n = 1000
	for i := 0; i < n; i++ {
		pctx := &pipeline.PipelineContext{
			Request: pipeline.NormalizedRequest{
				Protocol: "http",
				Method:   "POST",
				Path:     "/some/url",
			},
		}
		require.NoError(t, p.Execute(context.Background(), pctx))
		require.NotNil(t, pctx.Response)
		counts[pctx.Response.Status]++
	}

	t.Logf("POST /some/url status distribution over %d runs: %v", n, counts)
	require.Greater(t, counts[200], 0, "200 bucket never fired")
	require.Greater(t, counts[500], 0, "500 bucket never fired")

	// With equal weights, each bucket should land within ~10% of 50%.
	for status, c := range counts {
		ratio := float64(c) / float64(n)
		require.InDelta(t, 0.5, ratio, 0.1,
			"status %d ratio %.2f outside 50%%±10%%", status, ratio)
	}
}

// TestDynamoLocal_ConcurrentSplit reproduces the real-world skew: a 50/50 rule
// hit concurrently (parallelism=20) produces a badly biased distribution because
// PercentileRouterStage shares a single *rand.Rand with no synchronization, and
// math/rand.Rand is not safe for concurrent use. Run with -race to surface the
// data race directly.
func TestDynamoLocal_ConcurrentSplit(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)

	store := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: rulesTable,
		SimsTable:  simsTable,
	})

	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-ok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"status": "ok"}},
	}))
	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-nok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 500, Body: map[string]any{"status": "nok"}},
	}))
	require.NoError(t, store.SaveRule(domain.Rule{
		ID:    "rule-conc-5050",
		Name:  "concurrent-fifty-fifty",
		Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/some/url"},
		Buckets: []domain.WeightedBucket{
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-ok"},
			{Weight: 50, Action: domain.ActionSimulate, SimulationID: "sim-nok"},
		},
	}))

	rules, err := store.GetRules()
	require.NoError(t, err)

	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(mustSimMap(t, store))
	p := pipeline.New(matchStage, routeStage, simStage)

	const (
		total       = 2000
		parallelism = 20
	)
	var mu sync.Mutex
	counts := map[int]int{}

	var wg sync.WaitGroup
	jobs := make(chan struct{}, total)
	for i := 0; i < total; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				pctx := &pipeline.PipelineContext{
					Request: pipeline.NormalizedRequest{
						Protocol: "http",
						Method:   "POST",
						Path:     "/some/url",
					},
				}
				if err := p.Execute(context.Background(), pctx); err != nil {
					continue
				}
				if pctx.Response == nil {
					continue
				}
				mu.Lock()
				counts[pctx.Response.Status]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	okRatio := float64(counts[200]) / float64(total)
	t.Logf("CONCURRENT (parallelism=%d) distribution over %d runs: %v (200=%.1f%%)",
		parallelism, total, counts, okRatio*100)
	require.InDelta(t, 0.5, okRatio, 0.1,
		"200 ratio %.2f outside 50%%±10%% — concurrent rng skew", okRatio)
}

// TestDynamoLocal_ExactBeatsWildcard proves rule specificity ordering: an exact
// path rule and a single-segment wildcard rule both match the same request, but
// the matcher ranks the exact (no '*') rule higher, so the literal path gets the
// exact rule's response and any other segment falls through to the wildcard.
//
//	/random-rule/1234/create   -> exact rule    -> 200 OK
//	/random-rule/9999/create   -> wildcard rule -> 500 NOK
func TestDynamoLocal_ExactBeatsWildcard(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)

	store := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: rulesTable,
		SimsTable:  simsTable,
	})

	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-ok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"status": "ok"}},
	}))
	require.NoError(t, store.SaveSimulation(domain.Simulation{
		ID:       "sim-nok",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 500, Body: map[string]any{"status": "nok"}},
	}))

	// Exact rule -> 200.
	require.NoError(t, store.SaveRule(domain.Rule{
		ID:    "rule-exact",
		Name:  "exact",
		Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/random-rule/1234/create"},
		Buckets: []domain.WeightedBucket{
			{Weight: 100, Action: domain.ActionSimulate, SimulationID: "sim-ok"},
		},
	}))
	// Wildcard rule -> 500.
	require.NoError(t, store.SaveRule(domain.Rule{
		ID:    "rule-wildcard",
		Name:  "wildcard",
		Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/random-rule/*/create"},
		Buckets: []domain.WeightedBucket{
			{Weight: 100, Action: domain.ActionSimulate, SimulationID: "sim-nok"},
		},
	}))

	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 2)

	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(mustSimMap(t, store))
	p := pipeline.New(matchStage, routeStage, simStage)

	statusFor := func(path string) int {
		pctx := &pipeline.PipelineContext{
			Request: pipeline.NormalizedRequest{
				Protocol: "http",
				Method:   "POST",
				Path:     path,
			},
		}
		require.NoError(t, p.Execute(context.Background(), pctx))
		require.NotNil(t, pctx.Response)
		return pctx.Response.Status
	}

	// Exact path must hit the exact rule despite the wildcard also matching.
	assert.Equal(t, 200, statusFor("/random-rule/1234/create"),
		"exact path should match the exact rule (higher specificity)")
	// A different segment falls through to the wildcard rule.
	assert.Equal(t, 500, statusFor("/random-rule/9999/create"),
		"non-exact segment should match the wildcard rule")
}

// mustSimMap loads all simulations from the store into a snapshot map, mirroring
// how server.rebuild() feeds the simulation stage.
func mustSimMap(t *testing.T, st *dynamostore.Store) map[string]domain.Simulation {
	t.Helper()
	sims, err := st.ListSimulations()
	require.NoError(t, err)
	m := make(map[string]domain.Simulation, len(sims))
	for _, s := range sims {
		m[s.ID] = s
	}
	return m
}

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
