package restapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// matchedByRule routes /api/matched/{rule_id} and /api/matched/{rule_id}/{id}.
//
//	GET    /api/matched/{rule}        → paginated list (reduced)
//	GET    /api/matched/{rule}/{id}   → full detail (with bodies)
//	DELETE /api/matched/{rule}        → clear a rule's captures
//	DELETE /api/matched               → clear all
func (a *adminAPI) matchedByRule(w http.ResponseWriter, r *http.Request) {
	if a.matchedBuf == nil {
		writeError(w, 404, "matched capture is disabled")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/matched/")
	rest = strings.Trim(rest, "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 1:
			a.matchedList(w, r, parts[0])
		case 2:
			a.matchedDetail(w, r, parts[0], parts[1])
		default:
			writeError(w, 404, "not found")
		}
	case http.MethodDelete:
		ruleID := ""
		if len(parts) >= 1 {
			ruleID = parts[0]
		}
		a.matchedBuf.Clear(ruleID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) matchedList(w http.ResponseWriter, r *http.Request, ruleID string) {
	q := r.URL.Query()
	mq := matched.Query{
		Cursor:  q.Get("cursor"),
		Method:  q.Get("method"),
		Path:    q.Get("path"),
		Headers: parseHeaderFilters(q["headers"]),
	}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			mq.Limit = n
		}
	}
	if s := q.Get("status"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			mq.Status = n
		}
	}
	page := a.matchedBuf.List(ruleID, mq)
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	writeJSON(w, 200, page)
}

func (a *adminAPI) matchedDetail(w http.ResponseWriter, r *http.Request, ruleID, id string) {
	full, ok := a.matchedBuf.Get(ruleID, id)
	if ok {
		// Buffer hit: if the entry was hydrated from the store, its bodies may
		// be nil. Set BodyWarning when RequestBodyID is set but body is absent.
		if full.RequestBodyID != "" && full.RequestBody == nil {
			full.BodyWarning = "request body unavailable (evicted)"
		}
		writeJSON(w, 200, full)
		return
	}

	// Buffer miss: fall back to the store if it supports matched capture.
	if ms, ok := a.store.(store.MatchedStore); ok {
		storeFull, err := ms.GetMatched(r.Context(), ruleID, id)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if storeFull != nil {
			if storeFull.RequestBodyID != "" && storeFull.RequestBody == nil {
				storeFull.BodyWarning = "request body unavailable (evicted)"
			}
			writeJSON(w, 200, storeFull)
			return
		}
	}

	writeError(w, 404, "matched request not found")
}

// parseHeaderFilters turns repeated "key:value" params into a map. Only the
// first ':' is the separator (values may contain ':'). Malformed entries skipped.
func parseHeaderFilters(vals []string) map[string]string {
	if len(vals) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, v := range vals {
		i := strings.Index(v, ":")
		if i <= 0 {
			continue
		}
		out[v[:i]] = v[i+1:]
	}
	return out
}
