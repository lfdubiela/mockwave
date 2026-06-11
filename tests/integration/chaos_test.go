package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chaosConfig is a rule that always simulates sim-ok (200) with the error
// fault profile p-err (probability 1, status 503) attached to its bucket.
func chaosConfig(profile domain.FaultProfile) domain.Config {
	return domain.Config{
		Rules: []domain.Rule{{
			ID:    "r-chaos",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/chaos"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate,
				SimulationID: "sim-ok", FaultProfileID: profile.ID,
			}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-ok", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"ok": true}},
		}},
		FaultProfiles: []domain.FaultProfile{profile},
	}
}

// newChaosServer boots a full server from cfg and returns the mock test
// server plus the underlying *server.Server (for kill switch access).
func newChaosServer(t *testing.T, cfg domain.Config) (*httptest.Server, *server.Server) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mockwave-chaos-*.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(f).Encode(cfg))
	require.NoError(t, f.Close())
	store, err := jsonfile.NewStore(f.Name())
	require.NoError(t, err)
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)
	return ts, srv
}

func errorProfile() domain.FaultProfile {
	return domain.FaultProfile{
		ID: "p-err", Name: "always 503", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultError, Probability: 1,
			Params: domain.FaultParams{StatusCode: 503, Body: `{"error":"injected"}`},
		}},
	}
}

func TestIntegration_Chaos_ErrorFault(t *testing.T) {
	ts, _ := newChaosServer(t, chaosConfig(errorProfile()))
	resp, err := http.Get(ts.URL + "/chaos")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 503, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "injected")
}

func TestIntegration_Chaos_KillSwitch(t *testing.T) {
	ts, srv := newChaosServer(t, chaosConfig(errorProfile()))
	admin := httptest.NewServer(restapi.NewMux(nil, nil, nil, nil, nil, nil,
		restapi.WithKillSwitch(srv.KillSwitch())))
	defer admin.Close()

	get := func(path string) int {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	post := func(path string) {
		resp, err := http.Post(admin.URL+path, "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 204, resp.StatusCode)
	}

	assert.Equal(t, 503, get("/chaos"), "fault active before halt")
	post("/api/chaos/halt")
	assert.Equal(t, 200, get("/chaos"), "kill switch suppresses faults")
	post("/api/chaos/resume")
	assert.Equal(t, 503, get("/chaos"), "fault active again after resume")
}

func TestIntegration_Chaos_Jitter(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-jit", Name: "slow", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultJitter, Probability: 1,
			Params: domain.FaultParams{BaseDelayMs: 200},
		}},
	}
	ts, _ := newChaosServer(t, chaosConfig(profile))
	start := time.Now()
	resp, err := http.Get(ts.URL + "/chaos")
	require.NoError(t, err)
	defer resp.Body.Close()
	elapsed := time.Since(start)
	assert.Equal(t, 200, resp.StatusCode, "jitter must not change the response")
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "jitter base delay must apply end-to-end")
}
