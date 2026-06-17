package awsmsg

import (
	"io"
	"net/http"
	"net/url"

	"github.com/mockwave/mockwave/domain"
)

// Matcher resolves the active event rule for a parsed event.
// *eventroute.Matcher satisfies it.
type Matcher interface {
	Match(ev domain.Event) *domain.EventRule
}

// CaptureFunc records a matched intercepted event. ruleID is the matched rule;
// messageID is the synthesized id returned to the caller.
type CaptureFunc func(ev domain.Event, ruleID, messageID string)

// Handler parses an intercepted AWS publish, matches it, captures it (when
// matched), and writes a protocol-faithful response.
type Handler struct {
	matcher func() Matcher
	capture CaptureFunc
	newID   func() string
}

// NewHandler wires the handler. matcher() is read per-request so the server can
// hot-swap the rule set on reload. capture and newID must be non-nil.
func NewHandler(matcher func() Matcher, capture CaptureFunc, newID func() string) *Handler {
	return &Handler{matcher: matcher, capture: capture, newID: newID}
}

// ServeHTTP handles one intercepted publish. d is the result of Detect.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, d DetectResult) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "awsmsg: read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var ev domain.Event
	switch d.Service {
	case domain.EventServiceSNS:
		var form url.Values
		form, err = url.ParseQuery(string(body))
		if err == nil {
			ev, err = parseSNS(form)
		}
	default:
		// SQS / EventBridge land in Phase 2.
		http.Error(w, "awsmsg: unsupported service "+d.Service, http.StatusNotImplemented)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ev.Region = d.Region
	ev.Identity = d.Identity
	ev.RawBody = body

	ruleID := ""
	if m := h.matcher(); m != nil {
		if rule := m.Match(ev); rule != nil {
			ruleID = rule.ID
		}
	}
	messageID := h.newID()
	if ruleID != "" {
		h.capture(ev, ruleID, messageID)
	}

	respondSNS(w, messageID, h.newID())
}
