package restapi

import (
	"encoding/json"
	"net/http"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/domain/ports"
)

type OnReload func()

func NewMux(store ports.DataStore, onReload OnReload) *http.ServeMux {
	mux := http.NewServeMux()
	api := &adminAPI{store: store, onReload: onReload}
	mux.HandleFunc("/api/health", api.health)
	mux.HandleFunc("/api/rules", api.rules)
	mux.HandleFunc("/api/rules/", api.ruleByID)
	mux.HandleFunc("/api/simulations", api.simulations)
	mux.HandleFunc("/api/simulations/", api.simulationByID)
	return mux
}

type adminAPI struct {
	store    ports.DataStore
	onReload OnReload
}

func (a *adminAPI) reload() {
	if a.onReload != nil {
		a.onReload()
	}
}

func (a *adminAPI) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *adminAPI) rules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rules, err := a.store.GetRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, rules)
	case http.MethodPost:
		var rule domain.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeError(w, 400, "invalid JSON: "+err.Error())
			return
		}
		if err := rule.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := a.store.SaveRule(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, rule)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) ruleByID(w http.ResponseWriter, r *http.Request) {
	id := idFromPath(r.URL.Path, "/api/rules/")
	switch r.Method {
	case http.MethodDelete:
		if err := a.store.DeleteRule(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	case http.MethodPut:
		var rule domain.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		rule.ID = id
		if err := rule.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := a.store.SaveRule(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, rule)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) simulations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var sim domain.Simulation
		if err := json.NewDecoder(r.Body).Decode(&sim); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if err := a.store.SaveSimulation(sim); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, sim)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) simulationByID(w http.ResponseWriter, r *http.Request) {
	id := idFromPath(r.URL.Path, "/api/simulations/")
	switch r.Method {
	case http.MethodDelete:
		if err := a.store.DeleteSimulation(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func idFromPath(path, prefix string) string {
	if len(path) > len(prefix) {
		return path[len(prefix):]
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
