package domain_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/stretchr/testify/assert"
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

func TestSimulation_SOAPFields(t *testing.T) {
	sim := domain.Simulation{
		ID:           "sim-soap",
		Protocol:     "soap",
		SoapEnvelope: `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>42</id></GetUserResponse></soap:Body></soap:Envelope>`,
	}
	assert.Equal(t, "soap", sim.Protocol)
	assert.NotEmpty(t, sim.SoapEnvelope)
}

func TestSimulation_GRPCFields(t *testing.T) {
	sim := domain.Simulation{
		ID:          "sim-grpc",
		Protocol:    "grpc",
		GRPCMessage: `{"id":"42","name":"mock"}`,
		GRPCStatus:  0,
	}
	assert.Equal(t, "grpc", sim.Protocol)
	assert.Equal(t, `{"id":"42","name":"mock"}`, sim.GRPCMessage)
	assert.Equal(t, 0, sim.GRPCStatus)
}
