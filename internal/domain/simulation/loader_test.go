package simulation_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/domain/simulation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubStore struct{ sims map[string]domain.Simulation }

func (s *stubStore) GetRules() ([]domain.Rule, error) { return nil, nil }
func (s *stubStore) GetSimulation(id string) (*domain.Simulation, error) {
	sim, ok := s.sims[id]
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	return &sim, nil
}
func (s *stubStore) SaveRule(r domain.Rule) error              { return nil }
func (s *stubStore) SaveSimulation(s2 domain.Simulation) error { return nil }
func (s *stubStore) DeleteRule(id string) error                { return nil }
func (s *stubStore) DeleteSimulation(id string) error          { return nil }

func newStore(sims ...domain.Simulation) *stubStore {
	m := make(map[string]domain.Simulation)
	for _, s := range sims {
		m[s.ID] = s
	}
	return &stubStore{sims: m}
}

func TestSimulationStage_LoadsResponse(t *testing.T) {
	sim := domain.Simulation{ID: "sim1", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"ok": true}}}
	stage := simulation.NewSimulationStage(newStore(sim))
	pctx := &pipeline.PipelineContext{SimulationID: "sim1"}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 200, pctx.Response.Status)
}

func TestSimulationStage_SkippedWhenForward(t *testing.T) {
	stage := simulation.NewSimulationStage(newStore())
	pctx := &pipeline.PipelineContext{ShouldForward: true}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Nil(t, pctx.Response)
}

func TestSimulationStage_SimNotFound(t *testing.T) {
	stage := simulation.NewSimulationStage(newStore())
	pctx := &pipeline.PipelineContext{SimulationID: "missing"}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestSimulationStage_Name(t *testing.T) {
	stage := simulation.NewSimulationStage(newStore())
	assert.Equal(t, "simulation", stage.Name())
}

func TestSimulationStage_PathTemplateRendering(t *testing.T) {
	sim := domain.Simulation{
		ID:       "sim-template",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"id": "{{request.path[1]}}"}},
	}
	stage := simulation.NewSimulationStage(newStore(sim))
	pctx := &pipeline.PipelineContext{
		SimulationID: "sim-template",
		Request:      pipeline.NormalizedRequest{PathSegs: []string{"users", "42"}},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	body, ok := pctx.Response.Body.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "42", body["id"])
}

func TestSimulationStage_PathTemplateOutOfBounds(t *testing.T) {
	sim := domain.Simulation{
		ID:       "sim-oob",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"id": "{{request.path[5]}}"}},
	}
	stage := simulation.NewSimulationStage(newStore(sim))
	pctx := &pipeline.PipelineContext{
		SimulationID: "sim-oob",
		Request:      pipeline.NormalizedRequest{PathSegs: []string{"users", "42"}},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	body, ok := pctx.Response.Body.(map[string]interface{})
	require.True(t, ok)
	// Template not replaced, raw template left in (or original value preserved)
	assert.NotNil(t, body["id"])
}

func TestSimulationStage_NilBody(t *testing.T) {
	sim := domain.Simulation{
		ID:       "sim-nil-body",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 204, Body: nil},
	}
	stage := simulation.NewSimulationStage(newStore(sim))
	pctx := &pipeline.PipelineContext{SimulationID: "sim-nil-body"}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	require.NotNil(t, pctx.Response)
	assert.Nil(t, pctx.Response.Body)
}

func TestSimulationStage_ResponseHeaders(t *testing.T) {
	sim := domain.Simulation{
		ID:       "sim-headers",
		Protocol: "http",
		Response: domain.HTTPResponse{Status: 200, Headers: map[string]string{"x-custom": "val"}, Body: nil},
	}
	stage := simulation.NewSimulationStage(newStore(sim))
	pctx := &pipeline.PipelineContext{SimulationID: "sim-headers"}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "val", pctx.Response.Headers["x-custom"])
}

func TestSimulationStage_SOAPProtocol(t *testing.T) {
	envelope := `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>42</id></GetUserResponse></soap:Body></soap:Envelope>`
	store := &stubStore{
		sims: map[string]domain.Simulation{
			"sim-soap": {
				ID:           "sim-soap",
				Protocol:     "soap",
				SOAPEnvelope: envelope,
				Response:     domain.HTTPResponse{DelayMs: 0},
			},
		},
	}
	stage := simulation.NewSimulationStage(store)
	pctx := &pipeline.PipelineContext{
		Request:      pipeline.NormalizedRequest{Protocol: "soap"},
		SimulationID: "sim-soap",
	}
	err := stage.Execute(context.Background(), pctx)
	require.NoError(t, err)
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 200, pctx.Response.Status)
	assert.Equal(t, envelope, pctx.Response.Body)
	assert.Equal(t, "text/xml; charset=utf-8", pctx.Response.Headers["content-type"])
}

func TestSimulationStage_GRPCProtocol(t *testing.T) {
	store := &stubStore{
		sims: map[string]domain.Simulation{
			"sim-grpc": {
				ID:          "sim-grpc",
				Protocol:    "grpc",
				GRPCMessage: `{"id":"42"}`,
				GRPCStatus:  0,
				Response:    domain.HTTPResponse{DelayMs: 50},
			},
		},
	}
	stage := simulation.NewSimulationStage(store)
	pctx := &pipeline.PipelineContext{
		Request:      pipeline.NormalizedRequest{Protocol: "grpc"},
		SimulationID: "sim-grpc",
	}
	err := stage.Execute(context.Background(), pctx)
	require.NoError(t, err)
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 0, pctx.Response.Status)
	assert.Equal(t, `{"id":"42"}`, pctx.Response.Body)
	assert.Equal(t, 50, pctx.Response.DelayMs)
}
