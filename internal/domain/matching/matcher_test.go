package matching_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
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

func TestConditionMatchStage_Name(t *testing.T) {
	stage := matching.NewConditionMatchStage(nil)
	assert.Equal(t, "condition-match", stage.Name())
}

func TestConditionMatchStage_ProtocolMismatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkRule("r1", "GET", "/foo", nil)})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "grpc", Method: "GET", Path: "/foo"}}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestConditionMatchStage_QueryMatch(t *testing.T) {
	rule := domain.Rule{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/search", Query: map[string]string{"q": "hello"}},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	stage := matching.NewConditionMatchStage([]domain.Rule{rule})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/search", Query: map[string]string{"q": "hello"}}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_QueryMismatch(t *testing.T) {
	rule := domain.Rule{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/search", Query: map[string]string{"q": "hello"}},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	stage := matching.NewConditionMatchStage([]domain.Rule{rule})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/search", Query: map[string]string{"q": "world"}}}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func mkBodyRule(id string, body map[string]string) domain.Rule {
	return domain.Rule{
		ID:      id,
		Match:   domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/orders", Body: body},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
}

func bodyReq(raw string) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "POST", Path: "/orders", Body: []byte(raw),
	}}
}

func TestConditionMatchStage_BodyTopLevel(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`{"status":"paid"}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyNested(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.user.id": "7"})})
	pctx := bodyReq(`{"user":{"id":7}}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyArrayIndex(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.items.0.id": "42"})})
	pctx := bodyReq(`{"items":[{"id":42}]}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyMismatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`{"status":"pending"}`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestConditionMatchStage_BodyMalformedJSON(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`not json`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestConditionMatchStage_BodyNonLeaf(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.user": "x"})})
	pctx := bodyReq(`{"user":{"id":7}}`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func mkMatchRule(id string, m domain.MatchCriteria) domain.Rule {
	return domain.Rule{ID: id, Match: m, Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}}}
}

func TestPrecedence_HeaderBeatsExactPath(t *testing.T) {
	wild := mkMatchRule("wild", domain.MatchCriteria{Protocol: "http", Path: "/*", Headers: map[string]string{"x-cid": "1"}})
	exact := mkMatchRule("exact", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{exact, wild})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders", Headers: map[string]string{"x-cid": "1"},
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "wild", pctx.Matched.ID)
}

func TestPrecedence_ExactBeatsWildcard(t *testing.T) {
	wild := mkMatchRule("wild", domain.MatchCriteria{Protocol: "http", Path: "/*"})
	exact := mkMatchRule("exact", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{wild, exact})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/orders"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "exact", pctx.Matched.ID)
}

func TestPrecedence_DeeperWildcardBeatsRootWildcard(t *testing.T) {
	root := mkMatchRule("root", domain.MatchCriteria{Protocol: "http", Path: "/*"})
	deep := mkMatchRule("deep", domain.MatchCriteria{Protocol: "http", Path: "/api/v1/*"})
	stage := matching.NewConditionMatchStage([]domain.Rule{root, deep})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/api/v1/x"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "deep", pctx.Matched.ID)
}

func TestPrecedence_BodyBeatsQuery(t *testing.T) {
	q := mkMatchRule("q", domain.MatchCriteria{Protocol: "http", Path: "/orders", Query: map[string]string{"a": "1"}})
	b := mkMatchRule("b", domain.MatchCriteria{Protocol: "http", Path: "/orders", Body: map[string]string{"$.k": "v"}})
	stage := matching.NewConditionMatchStage([]domain.Rule{q, b})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders",
		Query: map[string]string{"a": "1"}, Body: []byte(`{"k":"v"}`),
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "b", pctx.Matched.ID)
}

func TestPrecedence_StableWhenEqual(t *testing.T) {
	r1 := mkMatchRule("first", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	r2 := mkMatchRule("second", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{r1, r2})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/orders"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "first", pctx.Matched.ID)
}

func TestConditionMatchStage_DisabledRuleSkipped(t *testing.T) {
	disabled := mkRule("r1", "GET", "/users/42", nil)
	disabled.Disabled = true
	stage := matching.NewConditionMatchStage([]domain.Rule{disabled})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/users/42"}}
	require.Error(t, stage.Execute(context.Background(), pctx), "disabled rule must not match")
	assert.Nil(t, pctx.Matched)
}

func TestConditionMatchStage_DisabledRuleFallsThroughToNext(t *testing.T) {
	disabled := mkRule("r1", "GET", "/users/42", nil)
	disabled.Disabled = true
	enabled := mkRule("r2", "GET", "/users/42", nil)
	stage := matching.NewConditionMatchStage([]domain.Rule{disabled, enabled})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/users/42"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r2", pctx.Matched.ID)
}
