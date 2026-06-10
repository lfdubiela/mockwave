package restapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mockwave/mockwave/domain"
)

// requireImportExport writes a 403 and returns false when the import/export
// endpoints are disabled (json-file store: the config file itself is already
// the import/export format).
func (a *adminAPI) requireImportExport(w http.ResponseWriter) bool {
	if !a.importExport {
		writeError(w, 403, "import/export is disabled for the json store: the config file itself is the import/export format. Use --store=dynamodb|mongo|cosmos to enable.")
		return false
	}
	return true
}

// exportHandler streams selected rules (all when ?rules is absent) plus the
// simulations their buckets reference, as a runnable domain.Config file.
func (a *adminAPI) exportHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireImportExport(w) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	rules, err := a.store.GetRules()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if param := strings.TrimSpace(r.URL.Query().Get("rules")); param != "" {
		byID := make(map[string]domain.Rule, len(rules))
		for _, ru := range rules {
			byID[ru.ID] = ru
		}
		var selected []domain.Rule
		var missing []string
		for _, id := range strings.Split(param, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if ru, ok := byID[id]; ok {
				selected = append(selected, ru)
			} else {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			writeError(w, 404, fmt.Sprintf("unknown rule ids: %s", strings.Join(missing, ", ")))
			return
		}
		rules = selected
	}

	cfg := domain.Config{Rules: rules, Simulations: []domain.Simulation{}}
	seen := map[string]bool{}
	for _, ru := range rules {
		for _, id := range ruleSimIDs(ru) {
			if seen[id] {
				continue
			}
			seen[id] = true
			sim, err := a.store.GetSimulation(id)
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
			if sim != nil {
				cfg.Simulations = append(cfg.Simulations, *sim)
			}
		}
	}
	if cfg.Rules == nil {
		cfg.Rules = []domain.Rule{}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="mockwave-export.json"`)
	writeJSON(w, 200, cfg)
}
