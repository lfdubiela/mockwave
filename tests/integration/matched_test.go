package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
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
