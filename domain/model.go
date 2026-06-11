package domain

import (
	"errors"
	"fmt"
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
}

// Fault types supported by FaultProfile.
const (
	FaultJitter = "jitter"
	FaultError  = "error"
)

// FaultParams holds type-specific fault parameters. One flat struct keeps the
// JSON shape simple; Validate enforces which fields each type requires.
type FaultParams struct {
	BaseDelayMs int               `json:"base_delay_ms,omitempty"` // jitter: fixed delay added to every affected request
	JitterMs    int               `json:"jitter_ms,omitempty"`    // jitter: random extra delay in [0, JitterMs)
	StatusCode  int               `json:"status_code,omitempty"`  // error: HTTP status to return
	Body        string            `json:"body,omitempty"`        // error: response body (raw string)
	Headers     map[string]string `json:"headers,omitempty"`     // error: response headers
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
			return fmt.Errorf("jitter fault requires baseDelayMs or jitterMs > 0")
		}
	case FaultError:
		if f.Params.StatusCode < 100 || f.Params.StatusCode > 599 {
			return fmt.Errorf("error fault requires statusCode in [100,599], got %d", f.Params.StatusCode)
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
