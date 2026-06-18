package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/mcp"
	"github.com/mockwave/mockwave/server"
)

// TestIntegration_LiveAdmin boots a REAL Mockwave admin server (jsonfile store
// backed by a temp file so admin POST handlers can persist) and exercises one
// representative method from every new MCP client tool group end-to-end. This
// catches mismatches between the MCP client's URL/shape assumptions and the
// real admin API — the per-group unit tests only ran against hand-written stubs.
func TestIntegration_LiveAdmin(t *testing.T) {
	// jsonfile.NewMemStore has an empty path: SaveRule/SaveFaultProfile/etc.
	// FLUSH and error, so the admin POST handlers (which call SaveX) would 500.
	// A temp-file-backed NewStore makes writes succeed. Seed it with one HTTP
	// rule so the scenario (step 4) has a valid rule_id to target and the rule
	// + scenario + referenced fault end up in the export (step 6).
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seed := domain.Config{
		Rules: []domain.Rule{{
			ID:    "http-rule",
			Name:  "seed rule",
			Match: domain.MatchCriteria{Protocol: "http", Path: "/seed"},
			Buckets: []domain.WeightedBucket{
				{Weight: 100, Action: domain.ActionSimulate, SimulationID: "sim"},
			},
		}},
		Simulations: []domain.Simulation{{
			ID:       "sim",
			Protocol: "http",
			Response: domain.HTTPResponse{Status: 200},
		}},
	}
	seedBytes, err := json.MarshalIndent(seed, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, seedBytes, 0o600))

	st, err := jsonfile.NewStore(cfgPath)
	require.NoError(t, err)

	srv, err := server.New(server.Config{
		Store:        st,
		ImportExport: true, // enables GET /api/export (step 6)
		Event:        server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	client := mcp.NewClient(admin.URL)

	// 1. Event rules (P0) -----------------------------------------------------
	_, err = client.CreateEventRule(domain.EventRule{
		ID:    "orders",
		Match: domain.EventMatch{Service: domain.EventServiceSNS},
	})
	require.NoError(t, err, "CreateEventRule must succeed against the real admin API")

	rules, err := client.ListEventRules()
	require.NoError(t, err)
	require.True(t, containsEventRule(rules, "orders"), "ListEventRules must contain the created rule")

	got, err := client.GetEventRule("orders")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "orders", got.ID)
	assert.Equal(t, domain.EventServiceSNS, got.Match.Service)

	// 2. Event captures (P0) — the headline round-trip ------------------------
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	snsClient := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		o.BaseEndpoint = aws.String(mock.URL)
	})
	for _, cid := range []string{"abc-123", "zzz-999"} {
		_, err := snsClient.Publish(context.Background(), &sns.PublishInput{
			TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:orders"),
			Message:  aws.String(`{"correlation_id":"` + cid + `","total":42}`),
		})
		require.NoError(t, err, "real SNS SDK must accept the synthesized response")
	}

	page, err := client.ListEventCaptures("orders", url.Values{"body": {"$.correlation_id:abc-123"}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "body filter must return exactly the abc-123 event")

	full, err := client.GetEventCapture("orders", page.Items[0].ID)
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.JSONEq(t, `{"correlation_id":"abc-123","total":42}`, string(full.RequestBody))

	// Sanity: the unfiltered list returns both events.
	all, err := client.ListEventCaptures("orders", nil)
	require.NoError(t, err)
	require.Len(t, all.Items, 2)

	// 3. Fault profiles (P2) --------------------------------------------------
	fp := domain.FaultProfile{
		ID:      "latency",
		Name:    "Added latency",
		Enabled: true,
		Faults: []domain.Fault{{
			Type:        domain.FaultJitter,
			Probability: 1,
			Params:      domain.FaultParams{BaseDelayMs: 50},
		}},
	}
	_, err = client.CreateFaultProfile(fp)
	require.NoError(t, err, "CreateFaultProfile must succeed against the real admin API")

	faults, err := client.ListFaultProfiles()
	require.NoError(t, err)
	require.True(t, containsFault(faults, "latency"), "ListFaultProfiles must contain the created profile")

	gotFP, err := client.GetFaultProfile("latency")
	require.NoError(t, err)
	require.NotNil(t, gotFP)
	assert.Equal(t, "latency", gotFP.ID)

	// 4. Scenarios (P2) — targets the seeded http-rule, references the fault. --
	sc := domain.Scenario{
		ID:      "outage",
		Name:    "Brief outage",
		RuleIDs: []string{"http-rule"},
		Phases:  []domain.ScenarioPhase{{DurationSec: 1, FaultProfileID: "latency"}},
	}
	_, err = client.CreateScenario(sc)
	require.NoError(t, err, "CreateScenario must succeed against the real admin API")

	scenarios, err := client.ListScenarios()
	require.NoError(t, err)
	require.True(t, containsScenario(scenarios, "outage"), "ListScenarios must contain the created scenario")

	// 5. Chaos control (P2) ---------------------------------------------------
	status, err := client.ChaosStatus()
	require.NoError(t, err, "ChaosStatus must succeed against the real admin API")
	require.NotNil(t, status)
	_, hasHalted := status["halted"]
	assert.True(t, hasHalted, "chaos status must carry a 'halted' flag")

	// 6. Import/export (P3) — rule + scenario + transitively the fault. -------
	// Note: the export endpoint is known NOT to emit event_rules (server-side
	// gap), so we deliberately do not assert event_rules here.
	exported, err := client.ExportConfig()
	require.NoError(t, err, "ExportConfig must succeed (requires ImportExport=true)")
	require.NotNil(t, exported)
	assert.True(t, containsRule(exported.Rules, "http-rule"), "export must include the seeded HTTP rule")
	assert.True(t, containsScenario(exported.Scenarios, "outage"), "export must include the created scenario")
	assert.True(t, containsFault(exported.FaultProfiles, "latency"),
		"export must include the fault profile referenced by the scenario phase")

	// 7. Dev (P4) -------------------------------------------------------------
	hist, err := client.MetricsHistory()
	require.NoError(t, err, "MetricsHistory must succeed against the real admin API")
	require.NotNil(t, hist)
	_, hasRules := hist["rules"]
	assert.True(t, hasRules, "metrics history must carry a 'rules' key")

	// EvalScript (optional) — a trivial script that sets a JSON response body.
	evalOut, err := client.EvalScript(map[string]any{"script": "response.body = {ok: true}"})
	require.NoError(t, err, "EvalScript must succeed against the real admin API")
	require.NotNil(t, evalOut)
}

func containsEventRule(rs []domain.EventRule, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func containsFault(fs []domain.FaultProfile, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

func containsScenario(ss []domain.Scenario, id string) bool {
	for _, s := range ss {
		if s.ID == id {
			return true
		}
	}
	return false
}

func containsRule(rs []domain.Rule, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}
