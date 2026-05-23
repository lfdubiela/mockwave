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
}
