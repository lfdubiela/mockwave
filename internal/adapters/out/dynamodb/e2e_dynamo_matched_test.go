//go:build integration

package dynamostore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_Dynamo_MatchedCaptureFullFlow proves the full matched-request capture
// pipeline backed by a real DynamoDB Local instance:
//
//  1. Persists a rule + simulation to Dynamo.
//  2. Builds a server with matched capture enabled (Dynamo as the write-behind sink).
//  3. Drives a POST /orders through the mock → asserts 201.
//  4. Validates the entry appears in the admin API (buffer).
//  5. Waits for the syncer to flush, then queries Dynamo DIRECTLY — proving
//     the entry was durably written to DynamoDB, not just held in memory.
//  6. Builds a SECOND server from the SAME Dynamo store and asserts that the
//     buffer is hydrated from Dynamo on startup (restart durability).
func TestE2E_Dynamo_MatchedCaptureFullFlow(t *testing.T) {
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)

	const (
		e2eRulesTable     = "e2e-matched-rules"
		e2eSimsTable      = "e2e-matched-sims"
		e2eMatchedTable   = "e2e-matched-requests"
		e2eFaultsTable    = "e2e-matched-faults"
		e2eScenariosTable = "e2e-matched-scenarios"
		ruleID            = "e2e-orders-rule"
		simID             = "e2e-orders-sim"
	)

	// ── 1. Create tables ──────────────────────────────────────────────────────
	createTable(t, client, e2eRulesTable)
	createTable(t, client, e2eSimsTable)
	createTable(t, client, e2eFaultsTable)
	createTable(t, client, e2eScenariosTable)
	createMatchedTable(t, client, e2eMatchedTable)

	dynStore := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable:     e2eRulesTable,
		SimsTable:      e2eSimsTable,
		FaultsTable:    e2eFaultsTable,
		ScenariosTable: e2eScenariosTable,
		MatchedTable:   e2eMatchedTable,
	})

	// ── 2. Persist rule + simulation ──────────────────────────────────────────
	require.NoError(t, dynStore.SaveSimulation(domain.Simulation{
		ID:       simID,
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 201, Body: map[string]any{"ok": true}},
	}))
	require.NoError(t, dynStore.SaveRule(domain.Rule{
		ID:   ruleID,
		Name: "e2e orders rule",
		Match: domain.MatchCriteria{
			Protocol: "http",
			Method:   "POST",
			Path:     "/orders",
		},
		Buckets: []domain.WeightedBucket{
			{Weight: 100, Action: domain.ActionSimulate, SimulationID: simID},
		},
	}))

	// ── 3. Build server (rules already in Dynamo; server.New calls rebuild) ───
	const syncInterval = 50 * time.Millisecond
	srv, err := server.New(server.Config{
		Store: dynStore,
		Matched: server.MatchedConfig{
			Enabled:      true,
			BufferSize:   100,
			SyncInterval: syncInterval,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	mockSrv := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	t.Cleanup(mockSrv.Close)
	adminSrv := httptest.NewServer(srv.AdminMux())
	t.Cleanup(adminSrv.Close)

	// ── 4. Drive a POST /orders ───────────────────────────────────────────────
	reqBody := `{"item":"widget","qty":3}`
	httpReq, err := http.NewRequest(http.MethodPost, mockSrv.URL+"/orders",
		bytes.NewBufferString(reqBody))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-CID", "e2e-dyn-1")

	httpResp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	_ = httpResp.Body.Close()
	assert.Equal(t, 201, httpResp.StatusCode, "mock should respond 201 per rule")

	// ── 5. Assert via admin API (buffer) ──────────────────────────────────────
	listURL := adminSrv.URL + "/api/matched/" + ruleID + "?headers=X-CID:e2e-dyn-1"
	listResp, err := http.Get(listURL)
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, 200, listResp.StatusCode)

	var page matched.Page
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&page))
	require.Len(t, page.Items, 1, "admin API should return exactly 1 matched entry")

	entry := page.Items[0]
	assert.Equal(t, "POST", entry.Method)
	assert.Equal(t, "/orders", entry.Path)

	// Fetch detail.
	detURL := adminSrv.URL + "/api/matched/" + ruleID + "/" + entry.ID
	detResp, err := http.Get(detURL)
	require.NoError(t, err)
	defer detResp.Body.Close()
	require.Equal(t, 200, detResp.StatusCode)

	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.Equal(t, "e2e-dyn-1", headerCI(full.Request.Headers, "x-cid"),
		"captured X-CID header must match what was sent")
	assert.JSONEq(t, reqBody, string(full.RequestBody),
		"captured request body must match what was sent")

	// ── 6. Wait for syncer flush → query Dynamo DIRECTLY ─────────────────────
	ctx := context.Background()
	assert.Eventually(t, func() bool {
		dynPage, err := dynStore.ListMatched(ctx, ruleID, matched.Query{})
		if err != nil || len(dynPage.Items) == 0 {
			return false
		}
		return dynPage.Items[0].ID == entry.ID
	}, 2*time.Second, 20*time.Millisecond,
		"entry must be durably written to DynamoDB within 2s")

	// Also verify GetMatched returns the full body from Dynamo.
	dynFull, err := dynStore.GetMatched(ctx, ruleID, entry.ID)
	require.NoError(t, err)
	require.NotNil(t, dynFull, "GetMatched must find the entry in DynamoDB")
	assert.JSONEq(t, reqBody, string(dynFull.RequestBody),
		"body stored in DynamoDB must match the original request body")

	// ── 7. Restart durability: second server hydrates from Dynamo ─────────────
	srv2, err := server.New(server.Config{
		Store: dynStore,
		Matched: server.MatchedConfig{
			Enabled:      true,
			BufferSize:   100,
			SyncInterval: syncInterval,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv2.Close() })

	admin2 := httptest.NewServer(srv2.AdminMux())
	t.Cleanup(admin2.Close)

	list2Resp, err := http.Get(admin2.URL + "/api/matched/" + ruleID)
	require.NoError(t, err)
	defer list2Resp.Body.Close()
	require.Equal(t, 200, list2Resp.StatusCode)

	var page2 matched.Page
	require.NoError(t, json.NewDecoder(list2Resp.Body).Decode(&page2))
	assert.GreaterOrEqual(t, len(page2.Items), 1,
		"second server must hydrate matched entries from DynamoDB on startup")
}

// headerCI looks up a header key case-insensitively in the map returned by
// matched.FullRequest.Headers (stored as flat string map).
func headerCI(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
