package pipeline

import "context"

type Stage interface {
	Name() string
	Execute(ctx context.Context, pctx *PipelineContext) error
}
