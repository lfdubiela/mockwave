package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/mockwave/mockwave/domain"
	restapi "github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/store"
)

// scenarioControlAdapter bridges the restapi.ScenarioControl seam to the
// server's scenario lifecycle, avoiding a restapi → server import cycle.
type scenarioControlAdapter struct{ s *Server }

func (a scenarioControlAdapter) Start(id string) error {
	sc, err := a.s.scenarioByID(id)
	if err != nil {
		return err
	}
	if startErr := a.s.StartScenario(*sc); startErr != nil {
		return restapi.ErrScenarioRunning
	}
	return nil
}

func (a scenarioControlAdapter) Stop() { a.s.StopScenario() }

func (a scenarioControlAdapter) ActiveStatus() any {
	run := a.s.scenario.Active()
	if run == nil {
		return nil
	}
	return map[string]any{
		"scenario_id": run.ScenarioID, "scenario_name": run.ScenarioName,
		"phase_index": run.PhaseIndex, "phase_count": run.PhaseCount,
		"phase_profile_id": run.PhaseProfileID,
	}
}

// scenarioByID loads a scenario from the store, returning
// restapi.ErrScenarioNotFound when missing or when the store lacks the
// ScenarioStore capability.
func (s *Server) scenarioByID(id string) (*domain.Scenario, error) {
	ss, ok := s.cfg.Store.(store.ScenarioStore)
	if !ok {
		return nil, restapi.ErrScenarioNotFound
	}
	sc, err := ss.GetScenario(id)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		return nil, restapi.ErrScenarioNotFound
	}
	return sc, nil
}

// adminMuxOptions returns the MuxOption slice for the admin mux, including
// WithMatched when matched-request capture is enabled.
func (s *Server) adminMuxOptions() []restapi.MuxOption {
	var opts []restapi.MuxOption
	if s.cfg.ImportExport {
		opts = append(opts, restapi.WithImportExport())
	}
	opts = append(opts, restapi.WithKillSwitch(s.killSwitch))
	opts = append(opts, restapi.WithScenarioControl(scenarioControlAdapter{s}))
	if s.matchedBuf != nil {
		opts = append(opts, restapi.WithMatched(s.matchedBuf))
	}
	if s.eventCaptureBuf != nil {
		opts = append(opts, restapi.WithEventCapture(s.eventCaptureBuf))
	}
	return opts
}

// AdminMux builds the admin HTTP mux. Exported primarily for tests; the live
// admin server uses the same construction path.
func (s *Server) AdminMux() http.Handler {
	opts := s.adminMuxOptions()
	return restapi.NewMux(
		s.cfg.Store,
		func() { _ = s.Rebuild() },
		s.collector,
		s.buffer,
		s.broker,
		s.engine,
		opts...,
	)
}

// startAdmin binds an HTTP listener on cfg.AdminPort, builds the admin mux,
// and serves in a goroutine. Stores the *http.Server in s.adminSrv for Shutdown.
// Returns an error if the port cannot be bound.
func (s *Server) startAdmin() error {
	mux := s.AdminMux()
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.AdminPort))
	if err != nil {
		return fmt.Errorf("server: admin listen :%d: %w", s.cfg.AdminPort, err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.adminSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			// Log to stderr; s.cfg.Logger is not available in the admin package context.
			// A future enhancement could route this through s.cfg.Logger.
			println("mockwave admin server error:", err.Error())
		}
	}()
	return nil
}

// Shutdown gracefully stops the admin HTTP server and cancels background goroutines.
// Safe to call when AdminPort was 0 (no-op).
// Shutdown is not safe for concurrent callers — call it from a single goroutine.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.brokerCancel != nil {
		s.brokerCancel()
	}
	if s.reloadCancel != nil {
		s.reloadCancel()
	}
	if s.adminSrv != nil {
		return s.adminSrv.Shutdown(ctx)
	}
	return nil
}
