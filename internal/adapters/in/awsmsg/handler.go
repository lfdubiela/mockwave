package awsmsg

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mockwave/mockwave/domain"
)

// Matcher resolves the active event rule for a parsed event.
// *eventroute.Matcher satisfies it.
type Matcher interface {
	Match(ev domain.Event) *domain.EventRule
}

// Forwarder re-emits an event to a real broker and returns the broker's id.
// *awsforward.Forwarder satisfies it. May be nil (forwarding disabled).
type Forwarder interface {
	Forward(ctx context.Context, ev domain.Event, fwd domain.EventForward) (string, error)
}

// Capture is the record handed to the capture sink for one matched event.
type Capture struct {
	Event         domain.Event
	RuleID        string
	MessageID     string // synthesized id, or the real id when forwarded
	Forwarded     bool
	ForwardTarget string
	Status        int // response status recorded (200, or 502 on forward error)
}

// CaptureFunc records a matched intercepted event.
type CaptureFunc func(c Capture)

// Handler parses an intercepted AWS publish, matches it, forwards or
// synthesizes a response, and captures the outcome.
type Handler struct {
	matcher   func() Matcher
	capture   CaptureFunc
	newID     func() string
	forwarder Forwarder // may be nil
}

// NewHandler wires the handler. matcher() is read per-request so the server can
// hot-swap the rule set on reload. forwarder may be nil to disable forwarding.
func NewHandler(matcher func() Matcher, capture CaptureFunc, newID func() string, forwarder Forwarder) *Handler {
	return &Handler{matcher: matcher, capture: capture, newID: newID, forwarder: forwarder}
}

// ServeHTTP handles one intercepted publish. d is the result of Detect.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, d DetectResult) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "awsmsg: read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

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
		id, ferr := h.resolveOutcome(ctx, ev)
		if ferr != nil {
			http.Error(w, "awsmsg: forward: "+ferr.Error(), http.StatusBadGateway)
			return
		}
		respondSNS(w, id, h.newID())

	case domain.EventServiceSQS:
		ev, attrs, perr := parseSQS(body)
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		h.stamp(&ev, d, body)
		id, ferr := h.resolveOutcome(ctx, ev)
		if ferr != nil {
			http.Error(w, "awsmsg: forward: "+ferr.Error(), http.StatusBadGateway)
			return
		}
		respondSQS(w, string(ev.Message), attrs, id)

	case domain.EventServiceEventBridge:
		evs, perr := parseEventBridge(body)
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		ids := make([]string, len(evs))
		for i := range evs {
			h.stamp(&evs[i], d, body)
			id, ferr := h.resolveOutcome(ctx, evs[i])
			if ferr != nil {
				http.Error(w, "awsmsg: forward: "+ferr.Error(), http.StatusBadGateway)
				return
			}
			ids[i] = id
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

// matchRule returns the matched rule for ev, or nil.
func (h *Handler) matchRule(ev domain.Event) *domain.EventRule {
	if m := h.matcher(); m != nil {
		return m.Match(ev)
	}
	return nil
}

// resolveOutcome decides forward vs synthesize for one event, captures the
// outcome when matched, and returns the id to relay in the response. A non-nil
// error means forwarding failed (caller writes 502).
func (h *Handler) resolveOutcome(ctx context.Context, ev domain.Event) (string, error) {
	rule := h.matchRule(ev)
	if rule != nil && rule.Forward != nil && h.forwarder != nil {
		target := forwardTarget(*rule.Forward)
		id, ferr := h.forwarder.Forward(ctx, ev, *rule.Forward)
		if rule.Forward.DelayMs > 0 {
			time.Sleep(time.Duration(rule.Forward.DelayMs) * time.Millisecond)
		}
		status := http.StatusOK
		if ferr != nil {
			status = http.StatusBadGateway
		}
		h.capture(Capture{Event: ev, RuleID: rule.ID, MessageID: id, Forwarded: true, ForwardTarget: target, Status: status})
		return id, ferr
	}
	id := h.newID()
	if rule != nil {
		h.capture(Capture{Event: ev, RuleID: rule.ID, MessageID: id, Status: http.StatusOK})
	}
	return id, nil
}

// forwardTarget is a human-readable forward destination for the capture record.
func forwardTarget(fwd domain.EventForward) string {
	if fwd.Endpoint != "" {
		return fwd.Endpoint
	}
	region := fwd.Region
	if region == "" {
		region = "us-east-1"
	}
	return "aws:" + region
}
