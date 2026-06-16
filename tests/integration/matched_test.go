package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_MatchedRequestCapture exercises the regressive validation flow:
// create rule → drive a request at the mock → assert the mock's response AND
// assert the exact request the caller sent, retrieved via the admin API.
func TestE2E_MatchedRequestCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "create-user",
			Name:  "Create User",
			Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/users"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "user-sim"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "user-sim", Protocol: "http",
			Response: domain.HTTPResponse{Status: 201, Body: map[string]any{"id": "u-1"}},
		}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// --- caller (system under test) sends a request to the mock ---
	body := `{"name":"Ada","email":"ada@example.com"}`
	req, _ := http.NewRequest(http.MethodPost, mock.URL+"/users", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CID", "req-uuid-9")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// --- tester validates the mock's RESPONSE ---
	assert.Equal(t, 201, resp.StatusCode)

	// --- tester validates the REQUEST sent to the mock ---
	listResp, err := http.Get(admin.URL + "/api/matched/create-user?headers=X-CID:req-uuid-9")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, 200, listResp.StatusCode)

	var page matched.Page
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	id := page.Items[0].ID
	assert.Equal(t, "POST", page.Items[0].Method)
	assert.Equal(t, "/users", page.Items[0].Path)

	detResp, err := http.Get(admin.URL + "/api/matched/create-user/" + id)
	require.NoError(t, err)
	defer detResp.Body.Close()
	require.Equal(t, 200, detResp.StatusCode)

	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.Equal(t, "req-uuid-9", headerCI(full.Headers, "x-cid"))
	assert.JSONEq(t, body, string(full.RequestBody))
}

// --- helpers ---

// getMatchedPage performs GET {adminURL}/api/matched/{ruleID}[?query] and decodes the response.
func getMatchedPage(t *testing.T, adminURL, ruleID, query string) matched.Page {
	t.Helper()
	url := adminURL + "/api/matched/" + ruleID
	if query != "" {
		url += "?" + query
	}
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	return page
}

// driveMockRequest sends a GET request to the mock with optional extra headers, returns the status code.
func driveMockRequest(t *testing.T, mockURL, method, path string, extraHeaders map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(method, mockURL+path, nil)
	require.NoError(t, err)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// --- Test 1: Pagination over HTTP (cursor + limit) ---

func TestE2E_MatchedPagination(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "get-item",
			Name:  "Get Item",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/item"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "item-sim"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "item-sim", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}},
		}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// Drive 5 requests with distinct X-Seq headers so we can track them.
	for i := 1; i <= 5; i++ {
		status := driveMockRequest(t, mock.URL, "GET", "/item", map[string]string{"X-Seq": fmt.Sprintf("%d", i)})
		assert.Equal(t, 200, status)
	}

	// First page: limit=2, no cursor.
	page1 := getMatchedPage(t, admin.URL, "get-item", "limit=2")
	assert.Len(t, page1.Items, 2, "first page should have 2 items")
	assert.NotEmpty(t, page1.NextCursor, "first page should have a next cursor")

	// Second page.
	page2 := getMatchedPage(t, admin.URL, "get-item", "limit=2&cursor="+page1.NextCursor)
	assert.Len(t, page2.Items, 2, "second page should have 2 items")
	assert.NotEmpty(t, page2.NextCursor, "second page should have a next cursor")

	// Third page (final).
	page3 := getMatchedPage(t, admin.URL, "get-item", "limit=2&cursor="+page2.NextCursor)
	assert.Len(t, page3.Items, 1, "third page should have 1 item")
	assert.Empty(t, page3.NextCursor, "third page should have no next cursor")

	// Collect all ids across pages; assert no duplicates and all 5 covered.
	seen := map[string]bool{}
	for _, page := range []matched.Page{page1, page2, page3} {
		for _, item := range page.Items {
			assert.False(t, seen[item.ID], "duplicate id %s across pages", item.ID)
			seen[item.ID] = true
		}
	}
	assert.Len(t, seen, 5, "expected exactly 5 unique ids across all pages")
}

// --- Test 2: Combined filters over HTTP ---

func TestE2E_MatchedFilters(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{
			{
				ID:    "post-a",
				Name:  "Post A",
				Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/a"},
				Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "sim-201"}},
			},
			{
				ID:    "get-b",
				Name:  "Get B",
				Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/b"},
				Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "sim-200"}},
			},
		},
		Simulations: []domain.Simulation{
			{ID: "sim-201", Protocol: "http", Response: domain.HTTPResponse{Status: 201, Body: map[string]any{"ok": true}}},
			{ID: "sim-200", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}}},
		},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// Drive POST /a requests: two with X-CID, one without.
	driveMockRequest(t, mock.URL, "POST", "/a", map[string]string{"X-CID": "cid-abc"})
	driveMockRequest(t, mock.URL, "POST", "/a", map[string]string{"X-CID": "cid-abc"})
	driveMockRequest(t, mock.URL, "POST", "/a", nil)

	// Drive GET /b requests.
	driveMockRequest(t, mock.URL, "GET", "/b", nil)
	driveMockRequest(t, mock.URL, "GET", "/b", nil)

	// Filter by method=POST&status=201 on post-a rule: should return all 3.
	page := getMatchedPage(t, admin.URL, "post-a", "method=POST&status=201")
	assert.Len(t, page.Items, 3, "expected 3 POST /a items with status 201")
	for _, item := range page.Items {
		assert.Equal(t, "POST", item.Method)
		assert.Equal(t, 201, item.ResponseStatus)
	}

	// Filter by path glob /a* on post-a rule.
	page = getMatchedPage(t, admin.URL, "post-a", "path=%2Fa*")
	assert.Len(t, page.Items, 3, "expected 3 items matching path /a*")

	// Filter by headers=X-CID:cid-abc AND method=POST: should return 2.
	page = getMatchedPage(t, admin.URL, "post-a", "method=POST&headers=X-CID:cid-abc")
	assert.Len(t, page.Items, 2, "expected 2 items with X-CID:cid-abc header")
	for _, item := range page.Items {
		assert.Equal(t, "cid-abc", headerCI(item.Headers, "x-cid"))
	}

	// Non-matching filter: status=404 should return empty items.
	page = getMatchedPage(t, admin.URL, "post-a", "status=404")
	assert.Empty(t, page.Items, "expected empty items for non-matching status filter")
	assert.Empty(t, page.NextCursor)

	// Filtering get-b rule by method=POST should return empty (all are GET).
	page = getMatchedPage(t, admin.URL, "get-b", "method=POST")
	assert.Empty(t, page.Items, "expected empty items when method filter doesn't match")
}

