# AWS Event Interception — Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend AWS event interception to SQS `SendMessage` and EventBridge `PutEvents` (both AWS JSON protocol), including the SQS MD5 checksums the SDK validates, so all three services (SNS/SQS/EventBridge) are intercepted, captured, and answered with SDK-accepted responses.

**Architecture:** Phase 1 built `internal/adapters/in/awsmsg` (detect → parse SNS → respond) plus an `EventRule` matcher and in-memory capture; the handler currently returns 501 for SQS/EventBridge. Phase 2 adds per-service parsers (`parse_sqs.go`, `parse_eb.go`) and responders (`respond_sqs.go`, `respond_eb.go`), a faithful SQS MD5 module (`md5.go`), and refactors the handler to branch per service over a shared `matchAndCapture`/`stamp` helper. Detection, server wiring, capture, and admin endpoints already handle any AWS service generically — no changes needed there.

**Tech Stack:** Go 1.26, `net/http`, `encoding/json`, `crypto/md5`; test-only `aws-sdk-go-v2/service/sqs` + `aws-sdk-go-v2/service/eventbridge`. Tests: `make test` (`go test ./... -race`); coverage gate ≥80% (`make coverage`).

**Spec:** [`docs/specs/2026-06-17-aws-event-interception-design.md`](../specs/2026-06-17-aws-event-interception-design.md) (Response synthesis, MD5, PutEvents-array sections). Index: [`2026-06-17-aws-event-interception-index.md`](2026-06-17-aws-event-interception-index.md).

---

## Context an implementer needs

- `domain.Event` (already exists) fields: `Service, Operation, Target, Source, DetailType, Subject, Message []byte, Attributes map[string]string, GroupID, DedupID, Region, Identity, RawBody`.
- Service constants: `domain.EventServiceSNS = "sns"`, `domain.EventServiceSQS = "sqs"`, `domain.EventServiceEventBridge = "eventbridge"`.
- `awsmsg.Detect` already resolves SQS (scope `sqs` / `X-Amz-Target: AmazonSQS.*`) and EventBridge (scope `events` / `X-Amz-Target: AWSEvents.*`).
- `server.captureEvent` already maps ANY event onto `matched.Request` with `Protocol = "aws-"+ev.Service`. The protocolMux already dispatches any AWS request to the awsmsg handler. So once the handler parses+responds SQS/EB, capture and admin endpoints work unchanged.
- The matcher (`eventroute`) matches `Target`/`Source`/`DetailType` with `path.Match` globs. NOTE: `path.Match`'s `*` does not cross `/`. SNS topic ARNs (no `/`) glob fine; SQS queue URLs (with `/`) match by exact string or service-only — document this, don't try to fix it in Phase 2.

## File Structure

- `internal/adapters/in/awsmsg/md5.go` — CREATE: `MsgAttr`, `md5OfBody`, `md5OfAttributes` (AWS attribute-encoding algorithm).
- `internal/adapters/in/awsmsg/parse_sqs.go` — CREATE: SQS SendMessage JSON → `domain.Event` + `[]MsgAttr`.
- `internal/adapters/in/awsmsg/respond_sqs.go` — CREATE: SQS JSON response (MD5s + MessageId).
- `internal/adapters/in/awsmsg/parse_eb.go` — CREATE: PutEvents JSON → `[]domain.Event` (one per entry).
- `internal/adapters/in/awsmsg/respond_eb.go` — CREATE: PutEvents JSON response (one EventId per entry).
- `internal/adapters/in/awsmsg/handler.go` — MODIFY: branch per service; add `stamp` + `matchAndCapture` helpers; SNS path preserved.
- `internal/adapters/in/awsmsg/handler_test.go` — MODIFY: update the unsupported-service test; add SQS + EventBridge handler tests.
- `tests/integration/event_capture_test.go` — MODIFY: add real-SDK SQS + EventBridge e2e tests.
- `go.mod` / `go.sum` — MODIFY (via `go get`): add sqs + eventbridge service modules.
- `docs/event-capture.md`, `README.md`, `docs/roadmap.md`, plan index — MODIFY: reflect SQS/EventBridge support.

