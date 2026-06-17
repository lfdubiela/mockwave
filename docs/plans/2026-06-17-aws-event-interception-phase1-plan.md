# AWS Event Interception — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Intercept the app-under-test's AWS SNS `Publish` calls on the mock port, match them against a separate `EventRule` config, capture them to state for assertion, and synthesize a valid SNS XML response so the SDK succeeds.

**Architecture:** A new inbound adapter `internal/adapters/in/awsmsg` is dispatched from the existing `protocolMux` when a request carries AWS SigV4 / `X-Amz-Target` markers. It parses the publish into a normalized `domain.Event`, matches it with `internal/domain/eventroute` (reusing the existing glob + JSONPath helpers, extracted into `internal/domain/jsonpath`), captures it into a dedicated `matched.Buffer` instance (reusing the matched-capture machinery), and writes a protocol-faithful XML response. Event rules and captures get their own admin endpoints, isolated from HTTP rules. SQS / EventBridge and re-signed forward are later phases.

**Tech Stack:** Go 1.26, `net/http`, `aws-sdk-go-v2/service/sns` (test-only contract client), stretchr/testify. Tests: `make test` (`go test ./... -race`); coverage gate ≥80% (`make coverage`).

**Spec:** [`docs/specs/2026-06-17-aws-event-interception-design.md`](../specs/2026-06-17-aws-event-interception-design.md)

---

## File Structure

- `domain/model.go` — MODIFY: add `Event`, `EventRule`, `EventMatch`, `EventForward`, service constants, `Validate()` methods; add `EventRules` to `Config`.
- `internal/domain/jsonpath/jsonpath.go` — CREATE: exported `Resolve` + `LeafToString` (extracted from the HTTP matcher; single source of truth).
- `internal/domain/matching/matcher.go` — MODIFY: call `jsonpath.Resolve` / `jsonpath.LeafToString` instead of the local copies.
- `internal/domain/eventroute/matcher.go` — CREATE: `Matcher` over `[]domain.EventRule` (service/target/source/detail-type globs + attributes + JSONPath message), first-match by specificity.
- `internal/adapters/in/awsmsg/detect.go` — CREATE: `Detect` (SigV4 credential scope + `X-Amz-Target`) → service/region/identity.
- `internal/adapters/in/awsmsg/parse_sns.go` — CREATE: SNS form body → `domain.Event`.
- `internal/adapters/in/awsmsg/respond_sns.go` — CREATE: SNS `PublishResponse` XML.
- `internal/adapters/in/awsmsg/handler.go` — CREATE: orchestrates detect → read → parse → match → capture → respond.
- `internal/matched/request.go` — MODIFY: add `Identity` field.
- `server/events.go` — CREATE: `EventConfig` + `resolveEventConfig` (mirrors `server/matched.go`); `eventQuery` capture helper.
- `server/server.go` — MODIFY: `Server` fields + event buffer/syncer/matcher wiring; `captureEvent`; load event rules in `rebuild`; inject `awsmsg` into `protocolMux`.
- `store/store.go` — MODIFY: add `EventRuleStore` optional interface.
- `internal/adapters/out/jsonfile/store.go` — MODIFY: implement `EventRuleStore`.
- `internal/adapters/cfg/restapi/server.go` — MODIFY: `WithEventCapture` option, `adminAPI.eventCaptureBuf`, routes for `/api/event-rules` + `/api/event-captures`.
- `internal/adapters/cfg/restapi/event_handlers.go` — CREATE: event-rule CRUD + event-capture list/detail/delete handlers.
- `server/admin.go` — MODIFY: pass `WithEventCapture` in `adminMuxOptions`.
- `cmd/mockwave/main.go` — MODIFY: `--aws` protocol token + `--event-capture` flags; pass `EventConfig`.
- `tests/integration/event_capture_test.go` — CREATE: e2e with the real SNS SDK.

---

## Task 1: Domain types — Event, EventRule, EventMatch, EventForward

**Files:**
- Modify: `domain/model.go`
- Test: `domain/event_test.go`

- [ ] **Step 1: Write the failing test**

Create `domain/event_test.go`:

```go
package domain

import (
	"encoding/json"
	"testing"
)

func TestEventRuleValidate(t *testing.T) {
	cases := []struct {
		name    string
		rule    EventRule
		wantErr bool
	}{
		{"ok sns", EventRule{ID: "r1", Match: EventMatch{Service: EventServiceSNS}}, false},
		{"ok sqs", EventRule{ID: "r2", Match: EventMatch{Service: EventServiceSQS}}, false},
		{"ok eventbridge", EventRule{ID: "r3", Match: EventMatch{Service: EventServiceEventBridge}}, false},
		{"missing id", EventRule{Match: EventMatch{Service: EventServiceSNS}}, true},
		{"bad service", EventRule{ID: "r4", Match: EventMatch{Service: "kinesis"}}, true},
		{"negative delay", EventRule{ID: "r5", Match: EventMatch{Service: EventServiceSNS}, Forward: &EventForward{DelayMs: -1}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.rule.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestConfigEventRulesRoundTrip(t *testing.T) {
	in := Config{EventRules: []EventRule{{
		ID:    "publish-orders",
		Name:  "Order events",
		Match: EventMatch{Service: EventServiceSNS, Target: "arn:aws:sns:*:*:orders"},
	}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Config
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.EventRules) != 1 || out.EventRules[0].ID != "publish-orders" {
		t.Fatalf("round-trip lost event rules: %+v", out.EventRules)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./domain/ -run TestEventRule -v`
Expected: FAIL — `undefined: EventRule` / `EventServiceSNS`.

- [ ] **Step 3: Write minimal implementation**

Append to `domain/model.go` (end of file):

```go
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
	Endpoint   string `json:"endpoint,omitempty"`   // "" = default AWS endpoint for Region
	Region     string `json:"region,omitempty"`
	Credential string `json:"credential,omitempty"` // "" | "default" | "profile:<n>" | "static:<n>"
	DelayMs    int    `json:"delay_ms,omitempty"`
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
```

Then add the `EventRules` field to the existing `Config` struct (around `domain/model.go:152-160`):

```go
type Config struct {
	Rules       []Rule       `json:"rules"`
	Simulations []Simulation `json:"simulations"`

	FaultProfiles []FaultProfile `json:"fault_profiles,omitempty"`

	Scenarios []Scenario `json:"scenarios,omitempty"`

	EventRules []EventRule `json:"event_rules,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./domain/ -run 'TestEventRule|TestConfigEventRules' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add domain/model.go domain/event_test.go
git commit -m "feat(events): domain types for AWS event interception"
```

---

## Task 2: Extract JSONPath helpers into a shared package

The HTTP matcher's `resolvePath` / `leafToString` are unexported in `internal/domain/matching`. The event matcher needs the same logic. Extract once (DRY); refactor the HTTP matcher to use it.

**Files:**
- Create: `internal/domain/jsonpath/jsonpath.go`
- Create: `internal/domain/jsonpath/jsonpath_test.go`
- Modify: `internal/domain/matching/matcher.go`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/jsonpath/jsonpath_test.go`:

