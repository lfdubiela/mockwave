package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T, cfg domain.Config) *httptest.Server {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mockwave-it-*.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(f).Encode(cfg))
	require.NoError(t, f.Close())
	store, err := jsonfile.NewStore(f.Name())
	require.NoError(t, err)
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)
	return ts
}

func TestIntegration_SimpleMockResponse(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-pong"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-pong", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"msg": "pong"}},
		}},
	}
	ts := newTestServer(t, cfg)
	resp, err := http.Get(ts.URL + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "pong", body["msg"])
}

func TestIntegration_HeaderMatchTakesPriority(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{
			{ID: "generic", Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/users/*"},
				Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-generic"}}},
			{ID: "specific", Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/users/*", Headers: map[string]string{"x-cid": "12345"}},
				Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-specific"}}},
		},
		Simulations: []domain.Simulation{
			{ID: "sim-generic", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"who": "generic"}}},
			{ID: "sim-specific", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"who": "specific"}}},
		},
	}
	ts := newTestServer(t, cfg)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/users/1", nil)
	req.Header.Set("x-cid", "12345")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "specific", body["who"])
}

func TestIntegration_NoMatchReturns404(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/orders"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 201}}},
	}
	ts := newTestServer(t, cfg)
	resp, err := http.Get(ts.URL + "/unknown")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
}

func TestIntegration_PercentileForwardProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"source":"upstream"}`)
	}))
	defer upstream.Close()
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/data"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionForward, ForwardURL: upstream.URL}},
		}},
	}
	ts := newTestServer(t, cfg)
	resp, err := http.Get(ts.URL + "/data")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "upstream", body["source"])
}

func TestIntegration_ForwardProxyWithDelay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"source":"upstream"}`)
	}))
	defer upstream.Close()
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/data"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionForward, DelayMs: 300, ForwardURL: upstream.URL}},
		}},
	}
	ts := newTestServer(t, cfg)
	start := time.Now()
	resp, err := http.Get(ts.URL + "/data")
	require.NoError(t, err)
	defer resp.Body.Close()
	elapsed := time.Since(start)
	assert.Equal(t, 200, resp.StatusCode)
	// Delay (300ms) dominates the fast local upstream → full-stack latency >= delay.
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "forward delay must apply through the full server pipeline")
	// And it must NOT double-apply (handler.go also sleeping) → well under 2x.
	assert.Less(t, elapsed, 1*time.Second, "delay must not double-apply via the mock handler path")
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "upstream", body["source"])
}

func TestIntegration_ScriptModifiesResponse(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/scripted"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-scripted"}},
		}},
		Simulations: []domain.Simulation{{
			ID:       "sim-scripted",
			Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"x": 1}},
			Script:   `response.body.cid = request.headers["x-cid"]; return response;`,
		}},
	}
	ts := newTestServer(t, cfg)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/scripted", nil)
	req.Header.Set("x-cid", "abc123")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "abc123", body["cid"])
}