---

## Task 1: SQS MD5 module

**Files:**
- Create: `internal/adapters/in/awsmsg/md5.go`
- Test: `internal/adapters/in/awsmsg/md5_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/in/awsmsg/md5_test.go`:

```go
package awsmsg

import "testing"

func TestMD5OfBody(t *testing.T) {
	// Known vector: md5("hello") = 5d41402abc4b2a76b9719d911017c592
	if got := md5OfBody("hello"); got != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("md5OfBody = %q", got)
	}
	if got := md5OfBody(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("md5OfBody(empty) = %q", got)
	}
}

func TestMD5OfAttributes(t *testing.T) {
	if got := md5OfAttributes(nil); got != "" {
		t.Fatalf("empty attrs should be \"\", got %q", got)
	}
	a := []MsgAttr{
		{Name: "env", DataType: "String", StringValue: "prod"},
		{Name: "n", DataType: "Number", StringValue: "7"},
	}
	// Deterministic + order-independent (sorted by name internally).
	h1 := md5OfAttributes(a)
	reordered := []MsgAttr{a[1], a[0]}
	h2 := md5OfAttributes(reordered)
	if h1 == "" || h1 != h2 {
		t.Fatalf("attr md5 must be deterministic & order-independent: %q vs %q", h1, h2)
	}
	// Different attribute sets produce different digests.
	if md5OfAttributes([]MsgAttr{{Name: "env", DataType: "String", StringValue: "dev"}}) == h1 {
		t.Fatalf("different attrs must differ")
	}
	// 32-char hex.
	if len(h1) != 32 {
		t.Fatalf("expected 32-char hex, got %d", len(h1))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run 'TestMD5' -v`
Expected: FAIL — `undefined: md5OfBody`.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/in/awsmsg/md5.go`:

```go
package awsmsg

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"io"
	"sort"
	"strings"
)

// MsgAttr is a message attribute carrying the fields needed to compute the AWS
// MD5-of-message-attributes checksum the SQS SDK validates.
type MsgAttr struct {
	Name        string
	DataType    string
	StringValue string
	BinaryValue []byte
}

// md5OfBody returns the hex MD5 of a message body — what the SDK validates as
// MD5OfMessageBody.
func md5OfBody(body string) string {
	sum := md5.Sum([]byte(body))
	return hex.EncodeToString(sum[:])
}

