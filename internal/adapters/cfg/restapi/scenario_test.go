package restapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/internal/chaos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scenarioMemStore extends faultMemStore with the store.ScenarioStore capability.
type scenarioMemStore struct {
	faultMemStore
	scenarios []domain.Scenario
}

func (m *scenarioMemStore) ListScenarios() ([]domain.Scenario, error) { return m.scenarios, nil }
func (m *scenarioMemStore) GetScenario(id string) (*domain.Scenario, error) {
	for _, s := range m.scenarios {
		if s.ID == id {
			ss := s
			return &ss, nil
		}
	}
	return nil, nil
}
func (m *scenarioMemStore) SaveScenario(s domain.Scenario) error {
	for i, existing := range m.scenarios {
		if existing.ID == s.ID {
			m.scenarios[i] = s
			return nil
		}
	}
	m.scenarios = append(m.scenarios, s)
	return nil
}
func (m *scenarioMemStore) DeleteScenario(id string) error {
	for i, s := range m.scenarios {
		if s.ID == id {
			m.scenarios = append(m.scenarios[:i], m.scenarios[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("not found")
}

// fakeScenarioControl records Start/Stop and returns a canned ActiveStatus.
type fakeScenarioControl struct {
	startErr   error
	startedIDs []string
	stops      int
	active     any
}

func (f *fakeScenarioControl) Start(id string) error {
	f.startedIDs = append(f.startedIDs, id)
	return f.startErr
}
func (f *fakeScenarioControl) Stop()           { f.stops++ }
func (f *fakeScenarioControl) ActiveStatus() any { return f.active }

func validScenario(id string) domain.Scenario {
	return domain.Scenario{
		ID:      id,
		Name:    "Scenario " + id,
		RuleIDs: []string{"r1"},
		Phases:  []domain.ScenarioPhase{{DurationSec: 5, FaultProfileID: "p1"}},
	}
}

func newScenarioStore() *scenarioMemStore {
	s := &scenarioMemStore{}
	s.rules = []domain.Rule{{ID: "r1", Match: domain.MatchCriteria{Path: "/x"}}}
	s.profiles = []domain.FaultProfile{validProfile("p1")}
	return s
}

func TestAdminAPI_Scenarios_CRUDRoundTrip(t *testing.T) {
	store := newScenarioStore()
	reloads := 0
	mux := restapi.NewMux(store, func() { reloads++ }, nil, nil, nil, nil)

	w := doReq(t, mux, http.MethodPost, "/api/scenarios", validScenario("sc1"))
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.Equal(t, 1, reloads)

	w = doReq(t, mux, http.MethodGet, "/api/scenarios/sc1", nil)
	require.Equal(t, 200, w.Code)
	var got domain.Scenario
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "sc1", got.ID)

	w = doReq(t, mux, http.MethodGet, "/api/scenarios", nil)
	require.Equal(t, 200, w.Code)
	var list []domain.Scenario
	require.NoError(t, json.NewDecoder(w.Body).Decode(&list))
	require.Len(t, list, 1)

	upd := validScenario("sc1")
	upd.Name = "Renamed"
	w = doReq(t, mux, http.MethodPut, "/api/scenarios/sc1", upd)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 2, reloads)
	assert.Equal(t, "Renamed", store.scenarios[0].Name)

	w = doReq(t, mux, http.MethodDelete, "/api/scenarios/sc1", nil)
	require.Equal(t, 204, w.Code)
	assert.Equal(t, 3, reloads)
	assert.Len(t, store.scenarios, 0)
}

func TestAdminAPI_PostScenario_DuplicateID(t *testing.T) {
	store := newScenarioStore()
	store.scenarios = []domain.Scenario{validScenario("sc1")}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	w := doReq(t, mux, http.MethodPost, "/api/scenarios", validScenario("sc1"))
	assert.Equal(t, 409, w.Code)
}

func TestAdminAPI_Scenario_NotFound(t *testing.T) {
	store := newScenarioStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/scenarios/nope"},
		{http.MethodPut, "/api/scenarios/nope"},
		{http.MethodDelete, "/api/scenarios/nope"},
	} {
		var body interface{}
		if tc.method == http.MethodPut {
			body = validScenario("nope")
		}
		w := doReq(t, mux, tc.method, tc.path, body)
		assert.Equal(t, 404, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestAdminAPI_PostScenario_Invalid(t *testing.T) {
	store := newScenarioStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	s := validScenario("sc1")
	s.Phases = nil
	w := doReq(t, mux, http.MethodPost, "/api/scenarios", s)
	assert.Equal(t, 422, w.Code)
	assert.Len(t, store.scenarios, 0)
}

func TestAdminAPI_PostScenario_UnknownRuleID(t *testing.T) {
	store := newScenarioStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	s := validScenario("sc1")
	s.RuleIDs = []string{"ghost"}
	w := doReq(t, mux, http.MethodPost, "/api/scenarios", s)
	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "unknown rule_id")
}

func TestAdminAPI_PostScenario_UnknownFaultProfile(t *testing.T) {
	store := newScenarioStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	s := validScenario("sc1")
	s.Phases = []domain.ScenarioPhase{{DurationSec: 5, FaultProfileID: "ghost"}}
	w := doReq(t, mux, http.MethodPost, "/api/scenarios", s)
	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "unknown fault_profile_id")
}

func TestAdminAPI_PostScenario_RecoveryPhaseOK(t *testing.T) {
	store := newScenarioStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	s := validScenario("sc1")
	s.Phases = []domain.ScenarioPhase{{DurationSec: 5}} // empty profile = recovery
	w := doReq(t, mux, http.MethodPost, "/api/scenarios", s)
	assert.Equal(t, 201, w.Code, w.Body.String())
}

func TestAdminAPI_Scenarios_UnsupportedStore(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/scenarios"},
		{http.MethodPost, "/api/scenarios"},
		{http.MethodGet, "/api/scenarios/sc1"},
		{http.MethodPut, "/api/scenarios/sc1"},
		{http.MethodDelete, "/api/scenarios/sc1"},
	} {
		w := doReq(t, mux, tc.method, tc.path, nil)
		assert.Equal(t, 501, w.Code, "%s %s", tc.method, tc.path)
		assert.Contains(t, w.Body.String(), "store does not support scenarios")
	}
}

func TestAdminAPI_Scenarios_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil)
	w := doReq(t, mux, http.MethodPatch, "/api/scenarios", nil)
	assert.Equal(t, 405, w.Code)
	w = doReq(t, mux, http.MethodPatch, "/api/scenarios/sc1", nil)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_ScenarioStart_Success(t *testing.T) {
	ctrl := &fakeScenarioControl{}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithScenarioControl(ctrl))
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/sc1/start", nil)
	assert.Equal(t, 202, w.Code, w.Body.String())
	assert.Equal(t, []string{"sc1"}, ctrl.startedIDs)
}