```go
package jsonpath

import (
	"encoding/json"
	"testing"
)

func TestResolveAndLeaf(t *testing.T) {
	var root interface{}
	_ = json.Unmarshal([]byte(`{"order":{"id":7,"items":["a","b"],"paid":true}}`), &root)

	cases := []struct {
		expr string
		ok   bool
		leaf string
	}{
		{"$.order.id", true, "7"},
		{"order.id", true, "7"},
		{"$.order.items.1", true, "b"},
		{"$.order.paid", true, "true"},
		{"$.order.missing", false, ""},
		{"$.order", false, ""}, // non-leaf
	}
	for _, c := range cases {
		leaf, ok := Resolve(root, c.expr)
		if ok != c.ok {
			t.Fatalf("Resolve(%q) ok = %v, want %v", c.expr, ok, c.ok)
		}
		if ok && LeafToString(leaf) != c.leaf {
			t.Fatalf("Resolve(%q) leaf = %q, want %q", c.expr, LeafToString(leaf), c.leaf)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/jsonpath/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/domain/jsonpath/jsonpath.go` (move the bodies verbatim from `internal/domain/matching/matcher.go`, exported):

```go
// Package jsonpath resolves dotted JSONPath-lite expressions against decoded
// JSON values. Shared by the HTTP rule matcher and the event matcher.
package jsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

// Resolve walks expr (e.g. "$.order.id" or "order.items.0") against root and
// returns the leaf scalar. ok is false when the path is missing or lands on a
// container (map/array) rather than a scalar.
func Resolve(root interface{}, expr string) (interface{}, bool) {
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, false
	}
	cur := root
	for _, seg := range strings.Split(expr, ".") {
		switch node := cur.(type) {
		case map[string]interface{}:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []interface{}:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	switch cur.(type) {
	case map[string]interface{}, []interface{}:
		return nil, false
	}
	return cur, true
}

// LeafToString renders a resolved scalar as a string for exact comparison.
func LeafToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
```

Then in `internal/domain/matching/matcher.go`: delete the local `resolvePath` and `leafToString` functions, add the import `"github.com/mockwave/mockwave/internal/domain/jsonpath"`, and replace the two call sites in `matchRule` (around `matcher.go:71-77`):

```go
		leaf, ok := jsonpath.Resolve(parsed, pathExpr)
		if !ok || jsonpath.LeafToString(leaf) != want {
			return false
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/jsonpath/ ./internal/domain/matching/ -v`
Expected: PASS — the new jsonpath tests AND the existing matcher tests stay green (proves the refactor preserved behavior).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/jsonpath/ internal/domain/matching/matcher.go
git commit -m "refactor(matching): extract shared jsonpath helpers"
```

---

## Task 3: Event matcher (eventroute)

**Files:**
- Create: `internal/domain/eventroute/matcher.go`
- Test: `internal/domain/eventroute/matcher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/eventroute/matcher_test.go`:

```go
package eventroute

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func ev() domain.Event {
	return domain.Event{
		Service:    domain.EventServiceSNS,
		Operation:  "Publish",
		Target:     "arn:aws:sns:us-east-1:123:orders",
		Message:    []byte(`{"type":"created","total":42}`),
		Attributes: map[string]string{"env": "prod"},
	}
}

