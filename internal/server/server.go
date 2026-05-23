package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	graphqladapter "github.com/mockwave/mockwave/internal/adapters/in/graphql"
	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	soapadapter "github.com/mockwave/mockwave/internal/adapters/in/soap"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/store"
	"github.com/mockwave/mockwave/internal/domain/routing"
	"github.com/mockwave/mockwave/internal/domain/simulation"
	"github.com/mockwave/mockwave/internal/scripting"
	googlegrpc "google.golang.org/grpc"
)

type Config struct {
	MockPort  int
	AdminPort int
	Store     store.DataStore
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

// Executor is the pipeline entry point. Any type that wraps the server's
// pipeline (e.g. a metrics middleware) must implement this interface.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// NewProxy returns an Executor backed by this server's active pipeline.
// Wrap it with middleware before passing to MockHandler or GRPCServer.
func (s *Server) NewProxy() Executor {
	return &pipelineProxy{server: s}
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

// MockHandler returns an http.Handler that routes requests through exec to the
// appropriate protocol handler. "http" is always active; "graphql" and "soap"
// share the HTTP port and are activated by the protocols list.
func (s *Server) MockHandler(protocols []string, exec Executor) http.Handler {
	httpH := httprest.NewHandler(exec)

	var gqlH *graphqladapter.Handler
	var soapH *soapadapter.Handler
	for _, p := range protocols {
		switch strings.ToLower(p) {
		case "graphql":
			gqlH = graphqladapter.NewHandler(exec)
		case "soap":
			soapH = soapadapter.NewHandler(exec)
		}
	}

	return &protocolMux{httpH: httpH, gqlH: gqlH, soapH: soapH}
}

// GRPCServer returns a configured *grpc.Server backed by exec.
// registry may be nil — proto JSON conversion is skipped when nil.
func (s *Server) GRPCServer(registry *grpcadapter.FileRegistry, exec Executor) *googlegrpc.Server {
	h := grpcadapter.NewHandler(exec, registry)
	return h.NewGRPCServer()
}

// protocolMux routes incoming HTTP requests to the correct protocol handler.
type protocolMux struct {
	httpH *httprest.Handler
	gqlH  *graphqladapter.Handler // nil if graphql not enabled
	soapH *soapadapter.Handler    // nil if soap not enabled
}

func (m *protocolMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	// SOAP detection: SOAPAction header or text/xml / application/soap+xml content-type
	if m.soapH != nil {
		if r.Header.Get("SOAPAction") != "" ||
			strings.HasPrefix(ct, "text/xml") ||
			strings.HasPrefix(ct, "application/soap+xml") {
			m.soapH.ServeHTTP(w, r)
			return
		}
	}
	// GraphQL detection: /graphql path
	if m.gqlH != nil && r.URL.Path == "/graphql" {
		m.gqlH.ServeHTTP(w, r)
		return
	}
	m.httpH.ServeHTTP(w, r)
}

type pipelineProxy struct{ server *Server }

func (a *pipelineProxy) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	a.server.mu.RLock()
	p := a.server.pipeline
	a.server.mu.RUnlock()
	return p.Execute(ctx, pctx)
}
