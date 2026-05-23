package routing_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/domain/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPercentileRouterStage_SingleSimulateBucket(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	rule := &domain.Rule{Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-ok"}}}
	pctx := &pipeline.PipelineContext{Matched: rule}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "sim-ok", pctx.SimulationID)
	assert.False(t, pctx.ShouldForward)
}

func TestPercentileRouterStage_SingleForwardBucket(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	rule := &domain.Rule{Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionForward}}}
	pctx := &pipeline.PipelineContext{Matched: rule}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.True(t, pctx.ShouldForward)
	assert.Empty(t, pctx.SimulationID)
}

func TestPercentileRouterStage_WeightDistribution(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	rule := &domain.Rule{Buckets: []domain.WeightedBucket{
		{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-error"},
		{Weight: 99, Action: domain.ActionForward},
	}}
	simCount, fwdCount := 0, 0
	for i := 0; i < 10000; i++ {
		pctx := &pipeline.PipelineContext{Matched: rule}
		require.NoError(t, stage.Execute(context.Background(), pctx))
		if pctx.ShouldForward {
			fwdCount++
		} else {
			simCount++
		}
	}
	assert.InDelta(t, 100, simCount, 300, "expected ~1%% simulate")
	assert.InDelta(t, 9900, fwdCount, 300, "expected ~99%% forward")
}

func TestPercentileRouterStage_NilMatched(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	assert.Error(t, stage.Execute(context.Background(), &pipeline.PipelineContext{Matched: nil}))
}

func TestPercentileRouterStage_Name(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	assert.Equal(t, "percentile-router", stage.Name())
}

func TestPercentileRouterStage_EmptyBuckets(t *testing.T) {
	stage := routing.NewPercentileRouterStage()
	rule := &domain.Rule{Buckets: []domain.WeightedBucket{}}
	pctx := &pipeline.PipelineContext{Matched: rule}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}
