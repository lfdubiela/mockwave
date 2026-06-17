package restapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// eventRules handles GET (list) and POST (create) on /api/event-rules.
func (a *adminAPI) eventRules(w http.ResponseWriter, r *http.Request) {
	ers, ok := a.store.(store.EventRuleStore)
	if !ok {
		writeError(w, 501, "event rules not supported by this store")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rules, err := ers.GetEventRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if rules == nil {
			rules = []domain.EventRule{}
		}
		writeJSON(w, 200, rules)
	case http.MethodPost:
		var er domain.EventRule
		if err := json.NewDecoder(r.Body).Decode(&er); err != nil {
			writeError(w, 400, "invalid JSON: "+err.Error())
			return
		}
		if err := er.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := ers.SaveEventRule(er); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, er)
	default:
		writeError(w, 405, "method not allowed")
	}
}

// eventRuleByID handles PUT (update) and DELETE on /api/event-rules/{id}.
func (a *adminAPI) eventRuleByID(w http.ResponseWriter, r *http.Request) {
	ers, ok := a.store.(store.EventRuleStore)
	if !ok {
		writeError(w, 501, "event rules not supported by this store")
		return
	}
	id := idFromPath(r.URL.Path, "/api/event-rules/")
	if id == "" {
		writeError(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var er domain.EventRule
		if err := json.NewDecoder(r.Body).Decode(&er); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		er.ID = id
		if err := er.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := ers.SaveEventRule(er); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, er)
	case http.MethodDelete:
		if err := ers.DeleteEventRule(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method not allowed")
	}
}

// eventCaptures lists/deletes captured events. Path: /api/event-captures/{ruleID}[/{id}].
func (a *adminAPI) eventCaptures(w http.ResponseWriter, r *http.Request) {
	if a.eventCaptureBuf == nil {
		writeError(w, 404, "event capture is disabled")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/event-captures/"), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 1:
			a.eventCaptureList(w, r, parts[0])
		case 2:
			a.eventCaptureDetail(w, parts[0], parts[1])
		default:
			writeError(w, 404, "not found")
		}
	case http.MethodDelete:
		ruleID := ""
		if len(parts) >= 1 {
			ruleID = parts[0]
		}
		a.eventCaptureBuf.Clear(ruleID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) eventCaptureList(w http.ResponseWriter, r *http.Request, ruleID string) {
	q := r.URL.Query()
	mq := matched.Query{Cursor: q.Get("cursor"), Method: q.Get("method"), Path: q.Get("path")}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			mq.Limit = n
		}
	}
	page := a.eventCaptureBuf.List(ruleID, mq)
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	writeJSON(w, 200, page)
}

func (a *adminAPI) eventCaptureDetail(w http.ResponseWriter, ruleID, id string) {
	full, ok := a.eventCaptureBuf.Get(ruleID, id)
	if !ok {
		writeError(w, 404, "event capture not found")
		return
	}
	writeJSON(w, 200, full)
}
