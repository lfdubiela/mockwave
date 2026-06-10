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

// MuxOption customizes optional admin API behavior.
type MuxOption func(*adminAPI)

// WithImportExport enables the /api/export and /api/import endpoints.
// They are intended for remote store backends; with the json-file store the
// config file itself is already the import/export format, so the endpoints
// stay disabled and return 403.
func WithImportExport() MuxOption {
	return func(a *adminAPI) { a.importExport = true }
}

// NewMux builds the admin HTTP mux.
// collector, buffer, broker, and engine may be nil — those endpoints return empty responses.
func NewMux(store store.DataStore, onReload OnReload, collector *metrics.Collector, buffer *unmatched.Buffer, broker *metrics.Broker, engine *scripting.Engine, opts ...MuxOption) *http.ServeMux {
	mux := http.NewServeMux()
	api := &adminAPI{
		store:     store,
		onReload:  onReload,
		collector: collector,
		buffer:    buffer,
		broker:    broker,
		engine:    engine,
	}
	for _, opt := range opts {
		opt(api)
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
	mux.HandleFunc("/api/export", api.exportHandler)
	mux.HandleFunc("/api/import/preview", api.importPreviewHandler)
	mux.HandleFunc("/api/import", api.importHandler)
	serveUI(mux)
	return mux
}

type adminAPI struct {
	store        store.DataStore
	onReload     OnReload
	collector    *metrics.Collector // may be nil
	buffer       *unmatched.Buffer  // may be nil
	broker       *metrics.Broker    // may be nil
	engine       *scripting.Engine  // may be nil — eval endpoint returns 503
	importExport bool               // /api/export + /api/import enabled (remote stores only)
}

// duplicateMatchError returns a 409-ready, human-readable message if rule's
// match criteria duplicate another existing rule's, or "" if it is unique.
// Backend-agnostic: enforced here so every DataStore behaves identically.
func (a *adminAPI) duplicateMatchError(rule domain.Rule) (string, error) {
	rules, err := a.store.GetRules()
	if err != nil {
		return "", err
	}
	if dup := domain.FindDuplicateRule(rules, rule); dup != nil {
		label := dup.Name
		if label == "" {
			label = dup.ID
		}
		return fmt.Sprintf("%s: rule %q (id %s) already has identical match criteria (protocol, method, path, headers, query, body). Change a match field to make this rule distinct.", domain.ErrDuplicateRule, label, dup.ID), nil
	}
	return "", nil
}

// ruleSimIDs returns the simulation IDs owned by a rule (its simulate buckets).
func ruleSimIDs(r domain.Rule) []string {
	var ids []string
	for _, b := range r.Buckets {
		if b.Action == domain.ActionSimulate && b.SimulationID != "" {
			ids = append(ids, b.SimulationID)
		}
	}
	return ids
}

// deleteSimulations best-effort deletes the given simulation IDs, skipping any
// that are still referenced by another rule's simulate buckets. GetRules is
// called after the owning rule has already been removed from the store, so
// referenced reflects only surviving rules. Not-found deletes are ignored so
// cascade cleanup is idempotent across backends. On store error the cascade is
// skipped entirely rather than risk over-deletion.
func (a *adminAPI) deleteSimulations(ids []string) {
	rules, err := a.store.GetRules()
	if err != nil {
		return // best-effort: skip cascade rather than risk over-deletion
	}
	referenced := map[string]bool{}
	for _, r := range rules {
		for _, id := range ruleSimIDs(r) {
			referenced[id] = true
		}
	}
	for _, id := range ids {
		if !referenced[id] {
			_ = a.store.DeleteSimulation(id)
		}
	}
}

// ruleByID returns the current stored rule with the given ID, or nil.
func (a *adminAPI) ruleByIDLookup(id string) (*domain.Rule, error) {
	rules, err := a.store.GetRules()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, nil
}

func (a *adminAPI) reload() {
	if a.onReload != nil {
		a.onReload()
	}
}

func (a *adminAPI) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "import_export": a.importExport})
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
		if msg, err := a.duplicateMatchError(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		} else if msg != "" {
			writeError(w, 409, msg)
			return
		}
		if err := a.store.SaveRule(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, rule)
	case http.MethodDelete:
		// Bulk delete: remove every rule. Backend-agnostic — iterates the
		// store's own delete so any DataStore behaves identically.
		rules, err := a.store.GetRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		for _, rule := range rules {
			if err := a.store.DeleteRule(rule.ID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			a.deleteSimulations(ruleSimIDs(rule)) // cascade: rule owns its sims
		}
		a.reload()
		w.WriteHeader(204)
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
		// Capture owned simulation IDs before deleting (DeleteRule may mutate
		// the store's backing slice, invalidating a held rule pointer).
		var ownedSimIDs []string
		if owned, err := a.ruleByIDLookup(id); err != nil {
			writeError(w, 500, err.Error())
			return
		} else if owned != nil {
			ownedSimIDs = ruleSimIDs(*owned)
		}
		if err := a.store.DeleteRule(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.deleteSimulations(ownedSimIDs) // cascade: rule owns its sims
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
		if msg, err := a.duplicateMatchError(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		} else if msg != "" {
			writeError(w, 409, msg)
			return
		}
		// Capture the previous version's owned simulation IDs (before saving) to
		// clean up any no longer referenced after this edit (e.g. removed bucket).
		var prevSimIDs []string
		if prev, err := a.ruleByIDLookup(id); err != nil {
			writeError(w, 500, err.Error())
			return
		} else if prev != nil {
			prevSimIDs = ruleSimIDs(*prev)
		}
		if err := a.store.SaveRule(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		newIDs := make(map[string]bool)
		for _, sid := range ruleSimIDs(rule) {
			newIDs[sid] = true
		}
		var orphaned []string
		for _, sid := range prevSimIDs {
			if !newIDs[sid] {
				orphaned = append(orphaned, sid)
			}
		}
		a.deleteSimulations(orphaned)
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
		items := a.buffer.ListDeduped()
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
	var rules []metrics.RuleSeries
	if a.collector != nil {
		rules = a.collector.RuleHistory(10)
	}
	if rules == nil {
		rules = []metrics.RuleSeries{}
	}
	writeJSON(w, 200, map[string]interface{}{"rules": rules})
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
