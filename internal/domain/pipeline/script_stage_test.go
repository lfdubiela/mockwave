package pipeline_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubRunner struct{}

func (s *stubRunner) Run(_ context.Context, script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
	if body, ok := resp["body"].(map[string]interface{}); ok {
		body["ran"] = true
		resp["body"] = body
	}
	return resp, nil
}

func TestScriptStage_RunsScript(t *testing.T) {
	stage := pipeline.NewScriptStage(&stubRunner{}, func(pctx *pipeline.PipelineContext) string { return "some script" })
	pctx := &pipeline.PipelineContext{
		Response: &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"key": "val"}},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	body := pctx.Response.Body.(map[string]interface{})
	assert.Equal(t, true, body["ran"])
}

func TestScriptStage_SkippedWhenForward(t *testing.T) {
	stage := pipeline.NewScriptStage(&stubRunner{}, func(_ *pipeline.PipelineContext) string { return "script" })
	pctx := &pipeline.PipelineContext{ShouldForward: true}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Nil(t, pctx.Response)
}

func TestScriptStage_SkippedWhenNoResponse(t *testing.T) {
	stage := pipeline.NewScriptStage(&stubRunner{}, func(_ *pipeline.PipelineContext) string { return "script" })
	pctx := &pipeline.PipelineContext{Response: nil}
	require.NoError(t, stage.Execute(context.Background(), pctx))
}

func TestScriptStage_Name(t *testing.T) {
	stage := pipeline.NewScriptStage(&stubRunner{}, func(_ *pipeline.PipelineContext) string { return "" })
	assert.Equal(t, "script", stage.Name())
}

func TestScriptStage_EmptyScriptSkipped(t *testing.T) {
	stage := pipeline.NewScriptStage(&stubRunner{}, func(_ *pipeline.PipelineContext) string { return "" })
	pctx := &pipeline.PipelineContext{
		Response: &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"key": "val"}},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	// body should not have "ran" set since script was empty
	body := pctx.Response.Body.(map[string]interface{})
	_, hasRan := body["ran"]
	assert.False(t, hasRan)
}

type statusModifyRunner struct{}

func (s *statusModifyRunner) Run(_ context.Context, script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
	// Return status as int64, delay_ms as int, to exercise all toInt branches
	resp["status"] = int64(201)
	resp["delay_ms"] = 50
	return resp, nil
}

func TestScriptStage_ModifiesStatusAndDelay(t *testing.T) {
	stage := pipeline.NewScriptStage(&statusModifyRunner{}, func(_ *pipeline.PipelineContext) string { return "modify" })
	pctx := &pipeline.PipelineContext{
		Response: &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"x": 1}, DelayMs: 0},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, 201, pctx.Response.Status)
	assert.Equal(t, 50, pctx.Response.DelayMs)
}

type errorRunner struct{}

func (s *errorRunner) Run(_ context.Context, script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("script error")
}

func TestScriptStage_RunnerError(t *testing.T) {
	stage := pipeline.NewScriptStage(&errorRunner{}, func(_ *pipeline.PipelineContext) string { return "script" })
	pctx := &pipeline.PipelineContext{
		Response: &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{}},
	}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}
