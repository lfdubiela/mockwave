package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/observability"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubStore struct{}

func (s *stubStore) GetRules() ([]domain.Rule, error)                    { return nil, nil }
func (s *stubStore) GetSimulation(id string) (*domain.Simulation, error) { return nil, nil }
func (s *stubStore) ListSimulations() ([]domain.Simulation, error)       { return nil, nil }
func (s *stubStore) SaveRule(r domain.Rule) error                        { return nil }
func (s *stubStore) SaveSimulation(sim domain.Simulation) error          { return nil }
func (s *stubStore) DeleteRule(id string) error                          { return nil }
func (s *stubStore) DeleteSimulation(id string) error                    { return nil }

func newStubStore() *stubStore { return &stubStore{} }

func TestServer_BuildsPipeline(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestServer_NilStoreUsesEnvFallback(t *testing.T) {
	// No env vars set — MOCKWAVE_STORE defaults to "json", which requires MOCKWAVE_CONFIG.
	// Ensure those vars are cleared so CI environment doesn't leak state.
	t.Setenv("MOCKWAVE_STORE", "json")
	t.Setenv("MOCKWAVE_CONFIG", "")
	_, err := server.New(server.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MOCKWAVE_CONFIG")
}

func TestServer_NilStoreBuildsFromEnv(t *testing.T) {
	// Write a minimal valid JSON config to a temp file.
	f, err := os.CreateTemp(t.TempDir(), "mockwave-*.json")
	require.NoError(t, err)
	_, err = f.WriteString(`{"rules":[],"simulations":[]}`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	t.Setenv("MOCKWAVE_STORE", "json")
	t.Setenv("MOCKWAVE_CONFIG", f.Name())

	srv, err := server.New(server.Config{})
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestServer_Rebuild(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NoError(t, srv.Rebuild())
}

func TestServer_HTTPHandler_NotNil(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	h := srv.HTTPHandler()
	assert.NotNil(t, h)
}

func TestServer_AcceptsObservabilityConfig(t *testing.T) {
	srv, err := server.New(server.Config{
		Store:   &stubStore{},
		Logger:  observability.NoopLogger{},
		Tracer:  observability.NoopTracer{},
		Metrics: observability.NoopMetrics{},
	})
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestServer_DefaultsToNoopWhenObservabilityNil(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NotNil(t, srv)
	assert.NoError(t, srv.Rebuild())
	// Getters must never return nil even when Config fields were nil.
	assert.NotNil(t, srv.Logger())
	assert.NotNil(t, srv.Tracer())
	assert.NotNil(t, srv.MetricsRecorder())
}

type stubStoreWithData struct{}

func (s *stubStoreWithData) GetRules() ([]domain.Rule, error) {
	return []domain.Rule{{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}}, nil
}
func (s *stubStoreWithData) GetSimulation(id string) (*domain.Simulation, error) {
	if id == "s1" {
		return &domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}, nil
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubStoreWithData) ListSimulations() ([]domain.Simulation, error) {
	return []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}}, nil
}
func (s *stubStoreWithData) SaveRule(r domain.Rule) error               { return nil }
func (s *stubStoreWithData) SaveSimulation(sim domain.Simulation) error { return nil }
func (s *stubStoreWithData) DeleteRule(id string) error                 { return nil }
func (s *stubStoreWithData) DeleteSimulation(id string) error           { return nil }

func TestServer_HTTPHandler_ServesRequest(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStoreWithData{}})
	require.NoError(t, err)
	h := srv.HTTPHandler()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestServer_MockHandler_RoutesGraphQL(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	h := srv.MockHandler([]string{"http", "graphql"}, srv.NewProxy())
	body := `{"query":"query GetItem { item { id } }","operationName":"GetItem"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp, "errors")
}

func TestServer_MockHandler_RoutesSOAP(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	h := srv.MockHandler([]string{"http", "soap"}, srv.NewProxy())
	req := httptest.NewRequest(http.MethodPost, "/service", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "GetItem")
	req.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	assert.Contains(t, w.Body.String(), "soap:Fault")
}

func TestServer_MockHandler_DefaultsToHTTP(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	h := srv.MockHandler([]string{"http"}, srv.NewProxy())
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
}

func TestServer_NewProxy_Execute_NoError(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	proxy := srv.NewProxy()
	require.NotNil(t, proxy)
	// Verify compile-time satisfaction of Executor interface.
	var _ server.Executor = proxy
}

// freePort returns a TCP port that is free at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestServer_AdminStartsWhenPortSet(t *testing.T) {
	port := freePort(t)
	srv, err := server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.NoError(t, err)
	require.NotNil(t, srv)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}

func TestServer_AdminPortAlreadyInUse_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin listen")
}

func TestServer_ShutdownStopsAdmin(t *testing.T) {
	port := freePort(t)
	srv, err := server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))

	_, err = http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	assert.Error(t, err)
}

func TestAdmin_ImportExportFlagWired(t *testing.T) {
	port := freePort(t)
	srv, err := server.New(server.Config{Store: newStubStore(), AdminPort: port, ImportExport: true})
	require.NoError(t, err)
	require.NotNil(t, srv)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["import_export"])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}

func TestServer_ShutdownNoopWhenAdminPortZero(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}

type versionedStub struct {
	*stubStore
	version int64
}

func (v *versionedStub) ConfigVersion() (int64, error) { return v.version, nil }

func TestServer_ReloaderEngagesForVersionedStore(t *testing.T) {
	vs := &versionedStub{stubStore: newStubStore()}
	srv, err := server.New(server.Config{Store: vs, ReloadInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer srv.Shutdown(context.Background())
	require.NotNil(t, srv)
}

func TestServer_FaultStage_ErrorAndKillSwitch(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate, SimulationID: "ok", FaultProfileID: "p",
			}},
		}},
		Simulations: []domain.Simulation{{ID: "ok", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}},
		FaultProfiles: []domain.FaultProfile{{
			ID: "p", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}},
		}},
	})
	srv, err := server.New(server.Config{Store: st})
	require.NoError(t, err)
	h := srv.HTTPHandler()

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, 503, do(), "fault profile should short-circuit with 503")
	srv.KillSwitch().Halt()
	assert.Equal(t, 200, do(), "halted kill switch should bypass faults")
	srv.KillSwitch().Resume()
	assert.Equal(t, 503, do(), "resumed kill switch should re-enable faults")
}

func TestServer_FaultProfileReload(t *testing.T) {
	st := newFileStore(t, domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate, SimulationID: "ok", FaultProfileID: "p",
			}},
		}},
		Simulations: []domain.Simulation{{ID: "ok", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}},
		FaultProfiles: []domain.FaultProfile{{
			ID: "p", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}},
		}},
	})
	srv, err := server.New(server.Config{Store: st})
	require.NoError(t, err)
	h := srv.HTTPHandler()

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, 503, do(), "enabled profile should fault")

	require.NoError(t, st.SaveFaultProfile(domain.FaultProfile{
		ID: "p", Name: "p", Enabled: false,
		Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}},
	}))
	require.NoError(t, srv.Rebuild())
	assert.Equal(t, 200, do(), "disabled profile after rebuild should not fault")
}

func TestServer_DanglingFaultProfileRef(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate, SimulationID: "ok", FaultProfileID: "missing",
			}},
		}},
		Simulations: []domain.Simulation{{ID: "ok", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}},
	})
	srv, err := server.New(server.Config{Store: st})
	require.NoError(t, err)
	h := srv.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code, "dangling fault profile reference should not fault")
}

func scenarioStore() *jsonfile.Store {
	return jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate, SimulationID: "ok",
			}},
		}},
		Simulations: []domain.Simulation{{ID: "ok", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}},
		FaultProfiles: []domain.FaultProfile{{
			ID: "boom", Name: "boom", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}},
		}},
	})
}

func TestServer_StartStopScenario(t *testing.T) {
	srv, err := server.New(server.Config{Store: scenarioStore()})
	require.NoError(t, err)
	h := srv.HTTPHandler()

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, 200, do(), "no scenario: rule responds 200")

	sc := domain.Scenario{
		ID: "drill", Name: "drill", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 60, FaultProfileID: "boom"}},
	}
	require.NoError(t, srv.StartScenario(sc))
	require.Eventually(t, func() bool {
		return srv.Scenario().Active() != nil
	}, time.Second, 5*time.Millisecond, "scenario becomes active")
	require.Equal(t, 503, do(), "active scenario overlays the boom profile")

	srv.StopScenario()
	require.Eventually(t, func() bool {
		return srv.Scenario().Active() == nil
	}, time.Second, 5*time.Millisecond, "controller cleared after stop")
	assert.Equal(t, 200, do(), "stopped scenario: rule responds 200 again")
}

func TestServer_StartScenario_AlreadyRunning(t *testing.T) {
	srv, err := server.New(server.Config{Store: scenarioStore()})
	require.NoError(t, err)

	sc := domain.Scenario{
		ID: "drill", Name: "drill", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 60, FaultProfileID: "boom"}},
	}
	require.NoError(t, srv.StartScenario(sc))
	t.Cleanup(srv.StopScenario)
	require.Error(t, srv.StartScenario(sc), "second start while running must error")
}

func TestServer_StartScenario_AfterCompletion(t *testing.T) {
	srv, err := server.New(server.Config{Store: scenarioStore()})
	require.NoError(t, err)

	// 1s phase elapses quickly; the run completes and clears scenarioCancel.
	sc := domain.Scenario{
		ID: "drill", Name: "drill", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 1, FaultProfileID: "boom"}},
	}
	require.NoError(t, srv.StartScenario(sc))
	// After the run completes, the goroutine clears scenarioCancel and a new
	// StartScenario must succeed (exercises the generation guard).
	require.Eventually(t, func() bool {
		return srv.StartScenario(sc) == nil
	}, 3*time.Second, 10*time.Millisecond, "starting again after completion must succeed")
	t.Cleanup(srv.StopScenario)
}

func newFileStore(t *testing.T, cfg domain.Config) *jsonfile.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	st, err := jsonfile.NewStore(path)
	require.NoError(t, err)
	return st
}
