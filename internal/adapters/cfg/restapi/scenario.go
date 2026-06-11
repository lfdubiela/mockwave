package restapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
)

// ScenarioControl is the minimal surface the admin API needs to drive
// scenarios; the server provides an adapter. Kept as an interface to avoid a
// dependency on the server package.
type ScenarioControl interface {
	Start(id string) error // ErrScenarioNotFound / ErrScenarioRunning / nil
	Stop()
	ActiveStatus() any // JSON-serializable summary, or nil
}

var (
	ErrScenarioNotFound = errors.New("scenario not found")
	ErrScenarioRunning  = errors.New("a scenario is already running")
)

// scenarioStore returns the store's ScenarioStore capability, writing a 501 and
// returning false when the backend does not support scenarios.
func (a *adminAPI) scenarioStore(w http.ResponseWriter) (store.ScenarioStore, bool) {
	ss, ok := a.store.(store.ScenarioStore)
	if !ok {
		writeError(w, 501, "store does not support scenarios")
		return nil, false
	}
	return ss, true
}

// validateScenarioRefs checks that every rule_id exists and every non-empty
// phase fault_profile_id resolves. It writes the error response itself and
// returns false when the scenario must be rejected.
func (a *adminAPI) validateScenarioRefs(w http.ResponseWriter, s domain.Scenario) bool {
	rules, err := a.store.GetRules()
	if err != nil {
		writeError(w, 500, err.Error())
		return false
	}
	known := map[string]bool{}
	for _, r := range rules {
		known[r.ID] = true
	}
	for _, id := range s.RuleIDs {
		if !known[id] {
			writeError(w, 422, "unknown rule_id "+id)
			return false
		}
	}
	fs, ok := a.store.(store.FaultStore)
	for _, p := range s.Phases {
		if p.FaultProfileID == "" {
			continue
		}
		if !ok {
			writeError(w, 422, "store does not support fault profiles")
			return false
		}
		prof, err := fs.GetFaultProfile(p.FaultProfileID)
		if err != nil {
			writeError(w, 500, err.Error())
			return false
		}
		if prof == nil {
			writeError(w, 422, "unknown fault_profile_id "+p.FaultProfileID)
			return false
		}
	}
	return true
}

func (a *adminAPI) scenarios(w http.ResponseWriter, r *http.Request) {
	ss, ok := a.scenarioStore(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := ss.ListScenarios()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if list == nil {
			list = []domain.Scenario{}
		}
		writeJSON(w, 200, list)
	case http.MethodPost:
		var s domain.Scenario
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, 400, "invalid JSON: "+err.Error())
			return
		}
		if err := s.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if !a.validateScenarioRefs(w, s) {
			return
		}
		existing, err := ss.GetScenario(s.ID)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if existing != nil {
			writeError(w, 409, "scenario "+s.ID+" already exists")
			return
		}
		if err := ss.SaveScenario(s); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, s)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) scenarioByID(w http.ResponseWriter, r *http.Request) {
	rest := idFromPath(r.URL.Path, "/api/scenarios/")
	switch {
	case strings.HasSuffix(rest, "/start"):
		a.scenarioStart(w, r, strings.TrimSuffix(rest, "/start"))
		return
	case strings.HasSuffix(rest, "/stop"):
		a.scenarioStop(w, r, strings.TrimSuffix(rest, "/stop"))
		return
	}
	ss, ok := a.scenarioStore(w)
	if !ok {
		return
	}
	id := rest
	switch r.Method {
	case http.MethodGet:
		s, err := ss.GetScenario(id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if s == nil {
			writeError(w, 404, "scenario not found: "+id)
			return
		}
		writeJSON(w, 200, s)
	case http.MethodPut:
		var s domain.Scenario
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, 400, "invalid JSON: "+err.Error())
			return
		}
		s.ID = id
		if err := s.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if !a.validateScenarioRefs(w, s) {
			return
		}
		existing, err := ss.GetScenario(id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if existing == nil {
			writeError(w, 404, "scenario not found: "+id)
			return
		}
		if err := ss.SaveScenario(s); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, s)
	case http.MethodDelete:
		existing, err := ss.GetScenario(id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if existing == nil {
			writeError(w, 404, "scenario not found: "+id)
			return
		}
		if err := ss.DeleteScenario(id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) scenarioStart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if a.scenarioControl == nil {
		writeError(w, 501, "scenario control not available")
		return
	}
	err := a.scenarioControl.Start(id)
	switch {
	case errors.Is(err, ErrScenarioNotFound):
		writeError(w, 404, "scenario not found: "+id)
	case errors.Is(err, ErrScenarioRunning):
		writeError(w, 409, ErrScenarioRunning.Error())
	case err != nil:
		writeError(w, 500, err.Error())
	default:
		w.WriteHeader(202)
	}
}

func (a *adminAPI) scenarioStop(w http.ResponseWriter, r *http.Request, _ string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if a.scenarioControl == nil {
		writeError(w, 501, "scenario control not available")
		return
	}
	a.scenarioControl.Stop()
	w.WriteHeader(204)
}
