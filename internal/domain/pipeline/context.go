package pipeline

import "github.com/mockwave/mockwave/domain"

type NormalizedRequest struct {
	Protocol string
	Method   string
	Path     string
	Headers  map[string]string
	Query    map[string]string
	RawQuery string // original percent-encoded query string, forwarded verbatim upstream
	Body     []byte
	PathSegs []string
}

type MockResponse struct {
	Status  int
	Headers map[string]string
	Body    interface{}
	DelayMs int
}

type PipelineContext struct {
	Request        NormalizedRequest
	Response       *MockResponse
	Matched        *domain.Rule
	SimulationID   string
	ShouldForward  bool
	ForwardDelayMs int    // delay (ms) for the selected forward bucket, applied concurrently in the forward stage
	ForwardURL     string // upstream base URL of the selected forward bucket
	// FaultProfileID is the chaos profile selected by the router for this
	// request's bucket ("" when the bucket has none).
	FaultProfileID string
	// FaultShortCircuit is set by the fault stage when a terminal fault fired:
	// later stages (simulation, script, forward) must skip processing.
	FaultShortCircuit bool
	// FaultDelayMs is extra latency injected by a jitter fault; protocol
	// adapters apply it together with Response.DelayMs.
	FaultDelayMs int
}
