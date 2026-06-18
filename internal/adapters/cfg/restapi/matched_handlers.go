package restapi

import (
	"net/http"
	"net/url"
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
	page := a.matchedBuf.List(ruleID, parseCaptureQuery(r.URL.Query()))
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

// parseKVFilters parses repeated "key:value" query params into a map, splitting
// each on the first ':'. Entries without a non-empty key are skipped.
func parseKVFilters(vals []string) map[string]string {
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

// parseCaptureQuery builds a matched.Query from the URL query params shared by
// the matched- and event-capture list endpoints.
func parseCaptureQuery(q url.Values) matched.Query {
	mq := matched.Query{
		Cursor:  q.Get("cursor"),
		Method:  q.Get("method"),
		Path:    q.Get("path"),
		Headers: parseKVFilters(q["headers"]),
		Query:   parseKVFilters(q["query"]),
		Body:    parseKVFilters(q["body"]),
	}
	for k, v := range parseKVFilters(q["attr"]) {
		if mq.Query == nil {
			mq.Query = map[string]string{}
		}
		mq.Query["attr."+k] = v
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
	return mq
}
