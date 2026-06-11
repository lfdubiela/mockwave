package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scenarioControl adapts the integration test's *server.Server to the
// restapi.ScenarioControl seam. The server's own adapter is unexported, so the
// test wires an equivalent one to build the admin mux with scenario endpoints.
type scenarioControl struct {
	srv   *server.Server
	store interface {
		GetScenario(id string) (*domain.Scenario, error)
	}
}

func (c scenarioControl) Start(id string) error {
	sc, err := c.store.GetScenario(id)
	if err != nil {
		return err
	}
	if sc == nil {
		return restapi.ErrScenarioNotFound
	}
	if startErr := c.srv.StartScenario(*sc); startErr != nil {
		return restapi.ErrScenarioRunning
	}
	return nil
}

func (c scenarioControl) Stop() { c.srv.StopScenario() }

func (c scenarioControl) ActiveStatus() any {
	run := c.srv.Scenario().Active()
	if run == nil {
		return nil
	}
	return map[string]any{
		"scenario_id":      run.ScenarioID,
		"scenario_name":    run.ScenarioName,
		"phase_index":      run.PhaseIndex,
		"phase_count":      run.PhaseCount,
		"phase_profile_id": run.PhaseProfileID,
	}
}

// scenarioConfig: rule r1 always simulates a clean 200, with a "boom" fault
// profile (always 503) available but NOT attached to the bucket. A scenario
// targets r1 with a single multi-second boom phase, so faults fire only while
// the scenario is active.
func scenarioConfig() domain.Config {
	return domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/chaos"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate, SimulationID: "sim-ok",
			}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-ok", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"ok": true}},
		}},
		FaultProfiles: []domain.FaultProfile{{
			ID: "boom", Name: "always 503", Enabled: true,
			Faults: []domain.Fault{{
				Type: domain.FaultError, Probability: 1,
				Params: domain.FaultParams{StatusCode: 503, Body: `{"error":"injected"}`},
			}},
		}},
		Scenarios: []domain.Scenario{{
			ID:      "drill",
			Name:    "Drill",
			RuleIDs: []string{"r1"},
			Phases:  []domain.ScenarioPhase{{DurationSec: 30, FaultProfileID: "boom"}},
		}},
	}
}

func TestIntegration_Chaos_Scenario_StartStop(t *testing.T) {
	ts, srv := newChaosServer(t, scenarioConfig())

	ss, ok := srv.Store().(interface {
		GetScenario(id string) (*domain.Scenario, error)
	})
	require.True(t, ok, "jsonfile store must implement ScenarioStore")

	admin := httptest.NewServer(restapi.NewMux(srv.Store(), nil, nil, nil, nil, nil,
		restapi.WithKillSwitch(srv.KillSwitch()),
		restapi.WithScenarioControl(scenarioControl{srv: srv, store: ss})))
	defer admin.Close()

	get := func() int {
		resp, err := http.Get(ts.URL + "/chaos")
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	post := func(path string, wantCode int) {
		resp, err := http.Post(admin.URL+path, "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, wantCode, resp.StatusCode, path)
	}

	// Before start: clean 200, no active scenario.
	assert.Equal(t, 200, get(), "no scenario active → clean 200")

	post("/api/scenarios/drill/start", http.StatusAccepted)

	// The scenario overlay applies boom (503) to r1.
	require.Eventually(t, func() bool { return get() == 503 }, 2*time.Second, 20*time.Millisecond,
		"scenario phase profile must inject 503 on the targeted rule")

	// Status reports the active scenario.
	resp, err := http.Get(admin.URL + "/api/chaos/status")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var status struct {
		ActiveScenario map[string]interface{} `json:"active_scenario"`
	}
	require.NoError(t, json.Unmarshal(body, &status))
	require.NotNil(t, status.ActiveScenario, "status must report the active scenario: %s", body)
	assert.Equal(t, "drill", status.ActiveScenario["scenario_id"])

	// Explicit stop (do not wait for phase elapse).
	post("/api/scenarios/drill/stop", http.StatusNoContent)

	require.Eventually(t, func() bool { return get() == 200 }, 2*time.Second, 20*time.Millisecond,
		"after stop the rule must return its clean 200 again")
}
