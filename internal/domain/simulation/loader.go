package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/store"
)

var templateRe = regexp.MustCompile(`\{\{request\.path\[(\d+)\]\}\}`)

type SimulationStage struct {
	store store.DataStore
}

func NewSimulationStage(store store.DataStore) *SimulationStage {
	return &SimulationStage{store: store}
}

func (s *SimulationStage) Name() string { return "simulation" }

func (s *SimulationStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if pctx.ShouldForward {
		return nil
	}
	sim, err := s.store.GetSimulation(pctx.SimulationID)
	if err != nil {
		return fmt.Errorf("simulation: %w", err)
	}

	switch sim.Protocol {
	case "soap":
		status := sim.Response.Status
		if status == 0 {
			status = 200
		}
		pctx.Response = &pipeline.MockResponse{
			Status:  status,
			Headers: map[string]string{"content-type": "text/xml; charset=utf-8"},
			Body:    sim.SOAPEnvelope,
			DelayMs: sim.Response.DelayMs,
		}
	case "grpc":
		pctx.Response = &pipeline.MockResponse{
			Status:  sim.GRPCStatus,
			Headers: map[string]string{},
			Body:    sim.GRPCMessage,
			DelayMs: sim.Response.DelayMs,
		}
	default: // "http", "graphql", ""
		body, err := renderBody(sim.Response.Body, pctx.Request)
		if err != nil {
			return fmt.Errorf("simulation: render body: %w", err)
		}
		headers := make(map[string]string)
		for k, v := range sim.Response.Headers {
			headers[k] = v
		}
		pctx.Response = &pipeline.MockResponse{
			Status:  sim.Response.Status,
			Headers: headers,
			Body:    body,
			DelayMs: sim.Response.DelayMs,
		}
	}
	return nil
}

func renderBody(body interface{}, req pipeline.NormalizedRequest) (interface{}, error) {
	if body == nil {
		return nil, nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	replaced := templateRe.ReplaceAllStringFunc(string(raw), func(match string) string {
		sub := templateRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx, err := strconv.Atoi(sub[1])
		if err != nil || idx >= len(req.PathSegs) {
			return match
		}
		// Template lives inside a JSON string value: "{{request.path[1]}}"
		// Replace only the template token, keep surrounding JSON quotes intact.
		return strings.ReplaceAll(req.PathSegs[idx], `"`, `\"`)
	})
	var out interface{}
	if err := json.Unmarshal([]byte(replaced), &out); err != nil {
		return nil, err
	}
	return out, nil
}
