package domain

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ActionSimulate = "simulate"
	ActionForward  = "forward"
)

// ErrDuplicateRule is returned when saving a rule whose match criteria are
// identical to another existing rule (different ID).
var ErrDuplicateRule = errors.New("a rule with identical match criteria already exists")

// FindDuplicateRule returns a pointer to the first rule in rules whose match
// criteria equal r's, excluding any rule sharing r's ID (so editing a rule in
// place is not flagged as a duplicate of itself). Returns nil when none match.
func FindDuplicateRule(rules []Rule, r Rule) *Rule {
	for i := range rules {
		if rules[i].ID != r.ID && rules[i].Match.Equal(r.Match) {
			return &rules[i]
		}
	}
	return nil
}

// MatchCriteria defines conditions a request must satisfy for a rule to apply.
type MatchCriteria struct {
	Protocol string            `json:"protocol"` // http | graphql | soap | grpc
	Method   string            `json:"method"`   // GET, POST, etc. (HTTP only)
	Path     string            `json:"path"`     // glob: /users/*, /api/**
	Headers  map[string]string `json:"headers"`  // exact key/value
	Query    map[string]string `json:"query"`    // exact key/value (HTTP only)
	Body     map[string]string `json:"body"`     // JSONPath → exact value
}

// Equal reports whether two MatchCriteria are identical across every field.
// Map comparison is order-independent; a nil map and an empty map are equal.
func (m MatchCriteria) Equal(o MatchCriteria) bool {
	return m.Protocol == o.Protocol &&
		m.Method == o.Method &&
		m.Path == o.Path &&
		equalStringMap(m.Headers, o.Headers) &&
		equalStringMap(m.Query, o.Query) &&
		equalStringMap(m.Body, o.Body)
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// WeightedBucket is one branch in a rule's traffic split.
type WeightedBucket struct {
	Weight       int    `json:"weight"`                // relative weight, must be > 0
	Action       string `json:"action"`                // "simulate" | "forward"
	SimulationID string `json:"simulation_id"`         // required when Action = "simulate"
	DelayMs      int    `json:"delay_ms,omitempty"`    // forward bucket: min response time (ms), concurrent w/ upstream
	ForwardURL   string `json:"forward_url,omitempty"` // required when Action = "forward"; upstream base URL

	FaultProfileID string `json:"fault_profile_id,omitempty"` // optional chaos profile applied to this bucket
}

func (b WeightedBucket) Validate() error {
	if b.Weight <= 0 {
		return fmt.Errorf("bucket weight must be > 0, got %d", b.Weight)
	}
	if b.Action == ActionSimulate && b.SimulationID == "" {
		return fmt.Errorf("bucket with action=simulate requires simulation_id")
	}
	if b.Action != ActionSimulate && b.Action != ActionForward {
		return fmt.Errorf("bucket action must be 'simulate' or 'forward', got %q", b.Action)
	}
	if b.DelayMs < 0 {
		return fmt.Errorf("bucket delay_ms must be >= 0, got %d", b.DelayMs)
	}
	if b.Action == ActionForward && b.ForwardURL == "" {
		return fmt.Errorf("bucket with action=forward requires forward_url")
	}
	return nil
}

// Rule maps incoming requests to a set of weighted response buckets.
type Rule struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Match   MatchCriteria    `json:"match"`
	Buckets []WeightedBucket `json:"buckets"`
	// Disabled rules are persisted but never match incoming requests.
	// Omitted when false so existing rules stay byte-compatible.
	Disabled bool `json:"disabled,omitempty"`
}

func (r Rule) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("rule id is required")
	}
	if r.Match.Path == "" {
		return fmt.Errorf("rule match.path is required")
	}
	if len(r.Buckets) == 0 {
		return fmt.Errorf("rule must have at least one bucket")
	}
	total := 0
	for i, b := range r.Buckets {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("bucket[%d]: %w", i, err)
		}
		total += b.Weight
	}
	// Bucket weights are percentages of traffic and must cover exactly 100%.
	if total != 100 {
		return fmt.Errorf("bucket weights must sum to 100, got %d", total)
	}
	return nil
}

// HTTPResponse holds an HTTP mock response definition.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    interface{}       `json:"body"`
	DelayMs int               `json:"delay_ms"`
}

// Simulation defines a mock response for a matched rule bucket.
type Simulation struct {
	ID       string       `json:"id"`
	Protocol string       `json:"protocol"`
	Response HTTPResponse `json:"response"`
	Script   string       `json:"script,omitempty"` // optional JS (goja)

	// SOAP: returned as-is as the response body with Content-Type: text/xml
	SOAPEnvelope string `json:"soap_envelope,omitempty"`

	// gRPC: JSON representation of the proto response message
	GRPCMessage string `json:"grpc_message,omitempty"`
	// gRPC: google.golang.org/grpc/codes.Code value (0 = OK)
	GRPCStatus int `json:"grpc_status,omitempty"`
}

