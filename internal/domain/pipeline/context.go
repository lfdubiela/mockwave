package pipeline

import "github.com/mockwave/mockwave/domain"

type NormalizedRequest struct {
	Protocol string
	Method   string
	Path     string
	Headers  map[string]string
	Query    map[string]string
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
	Request       NormalizedRequest
	Response      *MockResponse
	Matched       *domain.Rule
	SimulationID  string
	ShouldForward bool
}