func TestAdminAPI_ScenarioStart_NotFound(t *testing.T) {
	ctrl := &fakeScenarioControl{startErr: restapi.ErrScenarioNotFound}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithScenarioControl(ctrl))
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/nope/start", nil)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_ScenarioStart_AlreadyRunning(t *testing.T) {
	ctrl := &fakeScenarioControl{startErr: restapi.ErrScenarioRunning}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithScenarioControl(ctrl))
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/sc1/start", nil)
	assert.Equal(t, 409, w.Code)
}

func TestAdminAPI_ScenarioStart_NoControl(t *testing.T) {
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil)
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/sc1/start", nil)
	assert.Equal(t, 501, w.Code)
	assert.Contains(t, w.Body.String(), "scenario control not available")
}

func TestAdminAPI_ScenarioStop_Success(t *testing.T) {
	ctrl := &fakeScenarioControl{}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithScenarioControl(ctrl))
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/sc1/stop", nil)
	assert.Equal(t, 204, w.Code)
	assert.Equal(t, 1, ctrl.stops)
}

func TestAdminAPI_ScenarioStop_NoControl(t *testing.T) {
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil)
	w := doReq(t, mux, http.MethodPost, "/api/scenarios/sc1/stop", nil)
	assert.Equal(t, 501, w.Code)
}

func TestAdminAPI_ChaosStatus_ActiveScenario(t *testing.T) {
	ks := chaos.NewKillSwitch()
	ctrl := &fakeScenarioControl{active: map[string]any{"scenario_id": "sc1"}}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithKillSwitch(ks), restapi.WithScenarioControl(ctrl))

	w := doReq(t, mux, http.MethodGet, "/api/chaos/status", nil)
	require.Equal(t, 200, w.Code)
	var body struct {
		Halted         bool           `json:"halted"`
		ActiveScenario map[string]any `json:"active_scenario"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "sc1", body.ActiveScenario["scenario_id"])

	ctrl.active = nil
	w = doReq(t, mux, http.MethodGet, "/api/chaos/status", nil)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Nil(t, body.ActiveScenario)
}

func TestAdminAPI_ChaosHalt_StopsScenario(t *testing.T) {
	ks := chaos.NewKillSwitch()
	ctrl := &fakeScenarioControl{}
	mux := restapi.NewMux(newScenarioStore(), nil, nil, nil, nil, nil, restapi.WithKillSwitch(ks), restapi.WithScenarioControl(ctrl))
	w := doReq(t, mux, http.MethodPost, "/api/chaos/halt", nil)
	require.Equal(t, 204, w.Code)
	assert.Equal(t, 1, ctrl.stops)
}
