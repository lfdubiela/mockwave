package pipeline

import (
	"context"
	"fmt"
)

type Pipeline struct {
	stages []Stage
}

func New(stages ...Stage) *Pipeline {
	return &Pipeline{stages: stages}
}

func (p *Pipeline) Execute(ctx context.Context, pctx *PipelineContext) error {
	for _, s := range p.stages {
		if err := s.Execute(ctx, pctx); err != nil {
			return fmt.Errorf("stage %q: %w", s.Name(), err)
		}
	}
	return nil
}
