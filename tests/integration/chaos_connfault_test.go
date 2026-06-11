package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connFaultConfig builds a rule that always simulates a body of the given size
// (so half/slow body assertions have a known full length) with the supplied
// connection-fault profile attached to its bucket.
func connFaultConfig(profile domain.FaultProfile, body string) domain.Config {
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
			Response: domain.HTTPResponse{Status: 200, Body: body},
		}},
		FaultProfiles: []domain.FaultProfile{profile},
	}
}

// generousClient avoids hanging the whole test run on hang/slowBody faults.
func generousClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

func TestIntegration_Chaos_ResetFault(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-reset", Name: "reset", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultReset, Probability: 1}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, "hello"))
	client := generousClient()
	resp, err := client.Get(ts.URL + "/chaos")
	// TCP RST observability varies across OSes. Relax (mirroring the adapter
	// tests): accept either a connection error, or any response that is not a
	// normal full 200 with the expected body.
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && readErr == nil && string(body) == "hello" {
		t.Fatalf("expected reset to disrupt the response, got a clean 200")
	}
}

func TestIntegration_Chaos_HangFault(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-hang", Name: "hang", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultHang, Probability: 1,
			Params: domain.FaultParams{MaxMs: 300},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, "hello"))
	client := generousClient()
	start := time.Now()
	resp, err := client.Get(ts.URL + "/chaos")
	elapsed := time.Since(start)
	if err == nil {
		resp.Body.Close()
	}
	assert.GreaterOrEqual(t, elapsed, 280*time.Millisecond, "hang must block ~max_ms before closing")
}

func TestIntegration_Chaos_HalfResponseFault(t *testing.T) {
	full := strings.Repeat("A", 1000)
	profile := domain.FaultProfile{
		ID: "p-half", Name: "half", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultHalfResponse, Probability: 1,
			Params: domain.FaultParams{Fraction: 0.5},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, full))
	client := generousClient()
	resp, err := client.Get(ts.URL + "/chaos")
	if err != nil {
		// Truncation may surface as a connection error; acceptable.
		return
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return // read error from truncated body is acceptable
	}
	assert.Less(t, len(body), len(full), "halfResponse must deliver a truncated body")
}

func TestIntegration_Chaos_SlowBodyFault(t *testing.T) {
	// ~2KB body at 2048 B/s ≈ 1s, comfortably clears the 500ms floor.
	full := strings.Repeat("B", 2048)
	profile := domain.FaultProfile{
		ID: "p-slow", Name: "slow", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultSlowBody, Probability: 1,
			Params: domain.FaultParams{BytesPerSec: 2048},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, full))
	client := generousClient()
	start := time.Now()
	resp, err := client.Get(ts.URL + "/chaos")
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	assert.GreaterOrEqual(t, time.Since(start), 500*time.Millisecond, "slowBody must throttle the body write")
}
