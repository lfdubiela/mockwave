package integration_test

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_Chaos_RetryStorm verifies that a retryStorm fault fails the
// first fail_first requests for a key (here key_by: path) with the configured
// status, then lets subsequent requests through to the real simulation.
func TestIntegration_Chaos_RetryStorm(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-retry", Name: "retry storm", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{
				FailFirst:  2,
				StatusCode: 503,
				KeyBy:      "path",
				WindowSec:  60,
			},
		}},
	}
	ts, _ := newChaosServer(t, chaosConfig(profile))
	client := generousClient()

	// fail_first=2 → first two requests to the same path fail with 503,
	// the third passes through to sim-ok (200).
	want := []int{503, 503, 200}
	for i, code := range want {
		resp, err := client.Get(ts.URL + "/chaos")
		require.NoError(t, err)
		assert.Equal(t, code, resp.StatusCode, "request %d", i)
		resp.Body.Close()
	}
}