// Config is the top-level structure of a mockwave JSON config file.
type Config struct {
	Rules       []Rule       `json:"rules"`
	Simulations []Simulation `json:"simulations"`

	FaultProfiles []FaultProfile `json:"fault_profiles,omitempty"`

	Scenarios []Scenario `json:"scenarios,omitempty"`

	EventRules []EventRule `json:"event_rules,omitempty"`
}

// ScenarioPhase is one timed step of a Scenario. FaultProfileID "" means a
// recovery phase: targeted rules run with no injected faults.
type ScenarioPhase struct {
	DurationSec    int    `json:"duration_sec"`
	FaultProfileID string `json:"fault_profile_id,omitempty"`
}

// Scenario applies a sequence of fault profiles to a set of rules over time.
type Scenario struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	RuleIDs []string        `json:"rule_ids"`
	Phases  []ScenarioPhase `json:"phases"`
}

func (s Scenario) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario id is required")
	}
	if len(s.RuleIDs) == 0 {
		return fmt.Errorf("scenario must target at least one rule")
	}
	if len(s.Phases) == 0 {
		return fmt.Errorf("scenario must have at least one phase")
	}
	for i, p := range s.Phases {
		if p.DurationSec <= 0 {
			return fmt.Errorf("phase[%d] duration_sec must be > 0, got %d", i, p.DurationSec)
		}
	}
	return nil
}

// Fault types supported by FaultProfile.
const (
	FaultJitter = "jitter"
	FaultError  = "error"

	FaultHang         = "hang"
	FaultReset        = "reset"
	FaultHalfResponse = "halfResponse"
	FaultSlowBody     = "slowBody"

	FaultRetryStorm = "retryStorm"
)

// FaultParams holds type-specific fault parameters. One flat struct keeps the
// JSON shape simple; Validate enforces which fields each type requires.
type FaultParams struct {
	BaseDelayMs int               `json:"base_delay_ms,omitempty"` // jitter: fixed delay added to every affected request
	JitterMs    int               `json:"jitter_ms,omitempty"`     // jitter: random extra delay in [0, JitterMs)
	StatusCode  int               `json:"status_code,omitempty"`   // error: HTTP status to return
	Body        string            `json:"body,omitempty"`          // error: response body (raw string)
	Headers     map[string]string `json:"headers,omitempty"`       // error: response headers
	MaxMs       int               `json:"max_ms,omitempty"`        // hang: max ms to block before giving up
	Fraction    float64           `json:"fraction,omitempty"`      // halfResponse: portion of body to write in [0,1)
	BytesPerSec int               `json:"bytes_per_sec,omitempty"` // slowBody: write throttle rate
	FailFirst   int               `json:"fail_first,omitempty"`    // retryStorm: number of initial requests per key to fail
	KeyBy       string            `json:"key_by,omitempty"`        // retryStorm: "path" or "header:<Name>"
	WindowSec   int               `json:"window_sec,omitempty"`    // retryStorm: sliding window TTL in seconds
}

// Fault is one failure mode inside a FaultProfile.
type Fault struct {
	Type        string      `json:"type"`
	Probability float64     `json:"probability"` // [0,1], rolled per request
	Params      FaultParams `json:"params"`
}

func (f Fault) Validate() error {
	if f.Probability < 0 || f.Probability > 1 {
		return fmt.Errorf("fault probability must be in [0,1], got %v", f.Probability)
	}
	switch f.Type {
	case FaultJitter:
		if f.Params.BaseDelayMs < 0 || f.Params.JitterMs < 0 {
			return fmt.Errorf("jitter fault delay params must be >= 0")
		}
		if f.Params.BaseDelayMs <= 0 && f.Params.JitterMs <= 0 {
			return fmt.Errorf("jitter fault requires base_delay_ms or jitter_ms > 0")
		}
	case FaultError:
		if f.Params.StatusCode < 100 || f.Params.StatusCode > 599 {
			return fmt.Errorf("error fault requires status_code in [100,599], got %d", f.Params.StatusCode)
		}
	case FaultHang:
		if f.Params.MaxMs <= 0 {
			return fmt.Errorf("hang fault requires max_ms > 0, got %d", f.Params.MaxMs)
		}
	case FaultReset:
		// no params
	case FaultHalfResponse:
		if f.Params.Fraction <= 0 || f.Params.Fraction >= 1 {
			return fmt.Errorf("halfResponse fault requires fraction in (0,1), got %v", f.Params.Fraction)
		}
	case FaultSlowBody:
		if f.Params.BytesPerSec <= 0 {
			return fmt.Errorf("slowBody fault requires bytes_per_sec > 0, got %d", f.Params.BytesPerSec)
		}
	case FaultRetryStorm:
		if f.Params.FailFirst <= 0 {
			return fmt.Errorf("retryStorm fault requires fail_first > 0, got %d", f.Params.FailFirst)
		}
		if f.Params.StatusCode < 100 || f.Params.StatusCode > 599 {
			return fmt.Errorf("retryStorm fault requires status_code in [100,599], got %d", f.Params.StatusCode)
		}
		if f.Params.WindowSec <= 0 {
			return fmt.Errorf("retryStorm fault requires window_sec > 0, got %d", f.Params.WindowSec)
		}
		switch {
		case f.Params.KeyBy == "path":
		case strings.HasPrefix(f.Params.KeyBy, "header:") && len(f.Params.KeyBy) > len("header:"):
		default:
			return fmt.Errorf("retryStorm key_by must be \"path\" or \"header:<Name>\", got %q", f.Params.KeyBy)
		}
	default:
		return fmt.Errorf("unknown fault type %q", f.Type)
	}
	return nil
}