func TestMatch(t *testing.T) {
	rules := []domain.EventRule{
		{ID: "any-sns", Match: domain.EventMatch{Service: domain.EventServiceSNS}},
		{ID: "orders", Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders"}},
		{ID: "orders-created", Match: domain.EventMatch{
			Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders",
			Message: map[string]string{"$.type": "created"},
		}},
	}
	m := NewMatcher(rules)

	// Most specific (message + target) wins over target-only and service-only.
	if got := m.Match(ev()); got == nil || got.ID != "orders-created" {
		t.Fatalf("Match = %v, want orders-created", got)
	}

	// Attribute mismatch falls through specificity.
	e := ev()
	e.Attributes = map[string]string{"env": "dev"}
	r := []domain.EventRule{{ID: "prod-only", Match: domain.EventMatch{Service: domain.EventServiceSNS, Attributes: map[string]string{"env": "prod"}}}}
	if got := NewMatcher(r).Match(e); got != nil {
		t.Fatalf("Match = %v, want nil (attr mismatch)", got)
	}

	// Disabled rules never match.
	d := []domain.EventRule{{ID: "off", Disabled: true, Match: domain.EventMatch{Service: domain.EventServiceSNS}}}
	if got := NewMatcher(d).Match(ev()); got != nil {
		t.Fatalf("Match = %v, want nil (disabled)", got)
	}

	// Wrong service: no match.
	e2 := ev()
	e2.Service = domain.EventServiceSQS
	if got := m.Match(e2); got != nil {
		t.Fatalf("Match = %v, want nil (service mismatch)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/eventroute/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/domain/eventroute/matcher.go`:

```go
// Package eventroute matches normalized outgoing events against event rules.
// First match wins, rules ordered most-specific first. Pure domain logic.
package eventroute

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/jsonpath"
)

// Matcher resolves the first matching event rule for an event.
type Matcher struct {
	rules []domain.EventRule
}

// NewMatcher drops disabled rules and sorts the rest most-specific first.
func NewMatcher(rules []domain.EventRule) *Matcher {
	active := make([]domain.EventRule, 0, len(rules))
	for _, r := range rules {
		if !r.Disabled {
			active = append(active, r)
		}
	}
	// Stable insertion sort by descending specificity (small rule sets).
	for i := 1; i < len(active); i++ {
		for j := i; j > 0 && specificity(active[j]) > specificity(active[j-1]); j-- {
			active[j], active[j-1] = active[j-1], active[j]
		}
	}
	return &Matcher{rules: active}
}

// Match returns the first matching rule, or nil.
func (m *Matcher) Match(ev domain.Event) *domain.EventRule {
	for i := range m.rules {
		if matchEvent(&m.rules[i], ev) {
			return &m.rules[i]
		}
	}
	return nil
}

// specificity counts the constraints a rule imposes; higher = checked first.
func specificity(r domain.EventRule) int {
	mc := r.Match
	n := 0
	for _, s := range []string{mc.Operation, mc.Target, mc.Source, mc.DetailType} {
		if s != "" {
			n++
		}
	}
	n += len(mc.Attributes) + len(mc.Message)
	return n
}

func matchEvent(r *domain.EventRule, ev domain.Event) bool {
	mc := r.Match
	if mc.Service != "" && mc.Service != ev.Service {
		return false
	}
	if mc.Operation != "" && !strings.EqualFold(mc.Operation, ev.Operation) {
		return false
	}
	if !globOK(mc.Target, ev.Target) || !globOK(mc.Source, ev.Source) || !globOK(mc.DetailType, ev.DetailType) {
		return false
	}
	for k, v := range mc.Attributes {
		if ev.Attributes[k] != v {
			return false
		}
	}
	if len(mc.Message) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(ev.Message, &parsed); err != nil {
			return false
		}
		for expr, want := range mc.Message {
			leaf, ok := jsonpath.Resolve(parsed, expr)
			if !ok || jsonpath.LeafToString(leaf) != want {
				return false
			}
		}
	}
	return true
}

// globOK reports whether pattern (empty = wildcard) matches value.
func globOK(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/eventroute/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/eventroute/
git commit -m "feat(events): event rule matcher"
```

---

## Task 4: Service detection (awsmsg.Detect)

**Files:**
- Create: `internal/adapters/in/awsmsg/detect.go`
- Test: `internal/adapters/in/awsmsg/detect_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/in/awsmsg/detect_test.go`:

```go
package awsmsg

import (
	"net/http"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func req(auth, target string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "http://mock/", nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if target != "" {
		r.Header.Set("X-Amz-Target", target)
	}
	return r
}

func TestDetect(t *testing.T) {
	snsAuth := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260101/us-east-1/sns/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc"
	if d := Detect(req(snsAuth, "")); d.Service != domain.EventServiceSNS || d.Region != "us-east-1" || d.Identity != "AKIDEXAMPLE" {
		t.Fatalf("sns detect = %+v", d)
	}

	// EventBridge scope service is "events"; X-Amz-Target confirms.
	ebAuth := "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-west-2/events/aws4_request, SignedHeaders=host, Signature=x"
	if d := Detect(req(ebAuth, "AWSEvents.PutEvents")); d.Service != domain.EventServiceEventBridge {
		t.Fatalf("eventbridge detect = %+v", d)
	}

	// SQS via X-Amz-Target (JSON protocol).
	sqsAuth := "AWS4-HMAC-SHA256 Credential=AKID/20260101/eu-west-1/sqs/aws4_request, SignedHeaders=host, Signature=x"
	if d := Detect(req(sqsAuth, "AmazonSQS.SendMessage")); d.Service != domain.EventServiceSQS {
		t.Fatalf("sqs detect = %+v", d)
	}

	// Non-AWS request.
	if d := Detect(req("", "")); d.Service != "" {
		t.Fatalf("non-aws detect = %+v, want empty service", d)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestDetect -v`
Expected: FAIL — `undefined: Detect`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/adapters/in/awsmsg/detect.go`:

```go
// Package awsmsg intercepts AWS messaging publish calls (SNS/SQS/EventBridge)
// arriving on the mock port, normalizes them, and synthesizes valid responses.
package awsmsg

import (
	"net/http"
	"strings"

	"github.com/mockwave/mockwave/domain"
)

// DetectResult carries the AWS service and signer identity resolved from a
// request. Service is "" when the request is not an AWS messaging publish.
type DetectResult struct {
	Service  string // sns | sqs | eventbridge | ""
	Region   string
	Identity string // access key id from the SigV4 credential
}

// Detect inspects request headers for AWS SigV4 / X-Amz-Target markers.
func Detect(r *http.Request) DetectResult {
	res := parseAuthScope(r.Header.Get("Authorization"))
	switch target := r.Header.Get("X-Amz-Target"); {
	case strings.HasPrefix(target, "AmazonSQS."):
		res.Service = domain.EventServiceSQS
	case strings.HasPrefix(target, "AWSEvents."):
		res.Service = domain.EventServiceEventBridge
	}
	return res
}

// parseAuthScope reads "Credential=<akid>/<date>/<region>/<service>/aws4_request"
// from a SigV4 Authorization header and maps the AWS service token to our
// service constant.
func parseAuthScope(auth string) DetectResult {
	var res DetectResult
	const marker = "Credential="
	i := strings.Index(auth, marker)
	if i < 0 {
		return res
	}
	cred := auth[i+len(marker):]
	if c := strings.IndexByte(cred, ','); c >= 0 {
		cred = cred[:c]
	}
	parts := strings.Split(strings.TrimSpace(cred), "/")
	if len(parts) < 5 {
		return res
	}
	res.Identity = parts[0]
	res.Region = parts[2]
	switch parts[3] {
	case "sns":
		res.Service = domain.EventServiceSNS
	case "sqs":
		res.Service = domain.EventServiceSQS
	case "events":
		res.Service = domain.EventServiceEventBridge
	}
	return res
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestDetect -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/detect.go internal/adapters/in/awsmsg/detect_test.go
git commit -m "feat(events): AWS service detection from SigV4 scope"
```

---

## Task 5: SNS parser

**Files:**
- Create: `internal/adapters/in/awsmsg/parse_sns.go`
- Test: `internal/adapters/in/awsmsg/parse_sns_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/in/awsmsg/parse_sns_test.go`:

```go
package awsmsg

import (
	"net/url"
	"testing"
)

func TestParseSNS(t *testing.T) {
	form := url.Values{}
	form.Set("Action", "Publish")
	form.Set("TopicArn", "arn:aws:sns:us-east-1:123:orders")
	form.Set("Subject", "new order")
	form.Set("Message", `{"id":7}`)
	form.Set("MessageGroupId", "g1")
	form.Set("MessageDeduplicationId", "d1")
	form.Set("MessageAttributes.entry.1.Name", "env")
	form.Set("MessageAttributes.entry.1.Value.DataType", "String")
	form.Set("MessageAttributes.entry.1.Value.StringValue", "prod")

	ev, err := parseSNS(form)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Service != "sns" || ev.Operation != "Publish" {
		t.Fatalf("service/op = %q/%q", ev.Service, ev.Operation)
	}
	if ev.Target != "arn:aws:sns:us-east-1:123:orders" {
		t.Fatalf("target = %q", ev.Target)
	}
	if ev.Subject != "new order" || string(ev.Message) != `{"id":7}` {
		t.Fatalf("subject/message = %q/%q", ev.Subject, ev.Message)
	}
	if ev.GroupID != "g1" || ev.DedupID != "d1" {
		t.Fatalf("fifo = %q/%q", ev.GroupID, ev.DedupID)
	}
	if ev.Attributes["env"] != "prod" {
		t.Fatalf("attributes = %v", ev.Attributes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseSNS -v`
Expected: FAIL — `undefined: parseSNS`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/adapters/in/awsmsg/parse_sns.go`:

```go
package awsmsg

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/mockwave/mockwave/domain"
)

// parseSNS converts an SNS Publish form body into a normalized Event.
func parseSNS(form url.Values) (domain.Event, error) {
	ev := domain.Event{
		Service:    domain.EventServiceSNS,
		Operation:  form.Get("Action"),
		Target:     firstNonEmpty(form.Get("TopicArn"), form.Get("TargetArn"), form.Get("PhoneNumber")),
		Subject:    form.Get("Subject"),
		Message:    []byte(form.Get("Message")),
		GroupID:    form.Get("MessageGroupId"),
		DedupID:    form.Get("MessageDeduplicationId"),
		Attributes: parseSNSAttributes(form),
	}
	if ev.Operation == "" {
		return domain.Event{}, fmt.Errorf("awsmsg: SNS request missing Action")
	}
	return ev, nil
}

// parseSNSAttributes reads MessageAttributes.entry.N.{Name,Value.StringValue}.
func parseSNSAttributes(form url.Values) map[string]string {
	var out map[string]string
	for i := 1; ; i++ {
		name := form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Name", i))
		if name == "" {
			break
		}
		val := form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Value.StringValue", i))
		if out == nil {
			out = map[string]string{}
		}
		out[name] = val
		_ = strconv.Itoa // keep imports stable if loop body shrinks
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

> Note: remove the `strconv` import and the `_ = strconv.Itoa` line if your linter flags it — they are only there to keep the example compiling if you trim the loop. The canonical version drops both.

Canonical `parse_sns.go` without the filler (use this):

```go
package awsmsg

import (
	"fmt"
	"net/url"

	"github.com/mockwave/mockwave/domain"
)

func parseSNS(form url.Values) (domain.Event, error) {
	ev := domain.Event{
		Service:    domain.EventServiceSNS,
		Operation:  form.Get("Action"),
		Target:     firstNonEmpty(form.Get("TopicArn"), form.Get("TargetArn"), form.Get("PhoneNumber")),
		Subject:    form.Get("Subject"),
		Message:    []byte(form.Get("Message")),
		GroupID:    form.Get("MessageGroupId"),
		DedupID:    form.Get("MessageDeduplicationId"),
		Attributes: parseSNSAttributes(form),
	}
	if ev.Operation == "" {
		return domain.Event{}, fmt.Errorf("awsmsg: SNS request missing Action")
	}
	return ev, nil
}

func parseSNSAttributes(form url.Values) map[string]string {
	var out map[string]string
	for i := 1; ; i++ {
		name := form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Name", i))
		if name == "" {
			break
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Value.StringValue", i))
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseSNS -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/parse_sns.go internal/adapters/in/awsmsg/parse_sns_test.go
git commit -m "feat(events): SNS publish parser"
```

---

## Task 6: SNS responder

**Files:**
- Create: `internal/adapters/in/awsmsg/respond_sns.go`
- Test: `internal/adapters/in/awsmsg/respond_sns_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/in/awsmsg/respond_sns_test.go`:

```go
package awsmsg

import (
	"encoding/xml"
	"net/http/httptest"
	"testing"
)

func TestRespondSNS(t *testing.T) {
	rec := httptest.NewRecorder()
	respondSNS(rec, "msg-1", "req-1")

	if ct := rec.Header().Get("Content-Type"); ct != "text/xml" {
		t.Fatalf("content-type = %q", ct)
	}
	var out struct {
		MessageID string `xml:"PublishResult>MessageId"`
		RequestID string `xml:"ResponseMetadata>RequestId"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not valid XML: %v\n%s", err, rec.Body.String())
	}
	if out.MessageID != "msg-1" || out.RequestID != "req-1" {
		t.Fatalf("ids = %q/%q", out.MessageID, out.RequestID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondSNS -v`
Expected: FAIL — `undefined: respondSNS`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/adapters/in/awsmsg/respond_sns.go`:

```go
package awsmsg

import (
	"fmt"
	"net/http"
)

// respondSNS writes a valid SNS PublishResponse so the caller's SDK succeeds.
func respondSNS(w http.ResponseWriter, messageID, requestID string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w,
		`<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/">`+
			`<PublishResult><MessageId>%s</MessageId></PublishResult>`+
			`<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>`+
			`</PublishResponse>`,
		messageID, requestID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondSNS -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/respond_sns.go internal/adapters/in/awsmsg/respond_sns_test.go
git commit -m "feat(events): SNS response synthesis"
```

---

## Task 7: awsmsg Handler (orchestration)

**Files:**
- Create: `internal/adapters/in/awsmsg/handler.go`
- Test: `internal/adapters/in/awsmsg/handler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/in/awsmsg/handler_test.go`:

```go
package awsmsg

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

type fakeMatcher struct{ id string }

func (f fakeMatcher) Match(ev domain.Event) *domain.EventRule {
	if f.id == "" {
		return nil
	}
	return &domain.EventRule{ID: f.id}
}

func TestHandlerSNS(t *testing.T) {
	var captured *domain.Event
	var capturedRule, capturedMsgID string
	h := NewHandler(
		func() Matcher { return fakeMatcher{id: "orders"} },
		func(ev domain.Event, ruleID, messageID string) {
			captured = &ev
			capturedRule = ruleID
			capturedMsgID = messageID
		},
		func() string { return "fixed-id" },
	)

	form := url.Values{}
	form.Set("Action", "Publish")
	form.Set("TopicArn", "arn:aws:sns:us-east-1:1:orders")
	form.Set("Message", `{"id":1}`)

	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSNS, Region: "us-east-1", Identity: "AKID"})

	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<MessageId>fixed-id</MessageId>") {
		t.Fatalf("response code=%d body=%s", rec.Code, rec.Body.String())
	}
	if captured == nil || captured.Target != "arn:aws:sns:us-east-1:1:orders" {
		t.Fatalf("captured = %+v", captured)
	}
	if captured.Identity != "AKID" || captured.Region != "us-east-1" {
		t.Fatalf("identity/region not stamped: %+v", captured)
	}
	if capturedRule != "orders" || capturedMsgID != "fixed-id" {
		t.Fatalf("rule/msgid = %q/%q", capturedRule, capturedMsgID)
	}
}

func TestHandlerUnmatchedStillResponds(t *testing.T) {
	captureCalled := false
	h := NewHandler(
		func() Matcher { return fakeMatcher{id: ""} }, // no match
		func(domain.Event, string, string) { captureCalled = true },
		func() string { return "x" },
	)
	form := url.Values{"Action": {"Publish"}, "TopicArn": {"arn:aws:sns:::t"}, "Message": {"{}"}}
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(form.Encode()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSNS})
	if rec.Code != 200 {
		t.Fatalf("unmatched should still 200, got %d", rec.Code)
	}
	if captureCalled {
		t.Fatalf("unmatched event must not be captured")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestHandler -v`
Expected: FAIL — `undefined: NewHandler` / `Matcher`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/adapters/in/awsmsg/handler.go`:

```go
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
	body, _ := io.ReadAll(r.Body)

	var ev domain.Event
	var err error
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -v`
Expected: PASS (all awsmsg tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/handler.go internal/adapters/in/awsmsg/handler_test.go
git commit -m "feat(events): awsmsg handler orchestration (SNS)"
```

---

## Task 8: Add Identity field to matched.Request

**Files:**
- Modify: `internal/matched/request.go`
- Test: `internal/matched/request_test.go` (add a case)

- [ ] **Step 1: Write the failing test**

Append to `internal/matched/request_test.go` (create the file if absent):

```go
package matched

import (
	"encoding/json"
	"testing"
)

func TestRequestIdentityRoundTrip(t *testing.T) {
	r := Request{ID: "1", RuleID: "r", Protocol: "aws-sns", Identity: "AKIDEXAMPLE"}
	b, _ := json.Marshal(r)
	var out Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Identity != "AKIDEXAMPLE" {
		t.Fatalf("identity = %q", out.Identity)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run TestRequestIdentity -v`
Expected: FAIL — `unknown field Identity`.

- [ ] **Step 3: Write minimal implementation**

In `internal/matched/request.go`, add the field to the `Request` struct (after `Query`, before `ResponseStatus`):

```go
	// Identity is the publisher principal for captured events (the SigV4 access
	// key id). Empty for HTTP captures.
	Identity string `json:"identity,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run TestRequestIdentity -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matched/request.go internal/matched/request_test.go
git commit -m "feat(events): capture publisher identity on matched.Request"
```

---

## Task 9: EventConfig seam + capture mapping helper

**Files:**
- Create: `server/events.go`
- Test: `server/events_test.go`

- [ ] **Step 1: Write the failing test**

Create `server/events_test.go`:

```go
package server

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestResolveEventConfigDefaults(t *testing.T) {
	c := resolveEventConfig(EventConfig{Enabled: true})
	if c.BufferSize != 10000 || c.ttlSeconds() != 3600 {
		t.Fatalf("defaults = buffer %d ttl %ds", c.BufferSize, c.ttlSeconds())
	}
}

func TestEventQuery(t *testing.T) {
	ev := domain.Event{
		Source:     "billing",
		DetailType: "InvoicePaid",
		Subject:    "subj",
		Attributes: map[string]string{"env": "prod"},
	}
	q := eventQuery(ev)
	if q["source"] != "billing" || q["detail_type"] != "InvoicePaid" || q["subject"] != "subj" || q["attr.env"] != "prod" {
		t.Fatalf("query = %v", q)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run 'TestResolveEventConfig|TestEventQuery' -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Write minimal implementation**

Create `server/events.go` (mirrors `server/matched.go`):

```go
package server

import (
	"time"

	"github.com/mockwave/mockwave/domain"
)

// EventConfig configures AWS event interception + capture. Disabled by default.
// Explicit non-zero fields win; otherwise env vars fill in; otherwise defaults.
type EventConfig struct {
	Enabled      bool
	TTL          time.Duration // capture expiry; default 1h
	BufferSize   int           // in-memory capacity; default 10000
	SyncInterval time.Duration // write-behind cadence; default 30s
}

func resolveEventConfig(in EventConfig) EventConfig {
	out := in
	if !out.Enabled && getenv("MOCKWAVE_EVENT_CAPTURE") == "true" {
		out.Enabled = true
	}
	if out.TTL <= 0 {
		if v := envInt("MOCKWAVE_EVENT_TTL", 0); v > 0 {
			out.TTL = time.Duration(v) * time.Second
		} else {
			out.TTL = time.Hour
		}
	}
	if out.BufferSize <= 0 {
		out.BufferSize = envInt("MOCKWAVE_EVENT_BUFFER_SIZE", 10000)
	}
	if out.SyncInterval <= 0 {
		if v := envInt("MOCKWAVE_EVENT_SYNC_INTERVAL", 0); v > 0 {
			out.SyncInterval = time.Duration(v) * time.Second
		} else {
			out.SyncInterval = 30 * time.Second
		}
	}
	return out
}

func (c EventConfig) ttlSeconds() int { return int(c.TTL / time.Second) }

// eventQuery flattens the non-body event metadata into the matched.Request
// Query map so it is filterable/visible in the capture admin view.
func eventQuery(ev domain.Event) map[string]string {
	q := map[string]string{}
	if ev.Source != "" {
		q["source"] = ev.Source
	}
	if ev.DetailType != "" {
		q["detail_type"] = ev.DetailType
	}
	if ev.Subject != "" {
		q["subject"] = ev.Subject
	}
	if ev.GroupID != "" {
		q["group_id"] = ev.GroupID
	}
	if ev.DedupID != "" {
		q["dedup_id"] = ev.DedupID
	}
	for k, v := range ev.Attributes {
		q["attr."+k] = v
	}
	return q
}
```

Add a tiny `getenv` shim at the top of `server/events.go` only if `server` has no existing `os.Getenv` wrapper — otherwise use `os.Getenv` directly. Check `server/matched.go`: it uses `os.Getenv` inline. So replace `getenv(...)` above with `os.Getenv(...)` and add `"os"` to imports:

```go
import (
	"os"
	"time"

	"github.com/mockwave/mockwave/domain"
)
```
and `if !out.Enabled && os.Getenv("MOCKWAVE_EVENT_CAPTURE") == "true" {`.

`envInt` already exists in `server/matched.go` — reuse it (same package), do not redefine.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/ -run 'TestResolveEventConfig|TestEventQuery' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/events.go server/events_test.go
git commit -m "feat(events): event config seam and capture mapping helper"
```

---

## Task 10: EventRuleStore interface + jsonfile implementation

**Files:**
- Modify: `store/store.go`
- Modify: `internal/adapters/out/jsonfile/store.go`
- Test: `internal/adapters/out/jsonfile/event_rules_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/out/jsonfile/event_rules_test.go`:

```go
package jsonfile

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestEventRulesMemStore(t *testing.T) {
	st := NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{ID: "a", Match: domain.EventMatch{Service: "sns"}}},
	})
	got, err := st.GetEventRules()
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("GetEventRules = %v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/out/jsonfile/ -run TestEventRulesMemStore -v`
Expected: FAIL — `st.GetEventRules undefined`.

- [ ] **Step 3: Write minimal implementation**

In `store/store.go`, add the interface (after `ScenarioStore`):

```go
// EventRuleStore is an optional capability for stores that persist AWS event
// interception rules. GetEventRules returns the full set; Save upserts by id.
type EventRuleStore interface {
	GetEventRules() ([]domain.EventRule, error)
	SaveEventRule(r domain.EventRule) error
	DeleteEventRule(id string) error
}
```

In `internal/adapters/out/jsonfile/store.go`, add (mirroring `GetRules`/`SaveRule`/`DeleteRule`):

```go
func (s *Store) GetEventRules() ([]domain.EventRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.EventRule, len(s.config.EventRules))
	copy(out, s.config.EventRules)
	return out, nil
}

func (s *Store) SaveEventRule(r domain.EventRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.config.EventRules {
		if existing.ID == r.ID {
			s.config.EventRules[i] = r
			return s.flush()
		}
	}
	s.config.EventRules = append(s.config.EventRules, r)
	return s.flush()
}

func (s *Store) DeleteEventRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.config.EventRules {
		if existing.ID == id {
			s.config.EventRules = append(s.config.EventRules[:i], s.config.EventRules[i+1:]...)
			return s.flush()
		}
	}
	return fmt.Errorf("jsonfile: event rule %q not found", id)
}
```

> `NewMemStore` has an empty `path`, so `flush()` errors on write — matching the existing rule-CRUD behavior in tests (reads work, writes fail). That is intentional and consistent.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/out/jsonfile/ -run TestEventRulesMemStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add store/store.go internal/adapters/out/jsonfile/store.go internal/adapters/out/jsonfile/event_rules_test.go
git commit -m "feat(events): EventRuleStore interface + jsonfile impl"
```

---

## Task 11: Server wiring — event buffer, matcher, capture, mux branch

This is the integration task. It has no new unit test of its own (covered by Task 14's e2e), but each sub-step must compile and keep the existing suite green.

**Files:**
- Modify: `server/server.go`

- [ ] **Step 1: Add Server fields**

In the `Server` struct (`server/server.go:81`), add after the matched fields:

```go
	eventCaptureBuf    *matched.Buffer
	eventCaptureSyncer *matched.Syncer
	eventMatcher       *eventroute.Matcher // guarded by s.mu; swapped on rebuild
```

Add imports: `"github.com/mockwave/mockwave/internal/domain/eventroute"` and `awsmsg "github.com/mockwave/mockwave/internal/adapters/in/awsmsg"`.

- [ ] **Step 2: Build the event matcher during rebuild**

`rebuild()` already loads rules and swaps the pipeline under `s.mu`. Add event-rule loading. Find where `rebuild` assigns the new pipeline and, in the same critical section, add:

```go
	var eventRules []domain.EventRule
	if ers, ok := s.cfg.Store.(store.EventRuleStore); ok {
		if er, err := ers.GetEventRules(); err == nil {
			eventRules = er
		} else {
			return err
		}
	}
	// (inside the existing s.mu lock that swaps the pipeline)
	s.eventMatcher = eventroute.NewMatcher(eventRules)
```

> If `rebuild` builds new state then briefly locks to swap, build `eventroute.NewMatcher(eventRules)` alongside the new pipeline and assign it within the same lock. Mirror exactly how the pipeline is swapped.

- [ ] **Step 3: Initialize the event capture buffer/syncer in New()**

In `New()` (after the matched block, ~`server/server.go:155`), add:

```go
	if ec := resolveEventConfig(cfg.Event); ec.Enabled {
		s.cfg.Event = ec
		s.eventCaptureBuf = matched.NewBuffer(ec.BufferSize)
		if sink := matchedSink(MatchedConfig{Store: nil}, s.cfg.Store); sink != nil {
			if ms, ok := sink.(store.MatchedStore); ok {
				if page, err := ms.ListMatched(context.Background(), "", store.MatchedQuery{Limit: ec.BufferSize}); err == nil {
					s.eventCaptureBuf.Hydrate(filterEventCaptures(page.Items))
				}
			}
			s.eventCaptureSyncer = matched.NewSyncer(s.eventCaptureBuf, sink, ec.SyncInterval)
			go s.eventCaptureSyncer.Run(context.Background())
		}
	}
```

Add `Event EventConfig` to the `server.Config` struct (find `Config` in `server/server.go`, add the field next to `Matched MatchedConfig`).

Add the hydrate filter helper at the bottom of `server/events.go`:

```go
// filterEventCaptures keeps only aws-* protocol entries when hydrating the event
// buffer from a store shared with HTTP matched capture.
func filterEventCaptures(items []matched.Request) []matched.Request {
	out := items[:0:0]
	for _, r := range items {
		if strings.HasPrefix(r.Protocol, "aws-") {
			out = append(out, r)
		}
	}
	return out
}
```

Add imports `"strings"` and `"github.com/mockwave/mockwave/internal/matched"` to `server/events.go`.

- [ ] **Step 4: Add captureEvent + currentEventMatcher methods**

Add to `server/server.go`:

```go
// currentEventMatcher returns the active event matcher under the read lock.
func (s *Server) currentEventMatcher() awsmsg.Matcher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.eventMatcher == nil {
		return nil
	}
	return s.eventMatcher
}

// captureEvent records a matched intercepted event into the event buffer.
func (s *Server) captureEvent(ev domain.Event, ruleID, messageID string) {
	if s.eventCaptureBuf == nil {
		return
	}
	now := time.Now()
	r := matched.Request{
		ID:             matched.NewID(),
		RuleID:         ruleID,
		At:             now,
		Protocol:       "aws-" + ev.Service,
		Method:         ev.Operation,
		Path:           ev.Target,
		Query:          eventQuery(ev),
		Identity:       ev.Identity,
		ResponseStatus: 200,
	}
	if ttl := s.cfg.Event.ttlSeconds(); ttl > 0 {
		r.TTL = now.Add(time.Duration(ttl) * time.Second).Unix()
	}
	var reqBody []byte
	if len(ev.Message) > 0 {
		r.RequestBodyID = matched.NewID()
		reqBody = ev.Message
	}
	r.ResponseBodyID = matched.NewID()
	respBody := map[string]string{"messageId": messageID}
	s.eventCaptureBuf.Add(r, reqBody, respBody)
}
```

- [ ] **Step 5: Inject awsmsg into protocolMux**

Change `protocolMux` to hold an optional aws handler:

```go
type protocolMux struct {
	httpH *httprest.Handler
	gqlH  *graphqladapter.Handler
	soapH *soapadapter.Handler
	awsH  *awsmsg.Handler
}
```

In `ServeHTTP`, add the AWS branch BEFORE the SOAP/GraphQL checks (so a signed publish is never mistaken for HTTP):

```go
func (m *protocolMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.awsH != nil {
		if d := awsmsg.Detect(r); d.Service != "" {
			m.awsH.ServeHTTP(w, r, d)
			return
		}
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	// ... existing soap / graphql / http branches unchanged ...
}
```

In `MockHandler`, build the aws handler when "aws" is requested and capture is enabled:

```go
func (s *Server) MockHandler(protocols []string, exec Executor) http.Handler {
	httpH := httprest.NewHandler(exec)
	var gqlH *graphqladapter.Handler
	var soapH *soapadapter.Handler
	var awsH *awsmsg.Handler
	for _, p := range protocols {
		switch strings.ToLower(p) {
		case "graphql":
			gqlH = graphqladapter.NewHandler(exec)
		case "soap":
			soapH = soapadapter.NewHandler(exec)
		case "aws":
			if s.eventCaptureBuf != nil {
				awsH = awsmsg.NewHandler(s.currentEventMatcher, s.captureEvent, matched.NewID)
			}
		}
	}
	return &protocolMux{httpH: httpH, gqlH: gqlH, soapH: soapH, awsH: awsH}
}
```

- [ ] **Step 6: Verify it compiles and the suite stays green**

Run: `go build ./... && go test ./server/ ./internal/... -race`
Expected: PASS (no behavior change for non-AWS traffic; AWS path exercised in Task 14).

- [ ] **Step 7: Commit**

```bash
git add server/server.go server/events.go
git commit -m "feat(events): wire event capture, matcher, and awsmsg mux branch"
```

---

## Task 12: Admin endpoints — event-rules CRUD + event-captures

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go`
- Create: `internal/adapters/cfg/restapi/event_handlers.go`
- Modify: `server/admin.go`
- Test: `internal/adapters/cfg/restapi/event_handlers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/cfg/restapi/event_handlers_test.go`:

```go
package restapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
)

func TestEventCapturesList(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "1", RuleID: "orders", At: time.Now(), Protocol: "aws-sns", Method: "Publish", Path: "arn:orders"}, []byte(`{"id":1}`), nil)
	api := &adminAPI{eventCaptureBuf: buf}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/event-captures/orders", nil)
	api.eventCaptures(rec, r)

	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var page matched.Page
	_ = json.NewDecoder(rec.Body).Decode(&page)
	if len(page.Items) != 1 || page.Items[0].Path != "arn:orders" {
		t.Fatalf("page = %+v", page)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestEventCapturesList -v`
Expected: FAIL — `adminAPI.eventCaptureBuf` / `eventCaptures` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/adapters/cfg/restapi/server.go`:

Add the field to `adminAPI` (after `matchedBuf`, ~line 100):

```go
	eventCaptureBuf *matched.Buffer // may be nil — event capture disabled
```

Add the option (after `WithMatched`, ~line 44):

```go
// WithEventCapture enables the event-captures admin endpoints.
func WithEventCapture(buf *matched.Buffer) MuxOption {
	return func(a *adminAPI) { a.eventCaptureBuf = buf }
}
```

Register routes in `NewMux` (after the matched routes):

```go
	mux.HandleFunc("/api/event-rules", api.eventRules)
	mux.HandleFunc("/api/event-rules/", api.eventRuleByID)
	mux.HandleFunc("/api/event-captures/", api.eventCaptures)
```

Create `internal/adapters/cfg/restapi/event_handlers.go`:

```go
package restapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// eventRules handles GET (list) and POST (create) on /api/event-rules.
func (a *adminAPI) eventRules(w http.ResponseWriter, r *http.Request) {
	ers, ok := a.store.(store.EventRuleStore)
	if !ok {
		writeError(w, 501, "event rules not supported by this store")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rules, err := ers.GetEventRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if rules == nil {
			rules = []domain.EventRule{}
		}
		writeJSON(w, 200, rules)
	case http.MethodPost:
		var er domain.EventRule
		if err := json.NewDecoder(r.Body).Decode(&er); err != nil {
			writeError(w, 400, "invalid JSON: "+err.Error())
			return
		}
		if err := er.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := ers.SaveEventRule(er); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 201, er)
	default:
		writeError(w, 405, "method not allowed")
	}
}

// eventRuleByID handles PUT (update) and DELETE on /api/event-rules/{id}.
func (a *adminAPI) eventRuleByID(w http.ResponseWriter, r *http.Request) {
	ers, ok := a.store.(store.EventRuleStore)
	if !ok {
		writeError(w, 501, "event rules not supported by this store")
		return
	}
	id := idFromPath(r.URL.Path, "/api/event-rules/")
	if id == "" {
		writeError(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var er domain.EventRule
		if err := json.NewDecoder(r.Body).Decode(&er); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		er.ID = id
		if err := er.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := ers.SaveEventRule(er); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, er)
	case http.MethodDelete:
		if err := ers.DeleteEventRule(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method not allowed")
	}
}

// eventCaptures lists/deletes captured events. Path: /api/event-captures/{ruleID}[/{id}].
func (a *adminAPI) eventCaptures(w http.ResponseWriter, r *http.Request) {
	if a.eventCaptureBuf == nil {
		writeError(w, 404, "event capture is disabled")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/event-captures/"), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 1:
			a.eventCaptureList(w, r, parts[0])
		case 2:
			a.eventCaptureDetail(w, parts[0], parts[1])
		default:
			writeError(w, 404, "not found")
		}
	case http.MethodDelete:
		ruleID := ""
		if len(parts) >= 1 {
			ruleID = parts[0]
		}
		a.eventCaptureBuf.Clear(ruleID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) eventCaptureList(w http.ResponseWriter, r *http.Request, ruleID string) {
	q := r.URL.Query()
	mq := matched.Query{Cursor: q.Get("cursor"), Method: q.Get("method"), Path: q.Get("path")}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			mq.Limit = n
		}
	}
	page := a.eventCaptureBuf.List(ruleID, mq)
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	writeJSON(w, 200, page)
}

func (a *adminAPI) eventCaptureDetail(w http.ResponseWriter, ruleID, id string) {
	full, ok := a.eventCaptureBuf.Get(ruleID, id)
	if !ok {
		writeError(w, 404, "event capture not found")
		return
	}
	writeJSON(w, 200, full)
}
```

In `server/admin.go`, extend `adminMuxOptions()` (after the matched block):

```go
	if s.eventCaptureBuf != nil {
		opts = append(opts, restapi.WithEventCapture(s.eventCaptureBuf))
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestEventCapturesList -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/server.go internal/adapters/cfg/restapi/event_handlers.go internal/adapters/cfg/restapi/event_handlers_test.go server/admin.go
git commit -m "feat(events): admin endpoints for event rules and captures"
```

---

## Task 13: CLI flags — `--aws` protocol + event-capture knobs

**Files:**
- Modify: `cmd/mockwave/main.go`

- [ ] **Step 1: Add flag variables**

In `startCmd()`, next to the matched-capture vars, add:

```go
		eventCapture        bool
		eventTTL            int
		eventBufferSize     int
		eventSyncInterval   int
```

- [ ] **Step 2: Pass EventConfig into server.New**

In the `server.New(server.Config{...})` literal, add after the `Matched:` block:

```go
				Event: server.EventConfig{
					Enabled:      eventCapture,
					TTL:          time.Duration(eventTTL) * time.Second,
					BufferSize:   eventBufferSize,
					SyncInterval: time.Duration(eventSyncInterval) * time.Second,
				},
```

- [ ] **Step 3: Register flags**

After the matched-capture flags:

```go
	cmd.Flags().BoolVar(&eventCapture, "event-capture", false, "enable AWS event interception + capture (use with --protocols aws)")
	cmd.Flags().IntVar(&eventTTL, "event-ttl", 3600, "TTL in seconds for captured events")
	cmd.Flags().IntVar(&eventBufferSize, "event-buffer-size", 10000, "in-memory buffer size for event capture")
	cmd.Flags().IntVar(&eventSyncInterval, "event-sync-interval", 30, "sync interval in seconds for event capture")
```

Update the `--protocols` help text to mention `aws`:

```go
	cmd.Flags().StringVar(&protocolsStr, "protocols", "http", "comma-separated: http,graphql,soap,grpc,aws")
```

- [ ] **Step 4: Verify it builds**

Run: `go build ./... && go run ./cmd/mockwave start --help | grep -E 'event-capture|aws'`
Expected: build succeeds; the new flags and the updated protocols help appear.

- [ ] **Step 5: Commit**

```bash
git add cmd/mockwave/main.go
git commit -m "feat(events): CLI flags for AWS interception and event capture"
```

---

## Task 14: End-to-end test with the real SNS SDK

Proves the whole path: a genuine `aws-sdk-go-v2` SNS client (SigV4-signed, endpoint override) publishes to Mockwave, accepts the synthesized XML, and the event is captured and assertable via the admin API.

**Files:**
- Create: `tests/integration/event_capture_test.go`
- Modify: `go.mod` / `go.sum` (via `go get`)

- [ ] **Step 1: Add the SNS SDK test dependency**

Run:

```bash
go get github.com/aws/aws-sdk-go-v2/service/sns@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
```

Expected: `go.mod` gains the SNS service + credentials modules (aws-sdk-go-v2 core is already present via the DynamoDB adapter).

- [ ] **Step 2: Write the failing test**

Create `tests/integration/event_capture_test.go`:

```go
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
)

func TestE2E_SNSEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Name:  "Order events",
			Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders"},
		}},
	})
	srv, err := server.New(server.Config{
		Store: st,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// Real SNS SDK client pointed at Mockwave with static credentials.
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	client := sns.NewFromConfig(cfg, func(o *sns.Options) {
		o.BaseEndpoint = aws.String(mock.URL)
	})

	out, err := client.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:orders"),
		Message:  aws.String(`{"type":"created","total":42}`),
	})
	require.NoError(t, err, "SDK must accept the synthesized response")
	require.NotNil(t, out.MessageId)
	assert.NotEmpty(t, *out.MessageId)

	// The publish was captured and is assertable via the admin API.
	resp, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	item := page.Items[0]
	assert.Equal(t, "aws-sns", item.Protocol)
	assert.Equal(t, "Publish", item.Method)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:orders", item.Path)
	assert.Equal(t, "AKIDEXAMPLE", item.Identity)

	// Detail carries the published message body.
	detResp, err := http.Get(admin.URL + "/api/event-captures/orders/" + item.ID)
	require.NoError(t, err)
	defer detResp.Body.Close()
	require.Equal(t, 200, detResp.StatusCode)
	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.JSONEq(t, `{"type":"created","total":42}`, string(full.RequestBody))
}
```

- [ ] **Step 3: Run test to verify it fails (then passes)**

Run: `go test ./tests/integration/ -run TestE2E_SNSEventCapture -v`
Expected first run (if any wiring is incomplete): FAIL with a concrete assertion/compile error. Fix wiring until it PASSES. A green run proves: Detect → parseSNS → match → capture → respondSNS, and that the real SDK accepts the response.

- [ ] **Step 4: Run the full suite + coverage gate**

Run: `make test && make coverage`
Expected: all tests pass; coverage ≥80%. If `awsmsg` or `eventroute` coverage drags the total below 80%, add focused unit cases (e.g. SNS attribute edge cases, `globOK` error path) until the gate passes.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/event_capture_test.go go.mod go.sum
git commit -m "test(events): e2e SNS interception with the real AWS SDK"
```

---

## Task 15: Docs

**Files:**
- Create: `docs/event-capture.md`
- Modify: `README.md` (Features bullet + a short section)

- [ ] **Step 1: Write the guide**

Create `docs/event-capture.md` documenting: pointing an AWS SDK client at Mockwave (`BaseEndpoint` / `AWS_ENDPOINT_URL`), the `event_rules` config block, `--protocols aws --event-capture`, the `/api/event-rules` and `/api/event-captures` endpoints, and the SNS-only / no-forward Phase 1 limits (link `docs/roadmap.md`). Mirror the structure of `docs/matched-capture.md`.

- [ ] **Step 2: Update the README**

Add a Features bullet after the matched-capture bullet:

```markdown
- **Outgoing event capture (AWS)** — intercept the app's SNS publishes on the mock port, capture them to state for assertion, and return a valid SDK response. Point your AWS SDK client at Mockwave via endpoint override. See [`docs/event-capture.md`](docs/event-capture.md). (SQS/EventBridge + re-signed forward on the [roadmap](docs/roadmap.md).)
```

- [ ] **Step 3: Verify links resolve**

Run: `grep -n "event-capture" README.md docs/event-capture.md`
Expected: the new references appear and paths are correct.

- [ ] **Step 4: Commit**

```bash
git add docs/event-capture.md README.md
git commit -m "docs(events): SNS interception guide + README feature"
```

---

## Self-Review

**Spec coverage (Phase 1 scope):**
- Entry & service detection (SNS, single endpoint, SigV4 scope) → Tasks 4, 11. ✓
- Normalized event → Task 1. ✓
- Separate `EventRule` config + matching → Tasks 1, 3, 10. ✓
- Capture reusing MatchedStore machinery (mapping onto `matched.Request`, `aws-*` protocol, `Identity`) → Tasks 8, 9, 11. ✓
- Response synthesis (SNS XML) → Task 6. ✓
- Admin endpoints (event-rules, event-captures) → Task 12. ✓
- Port topology (protocolMux branch, gRPC/admin untouched) → Task 11. ✓
- Testing: unit (detect/parse/respond/matcher/jsonpath) + e2e SDK round-trip → Tasks 2–8, 14. ✓
- Out-of-scope deferred: SQS/EventBridge (Phase 2), forward (Phase 3), persistence on cloud backends (Phase 4) — tracked in the index + `docs/roadmap.md`. ✓

**Type consistency:** `domain.Event`/`EventRule`/`EventMatch`/`EventForward`, `EventServiceSNS|SQS|EventBridge`, `awsmsg.Matcher`/`CaptureFunc`/`Handler.ServeHTTP(w,r,DetectResult)`, `eventroute.Matcher.Match`, `jsonpath.Resolve`/`LeafToString`, `matched.Request.Identity`, `server.EventConfig`/`resolveEventConfig`/`eventQuery`/`captureEvent`/`currentEventMatcher`, `store.EventRuleStore`, `restapi.WithEventCapture`/`eventCaptureBuf`/`eventCaptures` — names are used identically across tasks. `eventroute.Matcher` satisfies `awsmsg.Matcher` (same `Match(domain.Event) *domain.EventRule` signature). ✓

**Placeholder scan:** the only "fill-in" note is the explicit guidance in Task 11 Step 2 to mirror the existing pipeline swap inside `s.mu` (the surrounding `rebuild` body isn't reproduced because it's existing code the worker reads in place) — every new symbol has complete code. The `strconv` filler in Task 5 is called out and replaced by the canonical version in the same step. ✓
