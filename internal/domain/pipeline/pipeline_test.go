package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingStage struct {
	name     string
	calls    int
	failWith error
}

func (s *countingStage) Name() string { return s.name }
func (s *countingStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	s.calls++
	if s.failWith != nil {
		return s.failWith
	}
	return nil
}

func TestPipeline_Execute_AllStagesRun(t *testing.T) {
	s1, s2, s3 := &countingStage{name: "s1"}, &countingStage{name: "s2"}, &countingStage{name: "s3"}
	p := pipeline.New(s1, s2, s3)
	err := p.Execute(context.Background(), &pipeline.PipelineContext{})
	require.NoError(t, err)
	assert.Equal(t, 1, s1.calls)
	assert.Equal(t, 1, s2.calls)
	assert.Equal(t, 1, s3.calls)
}

func TestPipeline_Execute_HaltsOnError(t *testing.T) {
	s1 := &countingStage{name: "s1"}
	s2 := &countingStage{name: "s2", failWith: errors.New("stage failed")}
	s3 := &countingStage{name: "s3"}
	p := pipeline.New(s1, s2, s3)
	err := p.Execute(context.Background(), &pipeline.PipelineContext{})
	assert.Error(t, err)
	assert.Equal(t, 1, s1.calls)
	assert.Equal(t, 1, s2.calls)
	assert.Equal(t, 0, s3.calls, "s3 must not run after s2 fails")
}

func TestPipelineContext_ShouldForward(t *testing.T) {
	pctx := &pipeline.PipelineContext{}
	assert.False(t, pctx.ShouldForward)
	pctx.Matched = &domain.Rule{ID: "r1"}
	pctx.ShouldForward = true
	assert.True(t, pctx.ShouldForward)
	assert.Equal(t, "r1", pctx.Matched.ID)
}
