// Package server is the public entry point for embedding Mockwave in a Go program.
// Configure with a store backend and optional observability implementations;
// all observability fields default to no-op when omitted.
//
// Example:
//
//	srv, _ := server.New(server.Config{
//	    Store:   myStore,
//	    Logger:  myLogger,   // observability.Logger
//	    Tracer:  myTracer,   // observability.Tracer
//	    Metrics: myRecorder, // observability.MetricsRecorder
//	})
//	http.ListenAndServe(":8080", srv.MockHandler([]string{"http"}, srv.NewProxy()))
package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mockwave/mockwave/domain"
	graphqladapter "github.com/mockwave/mockwave/internal/adapters/in/graphql"
	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	soapadapter "github.com/mockwave/mockwave/internal/adapters/in/soap"
	"github.com/mockwave/mockwave/internal/chaos"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/domain/routing"
	"github.com/mockwave/mockwave/internal/domain/simulation"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/reload"
	"github.com/mockwave/mockwave/internal/scripting"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
	"github.com/mockwave/mockwave/store"
	googlegrpc "google.golang.org/grpc"
)

// Config holds all options for creating a Server.
// Store is optional: when nil, server.New reads MOCKWAVE_STORE from the environment
// to select a backend. Observability fields default to no-op when nil.
type Config struct {
	MockPort  int
	AdminPort int
	Store     store.DataStore

	// Logger receives structured log entries. Defaults to NoopLogger when nil.
	Logger observability.Logger
	// Tracer creates spans for distributed tracing. Defaults to NoopTracer when nil.
	Tracer observability.Tracer
	// Metrics records per-request metrics. Defaults to NoopMetrics when nil.
	Metrics observability.MetricsRecorder

	// ReloadInterval controls the version-poll reloader cadence for remote
	// (store.VersionedStore) backends. 0 resolves to 15s. Ignored for stores that
	// do not implement store.VersionedStore (e.g. the JSON store).
	ReloadInterval time.Duration

	// ImportExport enables the admin /api/export and /api/import endpoints.
	// Set true for remote store backends; leave false for the json-file store,
	// whose config file is already the import/export format.
	ImportExport bool
}

func (c Config) reloadInterval() time.Duration {
	if c.ReloadInterval <= 0 {
		return 15 * time.Second
	}
	return c.ReloadInterval
}

// Server holds the active pipeline and serves mock traffic across protocols.
type Server struct {
	cfg          Config
	mu           sync.RWMutex
	pipeline     *pipeline.Pipeline
	engine       *scripting.Engine
	collector    *metrics.Collector
	buffer       *unmatched.Buffer
	broker       *metrics.Broker
	brokerCancel context.CancelFunc
	reloadCancel context.CancelFunc
	killSwitch   *chaos.KillSwitch
	adminSrv     *http.Server
}

// New creates a Server from cfg, loading rules from the store.
// If Store is nil, New falls back to building a store from environment variables
// (see buildStoreFromEnv). Returns an error if store construction or rule loading fails.
// Nil observability fields are replaced with no-op defaults.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		s, err := buildStoreFromEnv()
		if err != nil {
			return nil, err
		}
		cfg.Store = s
	}
	if cfg.Logger == nil {
		cfg.Logger = observability.NoopLogger{}
	}
	if cfg.Tracer == nil {
		cfg.Tracer = observability.NoopTracer{}
	}
	if cfg.Metrics == nil {
		cfg.Metrics = observability.NoopMetrics{}
	}
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(100)
	broker := metrics.NewBroker(col)
	brokerCtx, brokerCancel := context.WithCancel(context.Background())
	go broker.Start(brokerCtx)

	s := &Server{
		cfg:          cfg,
		engine:       scripting.NewEngine(),
		collector:    col,
		buffer:       buf,
		broker:       broker,
		brokerCancel: brokerCancel,
		killSwitch:   chaos.NewKillSwitch(),
	}
	if err := s.rebuild(); err != nil {
		brokerCancel()
		return nil, err
	}
	if vs, ok := s.cfg.Store.(store.VersionedStore); ok {
		if s.cfg.ReloadInterval == 0 {
			s.cfg.ReloadInterval = reloadIntervalFromEnv()
		}
		rl := reload.New(vs, s.cfg.reloadInterval(), s.Rebuild, s.cfg.Logger)
		rctx, rcancel := context.WithCancel(context.Background())
		s.reloadCancel = rcancel
		go rl.Run(rctx)
	}
	if cfg.AdminPort > 0 {
		if err := s.startAdmin(); err != nil {
			brokerCancel()
			return nil, err
		}
	}
	return s, nil
}

