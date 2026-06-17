// Package matched captures requests that matched a rule so testers can later
// retrieve, via the admin API, the exact request a system-under-test sent to
// the mock. Capture is best-effort and never blocks or fails a request.
package matched

import (
	"time"

	"github.com/google/uuid"
)

// NewID returns a time-ordered unique id (UUID v7). v7 is lexicographically
// sortable by creation time, so ids double as pagination cursors.
func NewID() string {
	// uuid.NewV7 only errors if the system RNG fails; fall back to v4 then.
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// Request is the reduced shape stored in memory and persisted. Bodies are kept
// out of line (referenced by *BodyID) so list queries stay small.
type Request struct {
	ID      string    `json:"id"`      // UUID v7, time-ordered
	RuleID  string    `json:"rule_id"` // which rule matched
	At      time.Time `json:"at"`      // capture timestamp

	Protocol string            `json:"protocol"` // http|graphql|soap|grpc
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`

	// Identity is the publisher principal for captured events (the SigV4 access
	// key id). Empty for HTTP captures.
	Identity string `json:"identity,omitempty"`

	ResponseStatus  int               `json:"response_status"`
	ResponseHeaders map[string]string `json:"response_headers"`

	RequestBodyID  string `json:"request_body_id,omitempty"`
	ResponseBodyID string `json:"response_body_id,omitempty"`

	// TTL is the epoch-second expiry (At + globalTTL); used by stores with
	// native TTL. Zero means no expiry hint set.
	TTL int64 `json:"ttl,omitempty"`
}

// Expired reports whether the entry's TTL has passed relative to now.
// A zero TTL never expires.
func (r Request) Expired(now time.Time) bool {
	return r.TTL != 0 && now.Unix() >= r.TTL
}

// RequestBody / ResponseBody hold the out-of-line payloads, keyed by the
// matching Request.*BodyID.
type RequestBody struct {
	ID   string `json:"id"`
	Body []byte `json:"body"`
}

type ResponseBody struct {
	ID   string      `json:"id"`
	Body interface{} `json:"body"`
}

// FullRequest is a Request plus its resolved bodies, returned by the detail
// endpoint.
type FullRequest struct {
	Request
	RequestBody  []byte      `json:"request_body,omitempty"`
	ResponseBody interface{} `json:"response_body,omitempty"`
	// BodyWarning is set when a body could not be resolved (lazy-load failure).
	BodyWarning string `json:"body_warning,omitempty"`
}
