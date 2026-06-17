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

	switch d.Service {
	case domain.EventServiceSNS:
		form, perr := url.ParseQuery(string(body))
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		ev, perr := parseSNS(form)
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		h.stamp(&ev, d, body)
		messageID := h.matchAndCapture(ev)
		respondSNS(w, messageID, h.newID())

	case domain.EventServiceSQS:
		ev, attrs, perr := parseSQS(body)
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		h.stamp(&ev, d, body)
		messageID := h.matchAndCapture(ev)
		respondSQS(w, string(ev.Message), attrs, messageID)

	case domain.EventServiceEventBridge:
		evs, perr := parseEventBridge(body)
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		ids := make([]string, len(evs))
		for i := range evs {
			h.stamp(&evs[i], d, body)
			ids[i] = h.matchAndCapture(evs[i])
		}
		respondEventBridge(w, ids)

	default:
		http.Error(w, "awsmsg: unsupported service "+d.Service, http.StatusNotImplemented)
	}
}

// stamp annotates a parsed event with request-scoped metadata.
func (h *Handler) stamp(ev *domain.Event, d DetectResult, body []byte) {
	ev.Region = d.Region
	ev.Identity = d.Identity
	ev.RawBody = body
}

// matchAndCapture matches the event, captures it when matched, and returns the
// synthesized id (used as MessageId/EventId in the response).
func (h *Handler) matchAndCapture(ev domain.Event) string {
	ruleID := ""
	if m := h.matcher(); m != nil {
		if rule := m.Match(ev); rule != nil {
			ruleID = rule.ID
		}
	}
	id := h.newID()
	if ruleID != "" {
		h.capture(ev, ruleID, id)
	}
	return id
}