// Rebuild reloads rules from the store and hot-swaps the active pipeline.
func (s *Server) Rebuild() error {
	return s.rebuild()
}

// Executor is the pipeline entry point. Any type that wraps the server's
// pipeline (e.g. a metrics middleware) must implement this interface.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// NewProxy returns an Executor backed by this server's active pipeline,
// pre-wrapped with metrics middleware. Every request through MockHandler
// or GRPCServer automatically feeds the admin dashboard.
func (s *Server) NewProxy() Executor {
	return metrics.NewMiddleware(
		&pipelineProxy{server: s},
		s.collector,
		s.buffer,
		s.cfg.Tracer,
		s.cfg.Metrics,
	)
}

// Logger returns the Logger configured for this server (never nil).
func (s *Server) Logger() observability.Logger { return s.cfg.Logger }

// Tracer returns the Tracer configured for this server (never nil).
func (s *Server) Tracer() observability.Tracer { return s.cfg.Tracer }

// MetricsRecorder returns the MetricsRecorder configured for this server (never nil).
func (s *Server) MetricsRecorder() observability.MetricsRecorder { return s.cfg.Metrics }

// KillSwitch returns the global chaos kill switch for this server.
func (s *Server) KillSwitch() *chaos.KillSwitch { return s.killSwitch }

func (s *Server) rebuild() error {
	rules, err := s.cfg.Store.GetRules()
	if err != nil {
		return fmt.Errorf("server: load rules: %w", err)
	}
	simList, err := s.cfg.Store.ListSimulations()
	if err != nil {
		return fmt.Errorf("server: load simulations: %w", err)
	}
	simMap := make(map[string]domain.Simulation, len(simList))
	for _, sim := range simList {
		simMap[sim.ID] = sim
	}
	matchStage := matching.NewConditionMatchStage(rules)
	routeStage := routing.NewPercentileRouterStage()
	simStage := simulation.NewSimulationStage(simMap)
	scriptStage := pipeline.NewScriptStage(s.engine, func(pctx *pipeline.PipelineContext) string {
		if pctx.SimulationID == "" {
			return ""
		}
		return simMap[pctx.SimulationID].Script
	})
	fwdStage := httprest.NewForwardStage(nil)
	profMap := map[string]domain.FaultProfile{}
	if fs, ok := s.cfg.Store.(store.FaultStore); ok {
		profiles, err := fs.ListFaultProfiles()
		if err != nil {
			return fmt.Errorf("server: load fault profiles: %w", err)
		}
		for _, p := range profiles {
			profMap[p.ID] = p
		}
	} else {
		for _, r := range rules {
			referenced := false
			for _, b := range r.Buckets {
				if b.FaultProfileID != "" {
					referenced = true
					break
				}
			}
			if referenced {
				s.cfg.Logger.Warn(context.Background(),
					"rules reference fault profiles but store does not support them",
					observability.F("rule_id", r.ID))
			}
		}
	}
	faultStage := chaos.NewFaultStage(profMap, s.killSwitch)
	p := pipeline.New(matchStage, routeStage, faultStage, simStage, scriptStage, fwdStage)
	s.mu.Lock()
	s.pipeline = p
	s.mu.Unlock()
	return nil
}

// HTTPHandler returns an http.Handler backed directly by this server's pipeline.
// Unlike MockHandler, this method does not accept an Executor and cannot be
// wrapped with observability middleware. Prefer MockHandler for production use
// when you need tracing, metrics, or logging on every request.
func (s *Server) HTTPHandler() *httprest.Handler {
	return httprest.NewHandler(&pipelineProxy{server: s})
}

// MockHandler returns an http.Handler routing through exec to the appropriate
// protocol handler. "http" is always active; "graphql" and "soap" share the
// HTTP port. Pass srv.NewProxy() (optionally wrapped with middleware) as exec.
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

type protocolMux struct {
	httpH *httprest.Handler
	gqlH  *graphqladapter.Handler
	soapH *soapadapter.Handler
}

func (m *protocolMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if m.soapH != nil {
		if r.Header.Get("SOAPAction") != "" ||
			strings.HasPrefix(ct, "text/xml") ||
			strings.HasPrefix(ct, "application/soap+xml") {
			m.soapH.ServeHTTP(w, r)
			return
		}
	}
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
