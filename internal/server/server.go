package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/domain/ports"
	"github.com/mockwave/mockwave/internal/domain/routing"
	"github.com/mockwave/mockwave/internal/domain/simulation"
	"github.com/mockwave/mockwave/internal/scripting"
)

type Config struct {
	MockPort  int
	AdminPort int
	Store     ports.DataStore
}

type Server struct {
	cfg      Config
	mu       sync.RWMutex
	pipeline *pipeline.Pipeline
	engine   *scripting.Engine
}

func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("server: store is required")
	}
	s := &Server{cfg: cfg, engine: scripting.NewEngine()}
	if err := s.rebuild(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) Rebuild() error {
	return s.rebuild()
}

func (s *Server) rebuild() error {
	rules, err := s.cfg.Store.GetRules()
	if err != nil {
		return fmt.Errorf("server: load rules: %w", err)
	}
	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(s.cfg.Store)
	scriptStage := pipeline.NewScriptStage(s.engine, func(pctx *pipeline.PipelineContext) string {
		if pctx.SimulationID == "" {
			return ""
		}
		sim, err := s.cfg.Store.GetSimulation(pctx.SimulationID)
		if err != nil || sim == nil {
			return ""
		}
		return sim.Script
	})
	fwdStage := httprest.NewForwardStage(nil)
	p := pipeline.New(matchStage, routeStage, simStage, scriptStage, fwdStage)
	s.mu.Lock()
	s.pipeline = p
	s.mu.Unlock()
	return nil
}

func (s *Server) HTTPHandler() *httprest.Handler {
	return httprest.NewHandler(&pipelineProxy{server: s})
}

type pipelineProxy struct{ server *Server }

func (a *pipelineProxy) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	a.server.mu.RLock()
	p := a.server.pipeline
	a.server.mu.RUnlock()
	return p.Execute(ctx, pctx)
}
