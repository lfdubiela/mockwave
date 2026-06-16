package restapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/internal/matched"
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
			a.matchedDetail(w, parts[0], parts[1])
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

func (a *adminAPI) matchedDetail(w http.ResponseWriter, ruleID, id string) {
	full, ok := a.matchedBuf.Get(ruleID, id)
	if !ok {
		writeError(w, 404, "matched request not found")
		return
	}
	writeJSON(w, 200, full)
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