// md5OfAttributes computes the AWS MD5-of-message-attributes digest: attributes
// sorted by name, each encoded as length-prefixed name, length-prefixed data
// type, a transport-type byte (1 = String/Number, 2 = Binary), then the
// length-prefixed value. Returns "" when there are no attributes.
func md5OfAttributes(attrs []MsgAttr) string {
	if len(attrs) == 0 {
		return ""
	}
	sorted := make([]MsgAttr, len(attrs))
	copy(sorted, attrs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	h := md5.New()
	for _, a := range sorted {
		writeLenPrefixed(h, []byte(a.Name))
		writeLenPrefixed(h, []byte(a.DataType))
		if strings.HasPrefix(a.DataType, "Binary") {
			h.Write([]byte{2})
			writeLenPrefixed(h, a.BinaryValue)
		} else {
			h.Write([]byte{1})
			writeLenPrefixed(h, []byte(a.StringValue))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeLenPrefixed(h io.Writer, b []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run 'TestMD5' -v`
Expected: PASS. (Authoritative correctness of `md5OfAttributes` is proven by the real-SDK e2e in Task 7, which rejects a wrong digest.)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/md5.go internal/adapters/in/awsmsg/md5_test.go
git commit -m "feat(events): SQS MD5 of body and message attributes"
```
No Co-Authored-By footer.

---

## Task 2: SQS parser

**Files:**
- Create: `internal/adapters/in/awsmsg/parse_sqs.go`
- Test: `internal/adapters/in/awsmsg/parse_sqs_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/in/awsmsg/parse_sqs_test.go`:

```go
package awsmsg

import "testing"

func TestParseSQS(t *testing.T) {
	body := []byte(`{
		"QueueUrl": "https://sqs.us-east-1.amazonaws.com/123/orders",
		"MessageBody": "{\"id\":7}",
		"MessageGroupId": "g1",
		"MessageDeduplicationId": "d1",
		"MessageAttributes": {"env": {"DataType": "String", "StringValue": "prod"}}
	}`)
	ev, attrs, err := parseSQS(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Service != "sqs" || ev.Operation != "SendMessage" {
		t.Fatalf("service/op = %q/%q", ev.Service, ev.Operation)
	}
	if ev.Target != "https://sqs.us-east-1.amazonaws.com/123/orders" {
		t.Fatalf("target = %q", ev.Target)
	}
	if string(ev.Message) != `{"id":7}` || ev.GroupID != "g1" || ev.DedupID != "d1" {
		t.Fatalf("message/fifo = %q/%q/%q", ev.Message, ev.GroupID, ev.DedupID)
	}
	if ev.Attributes["env"] != "prod" {
		t.Fatalf("attributes = %v", ev.Attributes)
	}
	if len(attrs) != 1 || attrs[0].Name != "env" || attrs[0].DataType != "String" || attrs[0].StringValue != "prod" {
		t.Fatalf("raw attrs = %+v", attrs)
	}
}

func TestParseSQSMissingQueueURL(t *testing.T) {
	if _, _, err := parseSQS([]byte(`{"MessageBody":"x"}`)); err == nil {
		t.Fatal("expected error when QueueUrl missing")
	}
	if _, _, err := parseSQS([]byte(`not json`)); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseSQS -v`
Expected: FAIL — `undefined: parseSQS`.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/in/awsmsg/parse_sqs.go`:

```go
package awsmsg

import (
	"encoding/json"
	"fmt"

	"github.com/mockwave/mockwave/domain"
)

type sqsSendMessage struct {
	QueueUrl               string                       `json:"QueueUrl"`
	MessageBody            string                       `json:"MessageBody"`
	MessageGroupId         string                       `json:"MessageGroupId"`
	MessageDeduplicationId string                       `json:"MessageDeduplicationId"`
	MessageAttributes      map[string]sqsAttributeValue `json:"MessageAttributes"`
}

type sqsAttributeValue struct {
	DataType    string `json:"DataType"`
	StringValue string `json:"StringValue"`
	BinaryValue []byte `json:"BinaryValue"` // JSON base64 → bytes
}

// parseSQS converts an SQS SendMessage JSON body into a normalized Event plus
// the raw message attributes the responder needs to compute MD5s.
func parseSQS(body []byte) (domain.Event, []MsgAttr, error) {
	var in sqsSendMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return domain.Event{}, nil, fmt.Errorf("awsmsg: SQS body: %w", err)
	}
	if in.QueueUrl == "" {
		return domain.Event{}, nil, fmt.Errorf("awsmsg: SQS request missing QueueUrl")
	}
	var attrs []MsgAttr
	var flat map[string]string
	for name, v := range in.MessageAttributes {
		attrs = append(attrs, MsgAttr{Name: name, DataType: v.DataType, StringValue: v.StringValue, BinaryValue: v.BinaryValue})
		if flat == nil {
			flat = map[string]string{}
		}
		flat[name] = v.StringValue
	}
	ev := domain.Event{
		Service:    domain.EventServiceSQS,
		Operation:  "SendMessage",
		Target:     in.QueueUrl,
		Message:    []byte(in.MessageBody),
		GroupID:    in.MessageGroupId,
		DedupID:    in.MessageDeduplicationId,
		Attributes: flat,
	}
	return ev, attrs, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseSQS -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/parse_sqs.go internal/adapters/in/awsmsg/parse_sqs_test.go
git commit -m "feat(events): SQS SendMessage parser"
```
No Co-Authored-By footer.

---

## Task 3: SQS responder

**Files:**
- Create: `internal/adapters/in/awsmsg/respond_sqs.go`
- Test: `internal/adapters/in/awsmsg/respond_sqs_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/in/awsmsg/respond_sqs_test.go`:

```go
package awsmsg

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestRespondSQS(t *testing.T) {
	rec := httptest.NewRecorder()
	respondSQS(rec, "hello", []MsgAttr{{Name: "env", DataType: "String", StringValue: "prod"}}, "msg-1")

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-amz-json-1.0" {
		t.Fatalf("content-type = %q", ct)
	}
	var out struct {
		MD5OfMessageBody       string  `json:"MD5OfMessageBody"`
		MD5OfMessageAttributes string  `json:"MD5OfMessageAttributes"`
		MessageId              string  `json:"MessageId"`
		SequenceNumber         *string `json:"SequenceNumber"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, rec.Body.String())
	}
	if out.MessageId != "msg-1" {
		t.Fatalf("messageId = %q", out.MessageId)
	}
	if out.MD5OfMessageBody != md5OfBody("hello") {
		t.Fatalf("body md5 = %q", out.MD5OfMessageBody)
	}
	if out.MD5OfMessageAttributes == "" {
		t.Fatalf("attributes md5 should be present")
	}
	if out.SequenceNumber != nil {
		t.Fatalf("SequenceNumber should be null")
	}
}

func TestRespondSQSNoAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	respondSQS(rec, "x", nil, "m")
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if _, present := out["MD5OfMessageAttributes"]; present {
		t.Fatalf("MD5OfMessageAttributes must be omitted when no attributes")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondSQS -v`
Expected: FAIL — `undefined: respondSQS`.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/in/awsmsg/respond_sqs.go`:

```go
package awsmsg

import (
	"encoding/json"
	"net/http"
)

// respondSQS writes a valid SQS SendMessage JSON response. The SDK validates
// MD5OfMessageBody (and MD5OfMessageAttributes when attributes are present), so
// both are computed faithfully.
func respondSQS(w http.ResponseWriter, body string, attrs []MsgAttr, messageID string) {
	resp := map[string]interface{}{
		"MD5OfMessageBody": md5OfBody(body),
		"MessageId":        messageID,
		"SequenceNumber":   nil,
	}
	if md := md5OfAttributes(attrs); md != "" {
		resp["MD5OfMessageAttributes"] = md
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondSQS -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/respond_sqs.go internal/adapters/in/awsmsg/respond_sqs_test.go
git commit -m "feat(events): SQS response synthesis with MD5 checksums"
```
No Co-Authored-By footer.

---

## Task 4: EventBridge parser

**Files:**
- Create: `internal/adapters/in/awsmsg/parse_eb.go`
- Test: `internal/adapters/in/awsmsg/parse_eb_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/in/awsmsg/parse_eb_test.go`:

```go
package awsmsg

import "testing"

func TestParseEventBridge(t *testing.T) {
	body := []byte(`{"Entries":[
		{"Source":"orders","DetailType":"Created","Detail":"{\"id\":1}","EventBusName":"bus-a"},
		{"Source":"orders","DetailType":"Shipped","Detail":"{\"id\":2}"}
	]}`)
	evs, err := parseEventBridge(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	if evs[0].Service != "eventbridge" || evs[0].Operation != "PutEvents" {
		t.Fatalf("service/op = %q/%q", evs[0].Service, evs[0].Operation)
	}
	if evs[0].Source != "orders" || evs[0].DetailType != "Created" || string(evs[0].Message) != `{"id":1}` {
		t.Fatalf("entry0 = %+v", evs[0])
	}
	if evs[0].Target != "bus-a" {
		t.Fatalf("entry0 bus = %q", evs[0].Target)
	}
	// EventBusName defaults to "default" when omitted.
	if evs[1].Target != "default" {
		t.Fatalf("entry1 bus = %q, want default", evs[1].Target)
	}
}

func TestParseEventBridgeErrors(t *testing.T) {
	if _, err := parseEventBridge([]byte(`{"Entries":[]}`)); err == nil {
		t.Fatal("expected error on empty entries")
	}
	if _, err := parseEventBridge([]byte(`not json`)); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseEventBridge -v`
Expected: FAIL — `undefined: parseEventBridge`.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/in/awsmsg/parse_eb.go`:

```go
package awsmsg

import (
	"encoding/json"
	"fmt"

	"github.com/mockwave/mockwave/domain"
)

type ebPutEvents struct {
	Entries []ebEntry `json:"Entries"`
}

type ebEntry struct {
	Source       string `json:"Source"`
	DetailType   string `json:"DetailType"`
	Detail       string `json:"Detail"`
	EventBusName string `json:"EventBusName"`
}

// parseEventBridge converts a PutEvents JSON body into one normalized Event per
// entry — PutEvents is a batch operation.
func parseEventBridge(body []byte) ([]domain.Event, error) {
	var in ebPutEvents
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("awsmsg: PutEvents body: %w", err)
	}
	if len(in.Entries) == 0 {
		return nil, fmt.Errorf("awsmsg: PutEvents has no entries")
	}
	evs := make([]domain.Event, len(in.Entries))
	for i, e := range in.Entries {
		bus := e.EventBusName
		if bus == "" {
			bus = "default"
		}
		evs[i] = domain.Event{
			Service:    domain.EventServiceEventBridge,
			Operation:  "PutEvents",
			Target:     bus,
			Source:     e.Source,
			DetailType: e.DetailType,
			Message:    []byte(e.Detail),
		}
	}
	return evs, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestParseEventBridge -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/parse_eb.go internal/adapters/in/awsmsg/parse_eb_test.go
git commit -m "feat(events): EventBridge PutEvents parser"
```
No Co-Authored-By footer.

---

## Task 5: EventBridge responder

**Files:**
- Create: `internal/adapters/in/awsmsg/respond_eb.go`
- Test: `internal/adapters/in/awsmsg/respond_eb_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/in/awsmsg/respond_eb_test.go`:

```go
package awsmsg

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestRespondEventBridge(t *testing.T) {
	rec := httptest.NewRecorder()
	respondEventBridge(rec, []string{"e1", "e2"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-amz-json-1.1" {
		t.Fatalf("content-type = %q", ct)
	}
	var out struct {
		FailedEntryCount int `json:"FailedEntryCount"`
		Entries          []struct {
			EventId string `json:"EventId"`
		} `json:"Entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, rec.Body.String())
	}
	if out.FailedEntryCount != 0 {
		t.Fatalf("FailedEntryCount = %d", out.FailedEntryCount)
	}
	if len(out.Entries) != 2 || out.Entries[0].EventId != "e1" || out.Entries[1].EventId != "e2" {
		t.Fatalf("entries = %+v", out.Entries)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondEventBridge -v`
Expected: FAIL — `undefined: respondEventBridge`.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/in/awsmsg/respond_eb.go`:

```go
package awsmsg

import (
	"encoding/json"
	"net/http"
)

// respondEventBridge writes a valid PutEvents JSON response with one EventId
// per input entry, in order.
func respondEventBridge(w http.ResponseWriter, eventIDs []string) {
	entries := make([]map[string]string, len(eventIDs))
	for i, id := range eventIDs {
		entries[i] = map[string]string{"EventId": id}
	}
	resp := map[string]interface{}{
		"FailedEntryCount": 0,
		"Entries":          entries,
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestRespondEventBridge -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/respond_eb.go internal/adapters/in/awsmsg/respond_eb_test.go
git commit -m "feat(events): EventBridge response synthesis"
```
No Co-Authored-By footer.

---

## Task 6: Handler refactor — branch per service

**Files:**
- Modify: `internal/adapters/in/awsmsg/handler.go`
- Modify: `internal/adapters/in/awsmsg/handler_test.go`

- [ ] **Step 1: Update the existing test + add SQS/EB handler tests**

In `internal/adapters/in/awsmsg/handler_test.go`:

(a) `TestHandlerUnsupportedService` currently sends `domain.EventServiceSQS` and expects 501. SQS is now supported, so change the service to a value the handler does NOT handle. Replace that test's `DetectResult{Service: domain.EventServiceSQS}` with `DetectResult{Service: "kinesis"}` (and update the failure message text from "sqs" to "unsupported"). The 501 expectation stays.

(b) Append these new tests:

```go
func TestHandlerSQS(t *testing.T) {
	var captured *domain.Event
	var capturedRule string
	h := NewHandler(
		func() Matcher { return fakeMatcher{id: "orders"} },
		func(ev domain.Event, ruleID, messageID string) { captured = &ev; capturedRule = ruleID },
		func() string { return "msg-7" },
	)
	body := `{"QueueUrl":"https://sqs/q/orders","MessageBody":"hello","MessageAttributes":{"env":{"DataType":"String","StringValue":"prod"}}}`
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSQS, Region: "us-east-1", Identity: "AKID"})

	if rec.Code != 200 {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		MD5OfMessageBody string `json:"MD5OfMessageBody"`
		MessageId        string `json:"MessageId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out.MessageId != "msg-7" || out.MD5OfMessageBody != md5OfBody("hello") {
		t.Fatalf("resp = %+v", out)
	}
	if captured == nil || captured.Target != "https://sqs/q/orders" || capturedRule != "orders" {
		t.Fatalf("captured = %+v rule=%q", captured, capturedRule)
	}
	if captured.Identity != "AKID" {
		t.Fatalf("identity not stamped: %+v", captured)
	}
}

func TestHandlerEventBridge(t *testing.T) {
	var captures int
	ids := []string{"e1", "e2"}
	next := 0
	h := NewHandler(
		func() Matcher { return fakeMatcher{id: "ev"} },
		func(domain.Event, string, string) { captures++ },
		func() string { id := ids[next%len(ids)]; next++; return id },
	)
	body := `{"Entries":[{"Source":"s","DetailType":"A","Detail":"{}"},{"Source":"s","DetailType":"B","Detail":"{}"}]}`
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceEventBridge})

	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var out struct {
		FailedEntryCount int `json:"FailedEntryCount"`
		Entries          []struct{ EventId string } `json:"Entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out.FailedEntryCount != 0 || len(out.Entries) != 2 {
		t.Fatalf("resp = %+v", out)
	}
	if captures != 2 {
		t.Fatalf("expected 2 captures, got %d", captures)
	}
}
```

Ensure `handler_test.go` imports `encoding/json` (add it if not already imported).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/in/awsmsg/ -run 'TestHandler' -v`
Expected: FAIL — SQS/EB still hit the 501 default branch (and `TestHandlerSQS`/`TestHandlerEventBridge` fail).

- [ ] **Step 3: Refactor the handler** — replace the body of `internal/adapters/in/awsmsg/handler.go` with:

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/in/awsmsg/ -v` then `go build ./...` and `go test ./... -race`
Expected: PASS — all awsmsg tests (existing SNS tests unchanged in behavior, plus new SQS/EB) and the full suite green.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/handler.go internal/adapters/in/awsmsg/handler_test.go
git commit -m "feat(events): handler dispatch for SQS and EventBridge"
```
No Co-Authored-By footer.

---

## Task 7: End-to-end contract tests (real SQS + EventBridge SDKs)

**Files:**
- Modify: `tests/integration/event_capture_test.go`
- Modify: `go.mod` / `go.sum` (via `go get`)

- [ ] **Step 1: Add the SDK test dependencies**

```bash
go get github.com/aws/aws-sdk-go-v2/service/sqs@latest
go get github.com/aws/aws-sdk-go-v2/service/eventbridge@latest
```

- [ ] **Step 2: Write the failing tests** — append to `tests/integration/event_capture_test.go`. Add the needed imports to the existing import block: `"github.com/aws/aws-sdk-go-v2/service/sqs"`, `sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"`, `"github.com/aws/aws-sdk-go-v2/service/eventbridge"`, `ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"`. (The existing test already imports `aws`, `awscfg`, `credentials`, `domain`, `jsonfile`, `matched`, `server`, testify.)

```go
func TestE2E_SQSEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "sqs-orders",
			Match: domain.EventMatch{Service: domain.EventServiceSQS},
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

	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	client := sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl:    aws.String(mock.URL + "/000000000000/orders"),
		MessageBody: aws.String(`{"type":"created"}`),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"env": {DataType: aws.String("String"), StringValue: aws.String("prod")},
		},
	})
	require.NoError(t, err, "SDK must accept the response and its MD5 checksums")
	require.NotNil(t, out.MessageId)

	resp, err := http.Get(admin.URL + "/api/event-captures/sqs-orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "aws-sqs", page.Items[0].Protocol)
	assert.Equal(t, "SendMessage", page.Items[0].Method)
	assert.Equal(t, "AKIDEXAMPLE", page.Items[0].Identity)
}

func TestE2E_EventBridgeEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "eb-orders",
			Match: domain.EventMatch{Service: domain.EventServiceEventBridge, Source: "orders.service"},
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

	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	client := eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.PutEvents(context.Background(), &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{Source: aws.String("orders.service"), DetailType: aws.String("OrderCreated"), Detail: aws.String(`{"id":1}`)},
			{Source: aws.String("orders.service"), DetailType: aws.String("OrderShipped"), Detail: aws.String(`{"id":2}`)},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), out.FailedEntryCount)
	require.Len(t, out.Entries, 2)

	resp, err := http.Get(admin.URL + "/api/event-captures/eb-orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 2)
	assert.Equal(t, "aws-eventbridge", page.Items[0].Protocol)
	assert.Equal(t, "PutEvents", page.Items[0].Method)
}
```

- [ ] **Step 3: Run the new e2e tests**

Run: `go test ./tests/integration/ -run 'TestE2E_SQS|TestE2E_EventBridge' -v`
Expected: PASS. If the SQS test fails with a checksum/MD5 error, the `md5OfAttributes` encoding (Task 1) is wrong — fix it there (this e2e is the authoritative MD5 check). If EventBridge fails to parse the response, check the `FailedEntryCount` JSON type and Content-Type.

- [ ] **Step 4: Full suite + coverage gate**

Run: `make test && make coverage`
Expected: all pass; coverage ≥80%. If below, add focused unit cases for any uncovered new branch (e.g. SQS Binary attribute encoding in `md5_test.go`, parser error paths). Report the final coverage %.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/event_capture_test.go go.mod go.sum
git commit -m "test(events): e2e SQS + EventBridge interception with real AWS SDKs"
```
No Co-Authored-By footer.

---

## Task 8: Docs + roadmap + index

**Files:**
- Modify: `docs/event-capture.md`
- Modify: `README.md`
- Modify: `docs/roadmap.md`
- Modify: `docs/plans/2026-06-17-aws-event-interception-index.md`

- [ ] **Step 1: Update `docs/event-capture.md`**

- Change the framing from "SNS only" to "SNS, SQS, and EventBridge". 
- Add the SQS section: JSON protocol (`X-Amz-Target: AmazonSQS.SendMessage`), captured as protocol `aws-sqs`, method `SendMessage`, path = queue URL; note the response carries `MD5OfMessageBody` (+ `MD5OfMessageAttributes` when attributes are present) so the SDK accepts it.
- Add the EventBridge section: JSON protocol (`X-Amz-Target: AWSEvents.PutEvents`), batch — each entry is captured as a separate event (protocol `aws-eventbridge`, method `PutEvents`, path = event bus name, plus `source`/`detail_type` in the capture query, detail JSON as the request body); the response returns one `EventId` per entry.
- Document the matcher caveat: `target` globs use `path.Match`, whose `*` does not cross `/`; SNS topic ARNs glob fine, but SQS queue URLs (which contain `/`) should be matched by exact string or by service-only rules. EventBridge matches well on `source`/`detail_type`.
- Update the Limitations section: SQS/EventBridge now supported; still no re-signed forward (Phase 3) and no cloud persistence (Phase 4); batch `PublishBatch`/`SendMessageBatch`, consumer side, and other providers remain on the roadmap.

- [ ] **Step 2: Update the README feature bullet**

Find the "Outgoing event capture (AWS)" bullet and change it to reflect SNS + SQS + EventBridge:

```markdown
- **Outgoing event capture (AWS)** — intercept the app's SNS / SQS / EventBridge publishes on the mock port, capture them to state for assertion, and return a valid SDK response (correct SQS MD5 checksums and per-entry EventBridge EventIds). Point your AWS SDK client at Mockwave via endpoint override (`--protocols http,aws --event-capture`). See [`docs/event-capture.md`](docs/event-capture.md). (Re-signed forward + cloud persistence on the [roadmap](docs/roadmap.md).)
```

- [ ] **Step 3: Update `docs/roadmap.md`**

In the "Outgoing event interception (AWS)" section: change the "Shipped in v1" line to note SNS/SQS/EventBridge publish interception are now shipped. Remove "GCP Pub/Sub & Azure Service Bus" only if still pending (keep it — still deferred). Remove nothing that's still deferred; specifically the **batch publish ops**, **consumer side**, **event fault injection**, **weighted/canary buckets**, **filter policies/fan-out**, **non-Go SDK fixtures**, and **other providers** remain. Just update the "shipped" framing so it no longer implies SNS-only.

- [ ] **Step 4: Update the plan index**

In `docs/plans/2026-06-17-aws-event-interception-index.md`, mark Phase 2 as delivered (link this plan file) and update the Phase 2 row description from "to be authored" to point at `2026-06-17-aws-event-interception-phase2-plan.md`.

- [ ] **Step 5: Verify + commit**

Run: `grep -n "aws-sqs\|aws-eventbridge\|SQS\|EventBridge" docs/event-capture.md | head` to confirm the new content is present, and confirm links resolve.

```bash
git add docs/event-capture.md README.md docs/roadmap.md docs/plans/2026-06-17-aws-event-interception-index.md
git commit -m "docs(events): document SQS + EventBridge interception (Phase 2)"
```
No Co-Authored-By footer.

---

## Self-Review

**Spec coverage (Phase 2):**
- SQS SendMessage parse (JSON) → Task 2. ✓
- SQS response synthesis with correct `MD5OfMessageBody` + `MD5OfMessageAttributes` → Tasks 1, 3 (authoritatively verified by the real-SDK e2e in Task 7). ✓
- EventBridge PutEvents parse (JSON, array) → Task 4. ✓
- EventBridge response, one EventId per entry, `FailedEntryCount` → Task 5. ✓
- Per-service wire/protocol-matched responses (XML for SNS preserved; `x-amz-json-1.0` for SQS; `x-amz-json-1.1` for EventBridge) → Tasks 3, 5, 6. ✓
- Handler dispatch for all three services; SNS behavior preserved → Task 6. ✓
- Contract round-trip with real `aws-sdk-go-v2` for SQS + EventBridge → Task 7. ✓
- Capture works for SQS/EB via existing `captureEvent` (protocol `aws-sqs`/`aws-eventbridge`) — asserted in Task 7. ✓
- Deferred (still Phase 3/4 + roadmap): re-signed forward, cloud persistence, batch ops, consumer side, other providers — Task 8 keeps these tracked. ✓

**Type consistency:** `parseSQS(body []byte) (domain.Event, []MsgAttr, error)`, `respondSQS(w, body string, attrs []MsgAttr, messageID string)`, `parseEventBridge(body []byte) ([]domain.Event, error)`, `respondEventBridge(w, eventIDs []string)`, `md5OfBody(string) string`, `md5OfAttributes([]MsgAttr) string`, `MsgAttr{Name,DataType,StringValue,BinaryValue}`, handler helpers `stamp(*domain.Event, DetectResult, []byte)` + `matchAndCapture(domain.Event) string` — names are used identically across tasks. The handler refactor (Task 6) references `parseSQS`/`respondSQS`/`parseEventBridge`/`respondEventBridge` defined in Tasks 2–5, which land before Task 6. ✓

**Placeholder scan:** No TBD/TODO; every code step is complete. The only "see Task N" reference is the ordering note (handler depends on Tasks 1–5), which is a sequencing fact, not a missing implementation. ✓

**Risk note:** `md5OfAttributes` is the one piece whose correctness can't be fully unit-asserted against a public vector — Task 7's real SQS SDK call (with an attribute) is the authoritative gate; if it rejects the digest, fix the encoding in Task 1.
