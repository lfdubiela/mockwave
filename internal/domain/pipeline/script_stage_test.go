package pipeline_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubRunner struct{}

func (s *stubRunner) Run(script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
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
