package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/scripting"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/store"
)

type OnReload func()

// NewMux builds the admin HTTP mux.
// collector, buffer, broker, and engine may be nil — those endpoints return empty responses.
func NewMux(store store.DataStore, onReload OnReload, collector *metrics.Collector, buffer *unmatched.Buffer, broker *metrics.Broker, engine *scripting.Engine) *http.ServeMux {
	mux := http.NewServeMux()
	api := &adminAPI{
		store:     store,
		onReload:  onReload,
		collector: collector,
		buffer:    buffer,
		broker:    broker,
		engine:    engine,
	}
	mux.HandleFunc("/api/health", api.health)
	mux.HandleFunc("/api/rules", api.rules)
	mux.HandleFunc("/api/rules/", api.ruleByID)
	mux.HandleFunc("/api/simulations", api.simulations)
	mux.HandleFunc("/api/simulations/", api.simulationByID)
	mux.HandleFunc("/api/reload", api.reloadHandler)
	mux.HandleFunc("/api/metrics", api.metricsSnapshot)
	mux.HandleFunc("/api/metrics/stream", api.metricsStream)
	mux.HandleFunc("/api/unmatched", api.unmatchedHandler)
	mux.HandleFunc("/api/openapi.json", api.openapiHandler)
	mux.HandleFunc("/api/metrics/history", api.metricsHistory)
	mux.HandleFunc("/api/script/eval", api.scriptEval)
	serveUI(mux)
	return mux
}

type adminAPI struct {
	store     store.DataStore
	onReload  OnReload
	collector *metrics.Collector  // may be nil
	buffer    *unmatched.Buffer   // may be nil
	broker    *metrics.Broker     // may be nil
	engine    *scripting.Engine   // may be nil — eval endpoint returns 503
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
	case http.MethodGet:
		rules, err := a.store.GetRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		for _, rule := range rules {
			if rule.ID == id {
				writeJSON(w, 200, rule)
				return
			}
		}
		writeError(w, 404, "rule not found: "+id)
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
	case http.MethodGet:
		sims, err := a.store.ListSimulations()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, sims)
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
	case http.MethodGet:
		sim, err := a.store.GetSimulation(id)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		if sim == nil {
			writeError(w, 404, "simulation not found: "+id)
			return
		}
		writeJSON(w, 200, sim)
	case http.MethodPut:
		var sim domain.Simulation
		if err := json.NewDecoder(r.Body).Decode(&sim); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		sim.ID = id
		if err := a.store.SaveSimulation(sim); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, sim)
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

func (a *adminAPI) reloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	a.reload()
	w.WriteHeader(204)
}

func (a *adminAPI) metricsSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	if a.collector == nil {
		writeJSON(w, 200, metrics.Snapshot{})
		return
	}
	writeJSON(w, 200, a.collector.Snapshot())
}

func (a *adminAPI) metricsStream(w http.ResponseWriter, r *http.Request) {
	if a.broker == nil {
		http.Error(w, "metrics streaming not configured", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported by this server", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := a.broker.Subscribe()
	defer unsub()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *adminAPI) unmatchedHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if a.buffer == nil {
			writeJSON(w, 200, []unmatched.Request{})
			return
		}
		items := a.buffer.List()
		if items == nil {
			items = []unmatched.Request{}
		}
		writeJSON(w, 200, items)
	case http.MethodDelete:
		if a.buffer != nil {
			a.buffer.Clear()
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) openapiHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(openapiJSON)
}

func (a *adminAPI) metricsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	var buckets []metrics.MinuteBucket
	if a.collector != nil {
		buckets = a.collector.History()
	}
	if buckets == nil {
		buckets = []metrics.MinuteBucket{}
	}
	writeJSON(w, 200, map[string]interface{}{"buckets": buckets})
}

func (a *adminAPI) scriptEval(w http.ResponseWriter, r *http.Request) {
	writeError(w, 503, "not implemented yet")
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