// --- Test 3: Detail fallback to store after buffer eviction ---

func TestE2E_MatchedEvictionFallback(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "evict-rule",
			Name:  "Evict Rule",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/evict"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "evict-sim"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "evict-sim", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}},
		}},
	})
	// BufferSize=2, SyncInterval short to flush quickly.
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 2, SyncInterval: 50 * time.Millisecond},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// Send request #1 with a unique header.
	driveMockRequest(t, mock.URL, "GET", "/evict", map[string]string{"X-Evict": "first"})

	// Read its id from the list before eviction.
	var evictedID string
	assert.Eventually(t, func() bool {
		page := getMatchedPage(t, admin.URL, "evict-rule", "headers=X-Evict:first")
		if len(page.Items) == 1 {
			evictedID = page.Items[0].ID
			return true
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "request #1 should appear in list")

	// Wait for the syncer to flush request #1 to the store.
	assert.Eventually(t, func() bool {
		storePage, err := st.ListMatched(context.Background(), "evict-rule", store.MatchedQuery{Limit: 100})
		if err != nil {
			return false
		}
		for _, item := range storePage.Items {
			if item.ID == evictedID {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "request #1 should be flushed to store")

	// Drive 4 more requests to force eviction of #1 from the buffer (cap=2).
	for i := 0; i < 4; i++ {
		driveMockRequest(t, mock.URL, "GET", "/evict", nil)
	}

	// Confirm request #1 is no longer in the buffer.
	_, inBuffer := srv.MatchedBuffer().Get("evict-rule", evictedID)
	assert.False(t, inBuffer, "evicted id should not be in the buffer")

	// GET detail for evicted id should fall back to the store and return 200.
	assert.Eventually(t, func() bool {
		resp, err := http.Get(admin.URL + "/api/matched/evict-rule/" + evictedID)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			return false
		}
		defer resp.Body.Close()
		var full matched.FullRequest
		if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
			return false
		}
		assert.Equal(t, "GET", full.Method)
		assert.Equal(t, "/evict", full.Path)
		return true
	}, 2*time.Second, 20*time.Millisecond, "detail for evicted id should be served from store")
}

// --- Test 4: Write-behind sync + restart hydration ---

func TestE2E_MatchedRestartHydration(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:    "hydrate-rule",
			Name:  "Hydrate Rule",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/hydrate"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "hydrate-sim"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "hydrate-sim", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}},
		}},
	}
	st := jsonfile.NewMemStore(cfg)

	// server1: capture + short sync.
	srv1, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: 50 * time.Millisecond},
	})
	require.NoError(t, err)

	mock1 := httptest.NewServer(srv1.MockHandler([]string{"http"}, srv1.NewProxy()))
	admin1 := httptest.NewServer(srv1.AdminMux())

	// Drive 3 matched requests through server1.
	for i := 0; i < 3; i++ {
		status := driveMockRequest(t, mock1.URL, "GET", "/hydrate", nil)
		assert.Equal(t, 200, status)
	}

	// Wait until all 3 are flushed to the store.
	assert.Eventually(t, func() bool {
		storePage, err := st.ListMatched(context.Background(), "hydrate-rule", store.MatchedQuery{Limit: 100})
		if err != nil {
			return false
		}
		return len(storePage.Items) >= 3
	}, 2*time.Second, 20*time.Millisecond, "3 requests should be flushed to store")

	// Also confirm via admin1 that all 3 are listed.
	page := getMatchedPage(t, admin1.URL, "hydrate-rule", "")
	assert.GreaterOrEqual(t, len(page.Items), 3)

	// Capture an id for later verification.
	capturedID := page.Items[0].ID

	// Close server1 (triggers final flush).
	mock1.Close()
	admin1.Close()
	require.NoError(t, srv1.Close())

	// server2: reuse the same store — hydration on New should seed the buffer.
	srv2, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv2.Close()

	admin2 := httptest.NewServer(srv2.AdminMux())
	defer admin2.Close()

	// The list endpoint should show the hydrated entries.
	page2 := getMatchedPage(t, admin2.URL, "hydrate-rule", "")
	assert.GreaterOrEqual(t, len(page2.Items), 3, "hydrated entries should be present in server2")

	// Detail for a previously-captured id should return 200 (either from buffer or store fallback).
	detResp, err := http.Get(admin2.URL + "/api/matched/hydrate-rule/" + capturedID)
	require.NoError(t, err)
	defer detResp.Body.Close()
	assert.Equal(t, 200, detResp.StatusCode, "detail for hydrated id should be 200")

	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.Equal(t, "GET", full.Method)
	assert.Equal(t, "/hydrate", full.Path)
}

func headerCI(h map[string]string, key string) string {
	for k, v := range h {
		if equalFoldASCII(k, key) {
			return v
		}
	}
	return ""
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