// FaultProfile is a named, reusable set of faults attachable to rule buckets.
type FaultProfile struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`
	Faults      []Fault `json:"faults"`
}

func (p FaultProfile) Validate() error {
	if p.ID == "" {
		return fmt.Errorf("fault profile id is required")
	}
	if len(p.Faults) == 0 {
		return fmt.Errorf("fault profile must have at least one fault")
	}
	for i, f := range p.Faults {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("fault[%d]: %w", i, err)
		}
	}
	return nil
}

// --- AWS outgoing event interception ---------------------------------------

// Event service identifiers for intercepted AWS messaging publishes.
const (
	EventServiceSNS         = "sns"
	EventServiceSQS         = "sqs"
	EventServiceEventBridge = "eventbridge"
)

// Event is a normalized outgoing message intercepted from the app under test.
// Produced by the awsmsg parser; used for matching and capture.
type Event struct {
	Service    string            `json:"service"`               // sns | sqs | eventbridge
	Operation  string            `json:"operation"`             // Publish | SendMessage | PutEvents
	Target     string            `json:"target"`                // topic ARN | queue URL | event bus name
	Source     string            `json:"source,omitempty"`      // EventBridge source
	DetailType string            `json:"detail_type,omitempty"` // EventBridge detail-type
	Subject    string            `json:"subject,omitempty"`     // SNS subject
	Message    []byte            `json:"message"`               // SNS Message / SQS body / EB Detail
	Attributes map[string]string `json:"attributes,omitempty"`
	GroupID    string            `json:"group_id,omitempty"` // FIFO MessageGroupId
	DedupID    string            `json:"dedup_id,omitempty"` // FIFO MessageDeduplicationId
	Region     string            `json:"region,omitempty"`
	Identity   string            `json:"identity,omitempty"` // access key id of the publisher
	RawBody    []byte            `json:"-"`                  // original wire body (forward verbatim)
}

// EventRule declares how to match and handle an intercepted outgoing event.
// Separate from the HTTP Rule: its own store seam and admin endpoints.
type EventRule struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Disabled bool          `json:"disabled,omitempty"`
	Match    EventMatch    `json:"match"`
	Forward  *EventForward `json:"forward,omitempty"` // nil = capture + synthesized response
}

// EventMatch filters intercepted events. Empty fields are wildcards.
type EventMatch struct {
	Service    string            `json:"service"`               // required: sns|sqs|eventbridge
	Operation  string            `json:"operation,omitempty"`   // optional
	Target     string            `json:"target,omitempty"`      // glob (path.Match)
	Source     string            `json:"source,omitempty"`      // EventBridge, glob
	DetailType string            `json:"detail_type,omitempty"` // EventBridge, glob
	Attributes map[string]string `json:"attributes,omitempty"`  // exact
	Message    map[string]string `json:"message,omitempty"`     // JSONPath → expected scalar
}

// EventForward configures optional re-signed forwarding to the real broker.
// Used from Phase 3 onward; validated here so configs are forward-compatible.
type EventForward struct {
	Endpoint   string `json:"endpoint,omitempty"` // "" = default AWS endpoint for Region
	Region     string `json:"region,omitempty"`
	Credential string `json:"credential,omitempty"` // "" | "default" | "profile:<n>" | "static:<n>"
	DelayMs    int    `json:"delay_ms,omitempty"`   // extra latency (ms) added after the forward call returns (additive, applied even on error)
}

// Validate reports whether the event rule is well-formed.
func (e EventRule) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event rule id is required")
	}
	switch e.Match.Service {
	case EventServiceSNS, EventServiceSQS, EventServiceEventBridge:
	default:
		return fmt.Errorf("event rule match.service must be one of sns|sqs|eventbridge, got %q", e.Match.Service)
	}
	if e.Forward != nil {
		if err := e.Forward.Validate(); err != nil {
			return fmt.Errorf("forward: %w", err)
		}
	}
	return nil
}

// Validate reports whether the forward config is well-formed.
func (f EventForward) Validate() error {
	if f.DelayMs < 0 {
		return fmt.Errorf("delay_ms must be >= 0, got %d", f.DelayMs)
	}
	return nil
}
