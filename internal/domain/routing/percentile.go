package routing

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type PercentileRouterStage struct {
	mu  sync.Mutex
	rng *rand.Rand
}

func NewPercentileRouterStage() *PercentileRouterStage {
	return &PercentileRouterStage{rng: rand.New(rand.NewSource(rand.Int63()))}
}

func (s *PercentileRouterStage) Name() string { return "percentile-router" }

func (s *PercentileRouterStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if pctx.Matched == nil {
		return fmt.Errorf("percentile-router: no matched rule in context")
	}
	bucket, err := s.selectBucket(pctx.Matched.Buckets)
	if err != nil {
		return fmt.Errorf("percentile-router: %w", err)
	}
	switch bucket.Action {
	case domain.ActionSimulate:
		pctx.SimulationID = bucket.SimulationID
	case domain.ActionForward:
		pctx.ShouldForward = true
		pctx.ForwardDelayMs = bucket.DelayMs
		pctx.ForwardURL = bucket.ForwardURL
	default:
		return fmt.Errorf("percentile-router: unknown action %q", bucket.Action)
	}
	return nil
}

func (s *PercentileRouterStage) selectBucket(buckets []domain.WeightedBucket) (domain.WeightedBucket, error) {
	if len(buckets) == 0 {
		return domain.WeightedBucket{}, fmt.Errorf("rule has no buckets")
	}
	total := 0
	for _, b := range buckets {
		total += b.Weight
	}
	// math/rand.Rand is not safe for concurrent use; the router is shared across
	// all in-flight requests, so guard rng access to keep the weighted draw fair
	// under parallelism.
	s.mu.Lock()
	pick := s.rng.Intn(total)
	s.mu.Unlock()
	cumulative := 0
	for _, b := range buckets {
		cumulative += b.Weight
		if pick < cumulative {
			return b, nil
		}
	}
	return buckets[len(buckets)-1], nil
}
