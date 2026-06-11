package chaos

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// FaultStage rolls fault probabilities for the bucket-selected profile and
// either annotates the context (jitter) or short-circuits it (error).
// Runs after the percentile router, before the simulation stage.
type FaultStage struct {
	profiles map[string]domain.FaultProfile
	ks       *KillSwitch
	mu       sync.Mutex
	rng      *rand.Rand
}

func NewFaultStage(profiles map[string]domain.FaultProfile, ks *KillSwitch) *FaultStage {
	return &FaultStage{
		profiles: profiles,
		ks:       ks,
		rng:      rand.New(rand.NewSource(rand.Int63())),
	}
}

func (s *FaultStage) Name() string { return "fault" }

func (s *FaultStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if pctx.FaultProfileID == "" || s.ks.Halted() {
		return nil
	}
	p, ok := s.profiles[pctx.FaultProfileID]
	if !ok || !p.Enabled {
		return nil
	}
	for _, f := range p.Faults {
		if !s.roll(f.Probability) {
			continue
		}
		switch f.Type {
		case domain.FaultJitter:
			d := f.Params.BaseDelayMs
			if f.Params.JitterMs > 0 {
				d += s.intn(f.Params.JitterMs)
			}
			pctx.FaultDelayMs += d
			pctx.FaultType = "jitter"
		case domain.FaultError:
			pctx.Response = &pipeline.MockResponse{
				Status:  f.Params.StatusCode,
				Headers: f.Params.Headers,
				Body:    parseBody(f.Params.Body),
			}
			pctx.FaultShortCircuit = true
			pctx.FaultType = "error"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil // first terminal fault wins
		}
	}
	return nil
}

func (s *FaultStage) roll(p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	s.mu.Lock()
	v := s.rng.Float64()
	s.mu.Unlock()
	return v < p
}

func (s *FaultStage) intn(n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Intn(n)
}

// parseBody returns the configured body as parsed JSON when it is valid JSON,
// otherwise the raw string; nil for empty.
func parseBody(b string) interface{} {
	if b == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(b), &v); err == nil {
		return v
	}
	return b
}
