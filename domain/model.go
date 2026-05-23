package domain

import "fmt"

const (
	ActionSimulate = "simulate"
	ActionForward  = "forward"
)

// MatchCriteria defines conditions a request must satisfy for a rule to apply.
type MatchCriteria struct {
	Protocol string            `json:"protocol"` // http | graphql | soap | grpc
	Method   string            `json:"method"`   // GET, POST, etc. (HTTP only)
	Path     string            `json:"path"`     // glob: /users/*, /api/**
	Headers  map[string]string `json:"headers"`  // exact key/value
	Query    map[string]string `json:"query"`    // exact key/value (HTTP only)
	Body     map[string]string `json:"body"`     // JSONPath → exact value
}

// WeightedBucket is one branch in a rule's traffic split.
type WeightedBucket struct {
	Weight       int    `json:"weight"`        // relative weight, must be > 0
	Action       string `json:"action"`        // "simulate" | "forward"
	SimulationID string `json:"simulation_id"` // required when Action = "simulate"
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
	return nil
}

// Rule maps incoming requests to a set of weighted response buckets.
type Rule struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Match      MatchCriteria    `json:"match"`
	Buckets    []WeightedBucket `json:"buckets"`
	ForwardURL string           `json:"forward_url"`
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
	for i, b := range r.Buckets {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("bucket[%d]: %w", i, err)
		}
		if b.Action == ActionForward && r.ForwardURL == "" {
			return fmt.Errorf("rule has forward bucket but forward_url is empty")
		}
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
}
