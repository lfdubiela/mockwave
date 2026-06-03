package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	restapi "github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
)

// startAdmin binds an HTTP listener on cfg.AdminPort, builds the admin mux,
// and serves in a goroutine. Stores the *http.Server in s.adminSrv for Shutdown.
// Returns an error if the port cannot be bound.
func (s *Server) startAdmin() error {
	mux := restapi.NewMux(
		s.cfg.Store,
		func() { _ = s.Rebuild() },
		s.collector,
		s.buffer,
		s.broker,
		s.engine,
	)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.AdminPort))
	if err != nil {
		return fmt.Errorf("server: admin listen :%d: %w", s.cfg.AdminPort, err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.adminSrv = srv
	go srv.Serve(ln) //nolint:errcheck
	return nil
}

// Shutdown gracefully stops the admin HTTP server and cancels background goroutines.
// Safe to call when AdminPort was 0 (no-op).
// Shutdown is not safe for concurrent callers — call it from a single goroutine.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.brokerCancel != nil {
		s.brokerCancel()
	}
	if s.adminSrv != nil {
		return s.adminSrv.Shutdown(ctx)
	}
	return nil
}
