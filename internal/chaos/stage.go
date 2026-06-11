package chaos

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// FaultStage rolls fault probabilities for the bucket-selected profile and
// either annotates the context (jitter) or short-circuits it (error).
// Runs after the percentile router, before the simulation stage.
type FaultStage struct {
	profiles map[string]domain.FaultProfile
	ks       *KillSwitch
	scenario *ScenarioController
	mu       sync.Mutex
	rng      *rand.Rand
	retry    *retryCounter
}

func NewFaultStage(profiles map[string]domain.FaultProfile, ks *KillSwitch) *FaultStage {
	return NewFaultStageWithScenario(profiles, ks, nil)
}

func NewFaultStageWithScenario(profiles map[string]domain.FaultProfile, ks *KillSwitch, sc *ScenarioController) *FaultStage {
	return &FaultStage{
		profiles: profiles,
		ks:       ks,
		scenario: sc,
		rng:      rand.New(rand.NewSource(rand.Int63())),
		retry:    newRetryCounter(time.Now),
	}
}

func (s *FaultStage) Name() string { return "fault" }

func (s *FaultStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if s.ks.Halted() {
		return nil
	}
	effectiveProfileID := pctx.FaultProfileID
	if s.scenario != nil {
		if phaseProfileID, overridden := s.scenario.Overlay(matchedRuleID(pctx)); overridden {
			effectiveProfileID = phaseProfileID
		}
	}
	if effectiveProfileID == "" {
		return nil
	}
	p, ok := s.profiles[effectiveProfileID]
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
		case domain.FaultSlowBody:
			pctx.SlowBodyBytesPerSec = f.Params.BytesPerSec
			pctx.FaultType = "slowBody"
		case domain.FaultHang:
			pctx.ConnFault = "hang"
			pctx.ConnFaultMaxMs = f.Params.MaxMs
			pctx.FaultShortCircuit = true
			pctx.FaultType = "hang"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
		case domain.FaultReset:
			pctx.ConnFault = "reset"
			pctx.FaultShortCircuit = true
			pctx.FaultType = "reset"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
		case domain.FaultHalfResponse:
			pctx.ConnFault = "halfResponse"
			pctx.ConnFaultFraction = f.Params.Fraction
			pctx.FaultShortCircuit = true
			pctx.FaultType = "halfResponse"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
		case domain.FaultRetryStorm:
			key := retryKey(pctx, f.Params.KeyBy)
			if !s.retry.shouldFail(key, f.Params.FailFirst, f.Params.WindowSec) {
				continue
			}
			pctx.Response = &pipeline.MockResponse{Status: f.Params.StatusCode}
			pctx.FaultShortCircuit = true
			pctx.FaultType = "retryStorm"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
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

// retryKey derives the retry-storm bucket key from the request per the fault's
// key_by setting. Header names are lower-cased to match the adapter's
// normalized header map.
func retryKey(pctx *pipeline.PipelineContext, keyBy string) string {
	if keyBy == "path" {
		return "path:" + pctx.Request.Path
	}
	if name, ok := strings.CutPrefix(keyBy, "header:"); ok {
		return "hdr:" + name + ":" + pctx.Request.Headers[strings.ToLower(name)]
	}
	return "path:" + pctx.Request.Path
}

func matchedRuleID(pctx *pipeline.PipelineContext) string {
	if pctx.Matched == nil {
		return ""
	}
	return pctx.Matched.ID
}
