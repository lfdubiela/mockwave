package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWeightedBucket_Validate(t *testing.T) {
	t.Run("simulate bucket requires simulation_id", func(t *testing.T) {
		b := domain.WeightedBucket{Weight: 1, Action: domain.ActionSimulate}
		err := b.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "simulation_id")
	})

	t.Run("forward bucket valid without simulation_id", func(t *testing.T) {
		b := domain.WeightedBucket{Weight: 99, Action: domain.ActionForward}
		err := b.Validate()
		assert.NoError(t, err)
	})

	t.Run("zero weight invalid", func(t *testing.T) {
		b := domain.WeightedBucket{Weight: 0, Action: domain.ActionForward}
		err := b.Validate()
		assert.Error(t, err)
	})

	t.Run("invalid action rejected", func(t *testing.T) {
		b := domain.WeightedBucket{Weight: 1, Action: "proxy"}
		err := b.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "action")
	})
}

func TestRule_Validate(t *testing.T) {
	t.Run("forward bucket requires forward_url", func(t *testing.T) {
		r := domain.Rule{
			ID: "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/foo"},
			Buckets: []domain.WeightedBucket{
				{Weight: 1, Action: domain.ActionForward},
			},
		}
		err := r.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "forward_url")
	})

	t.Run("valid rule passes", func(t *testing.T) {
		r := domain.Rule{
			ID: "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/foo"},
			Buckets: []domain.WeightedBucket{
				{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim1"},
			},
		}
		err := r.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty id invalid", func(t *testing.T) {
		r := domain.Rule{
			Match:   domain.MatchCriteria{Path: "/foo"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
		}
		err := r.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "id")
	})
}

func TestSimulation_SOAP_JSONRoundTrip(t *testing.T) {
	sim := domain.Simulation{
		ID:           "sim-soap",
		Protocol:     "soap",
		SOAPEnvelope: `<soap:Envelope/>`,
	}
	data, err := json.Marshal(sim)
	require.NoError(t, err)

	// JSON must use snake_case tag
	assert.Contains(t, string(data), `"soap_envelope"`)
	// grpc fields omitted when zero-value (omitempty)
	assert.NotContains(t, string(data), `"grpc_message"`)
	assert.NotContains(t, string(data), `"grpc_status"`)

	var decoded domain.Simulation
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, sim.SOAPEnvelope, decoded.SOAPEnvelope)
	assert.Equal(t, sim.Protocol, decoded.Protocol)
}

func TestSimulation_GRPC_JSONRoundTrip(t *testing.T) {
	sim := domain.Simulation{
		ID:          "sim-grpc",
		Protocol:    "grpc",
		GRPCMessage: `{"id":"42"}`,
		GRPCStatus:  5, // codes.NotFound
	}
	data, err := json.Marshal(sim)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"grpc_message"`)
	assert.Contains(t, string(data), `"grpc_status"`)
	// soap_envelope omitted when empty (omitempty)
	assert.NotContains(t, string(data), `"soap_envelope"`)

	var decoded domain.Simulation
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, sim.GRPCMessage, decoded.GRPCMessage)
	assert.Equal(t, sim.GRPCStatus, decoded.GRPCStatus)
}
