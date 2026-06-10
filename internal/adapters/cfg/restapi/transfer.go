package restapi

import (
	"encoding/json"
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

// conflictParty identifies one side of an import conflict for UI display.
type conflictParty struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// importConflict is one incoming rule that would replace something existing.
// Reason "id": incoming rule ID already exists in the store.
// Reason "match": match criteria equal an existing rule with a different ID.
type importConflict struct {
	Reason   string        `json:"reason"`
	Incoming conflictParty `json:"incoming"`
	Existing conflictParty `json:"existing"`
}

func party(r domain.Rule) conflictParty {
	name := r.Name
	if name == "" {
		name = r.ID
	}
	return conflictParty{ID: r.ID, Name: name}
}

// detectConflicts compares incoming rules against stored ones. Same ID wins
// over equal match when both apply (identity is the more specific fact).
func detectConflicts(stored, incoming []domain.Rule) []importConflict {
	byID := make(map[string]domain.Rule, len(stored))
	for _, r := range stored {
		byID[r.ID] = r
	}
	var out []importConflict
	for _, in := range incoming {
		if ex, ok := byID[in.ID]; ok {
			out = append(out, importConflict{Reason: "id", Incoming: party(in), Existing: party(ex)})
			continue
		}
		if dup := domain.FindDuplicateRule(stored, in); dup != nil {
			out = append(out, importConflict{Reason: "match", Incoming: party(in), Existing: party(*dup)})
		}
	}
	return out
}

// validateImportPayload checks every incoming rule and cross-references sim
// IDs against the payload and the store. Returns a non-empty message on the
// first class of failure found; "" when the payload is acceptable.
func (a *adminAPI) validateImportPayload(cfg domain.Config) (string, error) {
	payloadSims := make(map[string]bool, len(cfg.Simulations))
	for _, s := range cfg.Simulations {
		if s.ID == "" {
			return "simulation with empty id", nil
		}
		if payloadSims[s.ID] {
			return fmt.Sprintf("payload contains duplicate simulation id %q", s.ID), nil
		}
		payloadSims[s.ID] = true
	}
	seenIDs := make(map[string]bool, len(cfg.Rules))
	for i, r := range cfg.Rules {
		if err := r.Validate(); err != nil {
			return fmt.Sprintf("rule[%d] (%s): %v", i, r.ID, err), nil
		}
		if seenIDs[r.ID] {
			return fmt.Sprintf("payload contains duplicate rule id %q", r.ID), nil
		}
		seenIDs[r.ID] = true
		if dup := domain.FindDuplicateRule(cfg.Rules[:i], r); dup != nil {
			return fmt.Sprintf("payload rules %q and %q have identical match criteria", dup.ID, r.ID), nil
		}
		for _, simID := range ruleSimIDs(r) {
			if payloadSims[simID] {
				continue
			}
			sim, err := a.store.GetSimulation(simID)
			if err != nil {
				return "", err
			}
			if sim == nil {
				return fmt.Sprintf("rule %q references simulation %q which exists neither in the payload nor in the store", r.ID, simID), nil
			}
		}
	}
	return "", nil
}

// decodeImportPayload handles the shared gate + decode + validate prologue of
// both import phases. Returns ok=false after writing the error response.
func (a *adminAPI) decodeImportPayload(w http.ResponseWriter, r *http.Request) (domain.Config, []domain.Rule, bool) {
	var cfg domain.Config
	if !a.requireImportExport(w) {
		return cfg, nil, false
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return cfg, nil, false
	}
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return cfg, nil, false
	}
	if msg, err := a.validateImportPayload(cfg); err != nil {
		writeError(w, 500, err.Error())
		return cfg, nil, false
	} else if msg != "" {
		writeError(w, 422, msg)
		return cfg, nil, false
	}
	stored, err := a.store.GetRules()
	if err != nil {
		writeError(w, 500, err.Error())
		return cfg, nil, false
	}
	return cfg, stored, true
}

// importHandler is phase 2: commit. Conflicting rules are skipped unless their
// incoming ID is listed in ?override=, in which case the existing counterpart
// (and its owned simulations) is removed first. Payload sims are written only
// for rules that are actually saved.
func (a *adminAPI) importHandler(w http.ResponseWriter, r *http.Request) {
	cfg, stored, ok := a.decodeImportPayload(w, r)
	if !ok {
		return
	}
	override := map[string]bool{}
	for _, id := range strings.Split(r.URL.Query().Get("override"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			override[id] = true
		}
	}
	conflictByIncoming := map[string]importConflict{}
	for _, c := range detectConflicts(stored, cfg.Rules) {
		conflictByIncoming[c.Incoming.ID] = c
	}
	storedByID := make(map[string]domain.Rule, len(stored))
	for _, ru := range stored {
		storedByID[ru.ID] = ru
	}
	payloadSims := make(map[string]domain.Simulation, len(cfg.Simulations))
	for _, s := range cfg.Simulations {
		payloadSims[s.ID] = s
	}

	imported, skipped, overridden := []string{}, []string{}, []string{}
	wrote := false
	// Reload on every exit path: a mid-loop store error must not leave
	// already-written rules invisible to the live pipeline.
	defer func() {
		if wrote {
			a.reload()
		}
	}()

	saveRuleWithSims := func(ru domain.Rule) bool {
		for _, simID := range ruleSimIDs(ru) {
			if sim, ok := payloadSims[simID]; ok {
				if err := a.store.SaveSimulation(sim); err != nil {
					writeError(w, 500, err.Error())
					return false
				}
			}
		}
		if err := a.store.SaveRule(ru); err != nil {
			writeError(w, 500, err.Error())
			return false
		}
		wrote = true
		return true
	}

	for _, in := range cfg.Rules {
		c, conflicting := conflictByIncoming[in.ID]
		switch {
		case conflicting && !override[in.ID]:
			skipped = append(skipped, in.ID)
		case conflicting:
			if ex, ok := storedByID[c.Existing.ID]; ok {
				if err := a.store.DeleteRule(ex.ID); err != nil {
					writeError(w, 500, err.Error())
					return
				}
				// Cascade the old rule's sims, except those the incoming rule
				// still references: the cascade runs before the incoming rule is
				// saved, so the reference guard in deleteSimulations cannot see
				// it yet and would delete a store-only sim it reuses.
				reused := map[string]bool{}
				for _, id := range ruleSimIDs(in) {
					reused[id] = true
				}
				var cascade []string
				for _, id := range ruleSimIDs(ex) {
					if !reused[id] {
						cascade = append(cascade, id)
					}
				}
				a.deleteSimulations(cascade) // cascade: rule owns its sims
				wrote = true
			}
			if !saveRuleWithSims(in) {
				return
			}
			overridden = append(overridden, in.ID)
		default:
			if !saveRuleWithSims(in) {
				return
			}
			imported = append(imported, in.ID)
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"imported":   imported,
		"skipped":    skipped,
		"overridden": overridden,
	})
}

// importPreviewHandler is phase 1: dry-run conflict report, no writes.
func (a *adminAPI) importPreviewHandler(w http.ResponseWriter, r *http.Request) {
	cfg, stored, ok := a.decodeImportPayload(w, r)
	if !ok {
		return
	}
	conflicts := detectConflicts(stored, cfg.Rules)
	if conflicts == nil {
		conflicts = []importConflict{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"importable": len(cfg.Rules) - len(conflicts),
		"conflicts":  conflicts,
	})
}
