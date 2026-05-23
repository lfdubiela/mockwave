package matching_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkRule(id, method, pathStr string, headers map[string]string) domain.Rule {
	return domain.Rule{
		ID:      id,
		Match:   domain.MatchCriteria{Protocol: "http", Method: method, Path: pathStr, Headers: headers},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
}

func TestConditionMatchStage_ExactPath(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkRule("r1", "GET", "/users/42", nil)})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/users/42"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_GlobPath(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkRule("r1", "GET", "/users/*", nil)})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/users/99"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_HeaderMatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{
		mkRule("generic", "GET", "/users/*", nil),
		mkRule("specific", "GET", "/users/*", map[string]string{"x-cid": "12345"}),
	})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/users/1",
		Headers: map[string]string{"x-cid": "12345"},
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "specific", pctx.Matched.ID)
}

func TestConditionMatchStage_NoMatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkRule("r1", "POST", "/orders", nil)})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/users/1"}}
	err := stage.Execute(context.Background(), pctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no rule matched")
}

func TestConditionMatchStage_MethodMismatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkRule("r1", "GET", "/foo", nil)})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "DELETE", Path: "/foo"}}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}
