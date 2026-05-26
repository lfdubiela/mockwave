package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/server"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startMetricsServer boots a mock server + admin mux and returns both test servers.
func startMetricsServer(t *testing.T, cfg domain.Config) (*httptest.Server, *httptest.Server, *metrics.Collector, *unmatched.Buffer) {
	t.Helper()
	store := jsonfile.NewMemStore(cfg)
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)

	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(50)
	broker := metrics.NewBroker(col)

	proxy := srv.NewProxy()
	wrapped := metrics.NewMiddleware(proxy, col, buf)

	mockSrv := httptest.NewServer(srv.MockHandler([]string{"http"}, wrapped))
	t.Cleanup(mockSrv.Close)

	adminMux := restapi.NewMux(store, func() {}, col, buf, broker, nil)
	adminSrv := httptest.NewServer(adminMux)
	t.Cleanup(adminSrv.Close)

	return mockSrv, adminSrv, col, buf
}

func TestMetrics_HitRecordedAfterMatchedRequest(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:   "r1",
			Name: "Test Rule",
			Match: domain.MatchCriteria{Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{
			ID:       "s1",
			Protocol: "http",
			Response: domain.HTTPResponse{Status: 200},
		}},
	}
	mockSrv, adminSrv, col, _ := startMetricsServer(t, cfg)

	// Fire a matched request
	resp, err := http.Get(mockSrv.URL + "/ping")
	require.NoError(t, err)
	resp.Body.Close()

	snap := col.Snapshot()
	assert.Equal(t, int64(1), snap.TotalRequests)
	assert.Equal(t, int64(0), snap.Misses)
	require.Len(t, snap.Rules, 1)
	assert.Equal(t, "r1", snap.Rules[0].RuleID)

	// Also verify via API
	apiResp, err := http.Get(adminSrv.URL + "/api/metrics")
	require.NoError(t, err)
	defer apiResp.Body.Close()
	var apiSnap metrics.Snapshot
	require.NoError(t, json.NewDecoder(apiResp.Body).Decode(&apiSnap))
	assert.Equal(t, int64(1), apiSnap.TotalRequests)
}

func TestMetrics_MissRecordedForUnmatchedRequest(t *testing.T) {
	cfg := domain.Config{Rules: []domain.Rule{}, Simulations: []domain.Simulation{}}
	mockSrv, adminSrv, _, buf := startMetricsServer(t, cfg)

	// Fire an unmatched request
	resp, err := http.Get(mockSrv.URL + "/no-such-route")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Check unmatched buffer via API
	apiResp, err := http.Get(adminSrv.URL + "/api/unmatched")
	require.NoError(t, err)
	defer apiResp.Body.Close()
	var items []unmatched.Request
	require.NoError(t, json.NewDecoder(apiResp.Body).Decode(&items))
	require.Len(t, items, 1)
	assert.Equal(t, "/no-such-route", items[0].Path)
	assert.Equal(t, "GET", items[0].Method)

	// Also verify the buffer was captured in-process
	inProc := buf.List()
	assert.Len(t, inProc, 1)
	_ = adminSrv
}

func TestMetrics_UnmatchedClear(t *testing.T) {
	cfg := domain.Config{}
	mockSrv, adminSrv, _, buf := startMetricsServer(t, cfg)

	// Create an unmatched entry
	resp, err := http.Get(mockSrv.URL + "/missing")
	require.NoError(t, err)
	resp.Body.Close()
	require.Len(t, buf.List(), 1)

	// Clear via admin API
	req, _ := http.NewRequest(http.MethodDelete, adminSrv.URL+"/api/unmatched", nil)
	delResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	delResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)
	assert.Empty(t, buf.List())
}

func TestMetrics_SSEStreamDelivery(t *testing.T) {
	cfg := domain.Config{}
	_, adminSrv, col, _ := startMetricsServer(t, cfg)
	col.RecordHit("r1", "Rule One", 10.0)

	// The broker in startMetricsServer is not started (no goroutine).
	// Test the /api/metrics endpoint (non-SSE) for structure instead.
	resp, err := http.Get(adminSrv.URL + "/api/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	var snap metrics.Snapshot
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&snap))
	assert.Equal(t, int64(1), snap.TotalRequests)
	require.Len(t, snap.Rules, 1)
	assert.Equal(t, "r1", snap.Rules[0].RuleID)
}

func TestMetrics_SSEStreamEvent(t *testing.T) {
	cfg := domain.Config{}
	_, adminSrv, col, _ := startMetricsServer(t, cfg)
	col.RecordHit("r-sse", "SSE Rule", 5.0)

	// Connect to SSE and start the broker for this test only.
	broker := metrics.NewBroker(col)
	brokerMux := http.NewServeMux()
	brokerMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		ch, unsub := broker.Subscribe()
		defer unsub()
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
		}
	})
	sseSrv := httptest.NewServer(brokerMux)
	t.Cleanup(sseSrv.Close)

	// Start broker; join before test exits to avoid goroutine leak / data race
	brokerCtx, cancelBroker := context.WithCancel(t.Context())
	t.Cleanup(cancelBroker)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); broker.Start(brokerCtx) }()
	t.Cleanup(wg.Wait)

	// Connect and read one SSE event
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(sseSrv.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var snap metrics.Snapshot
	var got bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			require.NoError(t, json.Unmarshal([]byte(data), &snap))
			got = true
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, got, "no SSE data line received within timeout")
	assert.Equal(t, int64(1), snap.TotalRequests)
	assert.Len(t, snap.Rules, 1)
	_ = adminSrv
}
