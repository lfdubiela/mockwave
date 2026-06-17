# AWS Event Interception — Phase 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional re-signed forwarding of intercepted events to a real broker: when a matched `EventRule` has a `Forward` config, Mockwave re-emits the event via `aws-sdk-go-v2` (signed with its own credentials), relays the broker's real id back to the app, and records the forward outcome in the capture.

**Architecture:** A new outbound adapter `internal/adapters/out/awsforward` resolves credentials/region/endpoint from the rule's `EventForward` and calls `sns.Publish` / `sqs.SendMessage` / `eventbridge.PutEvents`. The `awsmsg` handler gains a `Forwarder` dependency and a per-event decision: forward (relay real id) or synthesize (Phase 1/2 behavior). Capture is upgraded from a 3-arg func to a `Capture` struct so it can record `Forwarded`/`ForwardTarget`/status. Forward errors propagate as HTTP 502 (never a synthetic success).

**Tech Stack:** Go 1.26, `aws-sdk-go-v2` (config, credentials, service/sns, service/sqs, service/eventbridge — all already deps). Tests: `make test` (`go test ./... -race`); coverage gate ≥80% (`make coverage`).

**Spec:** [`docs/specs/2026-06-17-aws-event-interception-design.md`](../specs/2026-06-17-aws-event-interception-design.md) (Forward section). Index: [`2026-06-17-aws-event-interception-index.md`](2026-06-17-aws-event-interception-index.md).

---

## Context an implementer needs (current code, post Phase 2)

- `domain.EventForward{Endpoint, Region, Credential string; DelayMs int}` exists and is validated. `Credential` is `"" | "default" | "profile:<name>" | "static:<name>"`. It's currently never acted on.
- `domain.Event` carries `Service, Operation, Target, Source, DetailType, Subject, Message []byte, Attributes map[string]string, GroupID, DedupID, Region, Identity, RawBody`.
- `awsmsg` handler (`internal/adapters/in/awsmsg/handler.go`) currently: `NewHandler(matcher func() Matcher, capture CaptureFunc, newID func() string)`, where `CaptureFunc = func(ev domain.Event, ruleID, messageID string)`. `ServeHTTP` dispatches per service; each branch parses, calls `h.stamp(&ev,d,body)` + `h.matchAndCapture(ev)` (which matches, captures if matched, returns a generated id), then writes the synthesized response.
- `server.captureEvent(ev domain.Event, ruleID, messageID string)` builds a `matched.Request` (`ResponseStatus` hardcoded 200; `respBody = {"messageId": messageID}`). The handler is built in `MockHandler`'s `case "aws"`: `awsmsg.NewHandler(s.currentEventMatcher, s.captureEvent, matched.NewID)`.
- `matched.Request` has `Identity` but NOT `Forwarded`/`ForwardTarget`. `ResponseStatus` field exists.
- All five aws-sdk-go-v2 modules (config, credentials, service/{sns,sqs,eventbridge}) are already in go.mod.

## Phase 3 design decisions (made here; documented in Task 7)

- **Per-event decision:** a matched rule with `Forward != nil` (and a non-nil forwarder) forwards; otherwise synthesize. EventBridge decides per entry (each entry matches independently).
- **Relay real ids:** the forward returns the broker's real MessageId/EventId, which is written into the protocol response via the existing responders. SQS MD5s are recomputed locally from the same body (identical).
- **Forward error → HTTP 502** for the whole request (SNS/SQS single; EventBridge if any entry's forward fails). Per-entry partial-failure fidelity is deferred (roadmap).
- **Credential `static:<name>`** resolves from env `MOCKWAVE_AWS_STATIC_<NAME>_KEY` / `_SECRET` / `_TOKEN` (name upper-cased). `default` = SDK default chain; `profile:<name>` = shared config profile.
- **Attribute forwarding** uses DataType `String` only (the normalized `Event.Attributes` is `name→StringValue`); Number/Binary fidelity on forward is deferred (roadmap).
- **`DelayMs`** is applied (sleep) on a forwarded event before responding.

## File Structure

- `internal/matched/request.go` — MODIFY: add `Forwarded bool`, `ForwardTarget string`.
- `internal/adapters/out/awsforward/creds.go` — CREATE: `resolveConfig` + static-cred-from-env.
- `internal/adapters/out/awsforward/forward.go` — CREATE: `Forwarder` + `Forward` (SNS/SQS/EventBridge) + attribute conversion.
- `internal/adapters/in/awsmsg/handler.go` — MODIFY: `Capture` struct, `CaptureFunc func(Capture)`, `Forwarder` interface, `matchRule`/`resolveOutcome`, forward-vs-synthesize, 502 on error.
- `internal/adapters/in/awsmsg/handler_test.go` — MODIFY: update fakes/signatures; add forward-path test.
- `server/server.go` — MODIFY: `captureEvent(awsmsg.Capture)`, build `awsforward.New()`, pass to `NewHandler`.
- `tests/integration/event_forward_test.go` — CREATE: e2e forward via a stub upstream (real SDK re-sign).
- `docs/event-capture.md`, `README.md`, `docs/roadmap.md`, plan index — MODIFY.

---

## Task 1: matched.Request — Forwarded + ForwardTarget

**Files:**
- Modify: `internal/matched/request.go`
- Test: `internal/matched/request_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/matched/request_test.go`:

```go
func TestRequestForwardedRoundTrip(t *testing.T) {
	r := matched.Request{ID: "1", RuleID: "r", Protocol: "aws-sns", Forwarded: true, ForwardTarget: "https://sns.real"}
	b, _ := json.Marshal(r)
	var out matched.Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Forwarded || out.ForwardTarget != "https://sns.real" {
		t.Fatalf("forward fields lost: %+v", out)
	}
}
```

(The test file is in package `matched_test` and already imports `encoding/json`, `testing`, and the `matched` package — confirm and reuse those imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run TestRequestForwarded -v`
Expected: FAIL — `unknown field Forwarded`.

- [ ] **Step 3: Write minimal implementation** — in `internal/matched/request.go`, add two fields to the `Request` struct, right after the `Identity` field:

```go
	// Forwarded is true when the captured event was re-emitted to a real broker
	// (Phase 3 forward). ForwardTarget is the forward destination (endpoint or
	// "aws:<region>"). Both empty/false for synthesized (mock) responses.
	Forwarded     bool   `json:"forwarded,omitempty"`
	ForwardTarget string `json:"forward_target,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run TestRequestForwarded -v` then `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matched/request.go internal/matched/request_test.go
git commit -m "feat(events): forwarded + forward target fields on matched.Request"
```
No Co-Authored-By footer.

---

## Task 2: awsforward credential resolver

**Files:**
- Create: `internal/adapters/out/awsforward/creds.go`
- Test: `internal/adapters/out/awsforward/creds_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/out/awsforward/creds_test.go`:

```go
package awsforward

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestResolveConfigStaticFromEnv(t *testing.T) {
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_KEY", "AKIDTEST")
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_SECRET", "secret")
	cfg, err := resolveConfig(context.Background(), domain.EventForward{Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != "AKIDTEST" || creds.SecretAccessKey != "secret" {
		t.Fatalf("creds = %+v", creds)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region = %q", cfg.Region)
	}
}

func TestResolveConfigStaticMissingEnv(t *testing.T) {
	if _, err := resolveConfig(context.Background(), domain.EventForward{Credential: "static:absent"}); err == nil {
		t.Fatal("expected error when static creds env is missing")
	}
}

func TestResolveConfigUnknownCredential(t *testing.T) {
	if _, err := resolveConfig(context.Background(), domain.EventForward{Credential: "vault:foo"}); err == nil {
		t.Fatal("expected error for unknown credential ref")
	}
}

func TestResolveConfigDefaultRegionFallback(t *testing.T) {
	// Empty region defaults to us-east-1; default credential chain is lazy
	// (no error at resolve time even without real AWS creds).
	cfg, err := resolveConfig(context.Background(), domain.EventForward{Credential: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", cfg.Region)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/out/awsforward/ -v`
Expected: FAIL — package/`resolveConfig` undefined.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/out/awsforward/creds.go`:

```go
// Package awsforward re-emits intercepted events to a real AWS broker, signed
// with Mockwave's own credentials (the app's SigV4 signature cannot be reused —
// it is bound to the original host and the secret key never travels).
package awsforward

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/mockwave/mockwave/domain"
)

// resolveConfig builds an aws.Config for a forward target. Region defaults to
// us-east-1 when unset. Credential resolution:
//   ""/"default"   → SDK default chain
//   "profile:<n>"  → shared config profile <n>
//   "static:<n>"   → env MOCKWAVE_AWS_STATIC_<N>_KEY/_SECRET/_TOKEN
func resolveConfig(ctx context.Context, fwd domain.EventForward) (aws.Config, error) {
	region := fwd.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}

	switch {
	case fwd.Credential == "" || fwd.Credential == "default":
		// default credential chain
	case strings.HasPrefix(fwd.Credential, "profile:"):
		opts = append(opts, config.WithSharedConfigProfile(strings.TrimPrefix(fwd.Credential, "profile:")))
	case strings.HasPrefix(fwd.Credential, "static:"):
		cp, err := staticCredsFromEnv(strings.TrimPrefix(fwd.Credential, "static:"))
		if err != nil {
			return aws.Config{}, err
		}
		opts = append(opts, config.WithCredentialsProvider(cp))
	default:
		return aws.Config{}, fmt.Errorf("awsforward: unknown credential ref %q", fwd.Credential)
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

func staticCredsFromEnv(name string) (aws.CredentialsProvider, error) {
	up := strings.ToUpper(name)
	key := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_KEY")
	secret := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_SECRET")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("awsforward: static credential %q requires MOCKWAVE_AWS_STATIC_%s_KEY and _SECRET", name, up)
	}
	token := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_TOKEN")
	return credentials.NewStaticCredentialsProvider(key, secret, token), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/out/awsforward/ -v` then `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/awsforward/creds.go internal/adapters/out/awsforward/creds_test.go
git commit -m "feat(events): awsforward credential resolver"
```
No Co-Authored-By footer.

---

## Task 3: awsforward Forwarder (real SDK re-emit)

**Files:**
- Create: `internal/adapters/out/awsforward/forward.go`
- Test: `internal/adapters/out/awsforward/forward_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/out/awsforward/forward_test.go`. It stands up per-service httptest stubs acting as the "real broker", points the forwarder at them via `EventForward.Endpoint` + static creds, and asserts the returned id AND that the upstream received a request re-signed with Mockwave's key (`AKIDTEST`):

```go
package awsforward

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func staticEnv(t *testing.T) {
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_KEY", "AKIDTEST")
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_SECRET", "secret")
}

func TestForwardSNS(t *testing.T) {
	staticEnv(t)
	var gotAuth string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>real-sns-1</MessageId></PublishResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></PublishResponse>`)
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceSNS, Operation: "Publish", Target: "arn:aws:sns:us-east-1:1:orders", Message: []byte(`{"id":1}`)},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-sns-1" {
		t.Fatalf("id = %q", id)
	}
	if !strings.Contains(gotAuth, "AKIDTEST") {
		t.Fatalf("upstream not re-signed with mockwave key: %q", gotAuth)
	}
}

func TestForwardSQS(t *testing.T) {
	staticEnv(t)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in struct{ MessageBody string }
		_ = json.Unmarshal(body, &in)
		sum := md5.Sum([]byte(in.MessageBody))
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		fmt.Fprintf(w, `{"MessageId":"real-sqs-1","MD5OfMessageBody":"%s"}`, hex.EncodeToString(sum[:]))
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceSQS, Operation: "SendMessage", Target: "https://sqs/q/orders", Message: []byte("hello")},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-sqs-1" {
		t.Fatalf("id = %q", id)
	}
}

func TestForwardEventBridge(t *testing.T) {
	staticEnv(t)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		fmt.Fprint(w, `{"FailedEntryCount":0,"Entries":[{"EventId":"real-eb-1"}]}`)
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceEventBridge, Operation: "PutEvents", Target: "default", Source: "orders", DetailType: "Created", Message: []byte(`{"id":1}`)},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-eb-1" {
		t.Fatalf("id = %q", id)
	}
}

func TestForwardUnsupportedService(t *testing.T) {
	staticEnv(t)
	f := New()
	if _, err := f.Forward(context.Background(), domain.Event{Service: "kinesis"}, domain.EventForward{Region: "us-east-1", Credential: "static:test"}); err == nil {
		t.Fatal("expected error for unsupported service")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/out/awsforward/ -run TestForward -v`
Expected: FAIL — `New`/`Forward` undefined.

- [ ] **Step 3: Write minimal implementation** — create `internal/adapters/out/awsforward/forward.go`:

```go
package awsforward

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/mockwave/mockwave/domain"
)

// Forwarder re-emits events to a real broker via aws-sdk-go-v2, signing with
// the credentials resolved from each rule's EventForward. Stateless: a config
// is resolved and a client built per call.
type Forwarder struct{}

// New returns a Forwarder.
func New() *Forwarder { return &Forwarder{} }

// Forward re-emits ev to the broker described by fwd and returns the broker's
// real message/event id.
func (f *Forwarder) Forward(ctx context.Context, ev domain.Event, fwd domain.EventForward) (string, error) {
	cfg, err := resolveConfig(ctx, fwd)
	if err != nil {
		return "", err
	}
	switch ev.Service {
	case domain.EventServiceSNS:
		client := sns.NewFromConfig(cfg, func(o *sns.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.Publish(ctx, &sns.PublishInput{
			TopicArn:               aws.String(ev.Target),
			Message:                aws.String(string(ev.Message)),
			Subject:                optStr(ev.Subject),
			MessageGroupId:         optStr(ev.GroupID),
			MessageDeduplicationId: optStr(ev.DedupID),
			MessageAttributes:      snsAttrs(ev.Attributes),
		})
		if err != nil {
			return "", err
		}
		return aws.ToString(out.MessageId), nil

	case domain.EventServiceSQS:
		client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:               aws.String(ev.Target),
			MessageBody:            aws.String(string(ev.Message)),
			MessageGroupId:         optStr(ev.GroupID),
			MessageDeduplicationId: optStr(ev.DedupID),
			MessageAttributes:      sqsAttrs(ev.Attributes),
		})
		if err != nil {
			return "", err
		}
		return aws.ToString(out.MessageId), nil

	case domain.EventServiceEventBridge:
		client := eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
			Entries: []ebtypes.PutEventsRequestEntry{{
				Source:       optStr(ev.Source),
				DetailType:   optStr(ev.DetailType),
				Detail:       aws.String(string(ev.Message)),
				EventBusName: optStr(ev.Target),
			}},
		})
		if err != nil {
			return "", err
		}
		if len(out.Entries) == 0 {
			return "", fmt.Errorf("awsforward: PutEvents returned no entries")
		}
		e := out.Entries[0]
		if e.EventId == nil {
			return "", fmt.Errorf("awsforward: PutEvents entry failed: %s", aws.ToString(e.ErrorMessage))
		}
		return aws.ToString(e.EventId), nil

	default:
		return "", fmt.Errorf("awsforward: unsupported service %q", ev.Service)
	}
}

// optStr returns a *string for non-empty s, else nil (so the SDK omits the field).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// snsAttrs/sqsAttrs convert the normalized name→value map into SDK attribute
// maps. DataType is "String" only — the normalized Event lost the original data
// type (Number/Binary forwarding is a roadmap item).
func snsAttrs(m map[string]string) map[string]snstypes.MessageAttributeValue {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]snstypes.MessageAttributeValue, len(m))
	for k, v := range m {
		out[k] = snstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	return out
}

func sqsAttrs(m map[string]string) map[string]sqstypes.MessageAttributeValue {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]sqstypes.MessageAttributeValue, len(m))
	for k, v := range m {
		out[k] = sqstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/out/awsforward/ -v` then `go build ./...`
Expected: PASS (all four forward tests + the cred tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/awsforward/forward.go internal/adapters/out/awsforward/forward_test.go
git commit -m "feat(events): awsforward re-signed re-emit for SNS/SQS/EventBridge"
```
No Co-Authored-By footer.

---

## Task 4: awsmsg handler — forward vs synthesize

**Files:**
- Modify: `internal/adapters/in/awsmsg/handler.go`
- Modify: `internal/adapters/in/awsmsg/handler_test.go`

This changes `CaptureFunc` to take a `Capture` struct and adds a `Forwarder`. Existing handler tests must be updated to the new signatures.

- [ ] **Step 1: Update + extend the tests** — in `internal/adapters/in/awsmsg/handler_test.go`:

(a) READ the file first. The capture fakes currently use `func(ev domain.Event, ruleID, messageID string)`. Update every `NewHandler(...)` call to the new 4-arg form `NewHandler(matcher, capture, newID, forwarder)` passing `nil` as the forwarder for the existing synthesize-path tests, and change every capture callback to `func(c Capture){...}`, reading `c.Event`, `c.RuleID`, `c.MessageID` where the old code read the positional args.

Specifically, for `TestHandlerSNS`: the capture closure becomes
```go
		func(c Capture) { captured = &c.Event; capturedRule = c.RuleID },
```
and `NewHandler(... , nil)`.
For `TestHandlerSQS`: same pattern (`captured = &c.Event; capturedRule = c.RuleID`), `NewHandler(..., nil)`.
For `TestHandlerEventBridge`: the capture closure becomes `func(c Capture) { captures++ }`, `NewHandler(..., nil)`.
For `TestHandlerUnmatchedStillResponds`: `func(c Capture) { captureCalled = true }`, `NewHandler(..., nil)`.
For `TestHandlerUnsupportedService` and `TestHandlerBodyReadError`: `func(c Capture) {}`, `NewHandler(..., nil)`.

(b) Append a forward-path test using a fake forwarder:

```go
type fakeForwarder struct {
	id  string
	err error
	got domain.Event
}

func (f *fakeForwarder) Forward(_ context.Context, ev domain.Event, _ domain.EventForward) (string, error) {
	f.got = ev
	return f.id, f.err
}

func TestHandlerSNSForward(t *testing.T) {
	var cap Capture
	ff := &fakeForwarder{id: "real-id"}
	h := NewHandler(
		func() Matcher {
			return matcherFunc(func(ev domain.Event) *domain.EventRule {
				return &domain.EventRule{ID: "orders", Forward: &domain.EventForward{Endpoint: "https://real", Region: "us-east-1"}}
			})
		},
		func(c Capture) { cap = c },
		func() string { return "synthetic" },
		ff,
	)
	form := "Action=Publish&TopicArn=arn:t&Message=hi"
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(form))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSNS})

	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	// The REAL forwarded id is relayed, not the synthesized one.
	if !strings.Contains(rec.Body.String(), "<MessageId>real-id</MessageId>") {
		t.Fatalf("expected relayed real id, body=%s", rec.Body.String())
	}
	if ff.got.Target != "arn:t" {
		t.Fatalf("forwarder got wrong event: %+v", ff.got)
	}
	if !cap.Forwarded || cap.ForwardTarget != "https://real" || cap.MessageID != "real-id" {
		t.Fatalf("capture = %+v", cap)
	}
}

func TestHandlerSNSForwardError(t *testing.T) {
	ff := &fakeForwarder{err: fmt.Errorf("upstream down")}
	h := NewHandler(
		func() Matcher {
			return matcherFunc(func(ev domain.Event) *domain.EventRule {
				return &domain.EventRule{ID: "orders", Forward: &domain.EventForward{Region: "us-east-1"}}
			})
		},
		func(c Capture) {},
		func() string { return "x" },
		ff,
	)
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader("Action=Publish&TopicArn=arn:t&Message=hi"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSNS})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("forward error must be 502, got %d", rec.Code)
	}
}
```

Add a tiny matcher adapter near the top of the test file (next to `fakeMatcher`):
```go
type matcherFunc func(domain.Event) *domain.EventRule

func (m matcherFunc) Match(ev domain.Event) *domain.EventRule { return m(ev) }
```

Ensure the test file imports `context`, `fmt`, `net/http`, `strings`, `net/http/httptest`, `encoding/json`, `domain` (add any missing).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/in/awsmsg/ -run TestHandler -v`
Expected: FAIL — `NewHandler` arity / `Capture` type / `Forwarder` undefined.

- [ ] **Step 3: Replace `internal/adapters/in/awsmsg/handler.go` with:**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/in/awsmsg/ -v` then `go build ./...`
Expected: the awsmsg package will COMPILE-FAIL until `server.captureEvent`/`MockHandler` are updated (Task 5) — BUT the awsmsg package's own tests should pass when built in isolation: run `go test ./internal/adapters/in/awsmsg/ -v` and confirm PASS. (`go build ./...` will fail on the `server` package; that is expected and fixed in Task 5.)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/in/awsmsg/handler.go internal/adapters/in/awsmsg/handler_test.go
git commit -m "feat(events): handler forward-vs-synthesize with capture struct"
```
No Co-Authored-By footer.

---

## Task 5: Server wiring — capture struct + forwarder

**Files:**
- Modify: `server/server.go`

- [ ] **Step 1: Update `captureEvent` to the new signature.** Replace the existing `func (s *Server) captureEvent(ev domain.Event, ruleID, messageID string)` with:

```go
func (s *Server) captureEvent(c awsmsg.Capture) {
	if s.eventCaptureBuf == nil {
		return
	}
	ev := c.Event
	now := time.Now()
	status := c.Status
	if status == 0 {
		status = 200
	}
	r := matched.Request{
		ID:             matched.NewID(),
		RuleID:         c.RuleID,
		At:             now,
		Protocol:       "aws-" + ev.Service,
		Method:         ev.Operation,
		Path:           ev.Target,
		Query:          eventQuery(ev),
		Identity:       ev.Identity,
		ResponseStatus: status,
		Forwarded:      c.Forwarded,
		ForwardTarget:  c.ForwardTarget,
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
	respBody := map[string]string{"messageId": c.MessageID}
	s.eventCaptureBuf.Add(r, reqBody, respBody)
}
```

- [ ] **Step 2: Update `MockHandler`'s `case "aws"`** to pass a forwarder. Add the import `awsforward "github.com/mockwave/mockwave/internal/adapters/out/awsforward"` to `server/server.go`, then change the case body to:

```go
		case "aws":
			if s.eventCaptureBuf != nil {
				awsH = awsmsg.NewHandler(s.currentEventMatcher, s.captureEvent, matched.NewID, awsforward.New())
			}
```

- [ ] **Step 3: Verify compile + full suite**

Run: `go build ./...` then `go test ./... -race`
Expected: PASS. The server now compiles against the new `captureEvent` signature, and the forwarder is wired. Non-AWS traffic and synthesize-path events are unchanged (forward only triggers when a matched rule has `Forward != nil`).

- [ ] **Step 4: Commit**

```bash
git add server/server.go
git commit -m "feat(events): wire forwarder and forward-aware capture"
```
No Co-Authored-By footer.

---

## Task 6: End-to-end forward test (real SDK re-sign via stub upstream)

**Files:**
- Create: `tests/integration/event_forward_test.go`

- [ ] **Step 1: Write the test.** App SDK → Mockwave (rule with `Forward` → stub upstream) → Mockwave relays the stub's real MessageId; the capture records `Forwarded=true`; the upstream sees a request re-signed with Mockwave's static key (not the app's).

```go
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestE2E_SNSForward(t *testing.T) {
	// Mockwave's static forward credential (resolved by awsforward from env).
	t.Setenv("MOCKWAVE_AWS_STATIC_FWD_KEY", "AKIDMOCKWAVE")
	t.Setenv("MOCKWAVE_AWS_STATIC_FWD_SECRET", "secret")

	// Stub "real SNS" upstream: records the signer, returns a real MessageId.
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>upstream-msg-42</MessageId></PublishResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></PublishResponse>`))
	}))
	defer upstream.Close()

	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Match: domain.EventMatch{Service: domain.EventServiceSNS},
			Forward: &domain.EventForward{
				Endpoint:   upstream.URL,
				Region:     "us-east-1",
				Credential: "static:fwd",
			},
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

	// App publishes to Mockwave with ITS OWN creds.
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDAPP", "appsecret", "")),
	)
	require.NoError(t, err)
	client := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
		Message:  aws.String(`{"id":1}`),
	})
	require.NoError(t, err)
	// The app receives the UPSTREAM's real id, relayed by Mockwave.
	require.Equal(t, "upstream-msg-42", aws.ToString(out.MessageId))

	// The upstream was re-signed with Mockwave's key, NOT the app's.
	require.Contains(t, upstreamAuth, "AKIDMOCKWAVE")
	require.NotContains(t, upstreamAuth, "AKIDAPP")

	// The capture records the forward.
	resp, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].Forwarded)
	assert.True(t, strings.HasPrefix(page.Items[0].ForwardTarget, upstream.URL) || page.Items[0].ForwardTarget == upstream.URL)
	assert.Equal(t, 200, page.Items[0].ResponseStatus)
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./tests/integration/ -run TestE2E_SNSForward -v`
Expected: PASS. If it fails, common causes: the forwarder's static cred env name must match `static:fwd` → `MOCKWAVE_AWS_STATIC_FWD_KEY`; the upstream must return well-formed PublishResponse XML so the forwarder's SDK parses the id.

- [ ] **Step 3: Full suite + coverage**

Run: `make test && make coverage`
Expected: all pass; coverage ≥80%. If below, add focused unit tests for any uncovered new branch (e.g. `forwardTarget` region fallback, an EventBridge forward error path). Report the final coverage %.

- [ ] **Step 4: Commit**

```bash
git add tests/integration/event_forward_test.go
git commit -m "test(events): e2e SNS forward with real-SDK re-sign via stub upstream"
```
No Co-Authored-By footer.

---

## Task 7: Docs + roadmap + index

**Files:**
- Modify: `docs/event-capture.md`
- Modify: `README.md`
- Modify: `docs/roadmap.md`
- Modify: `docs/plans/2026-06-17-aws-event-interception-index.md`

- [ ] **Step 1: Update `docs/event-capture.md`**

Add a **Forwarding** section:
- A rule may carry a `forward` block (`endpoint`, `region`, `credential`, `delay_ms`). When present on a matched rule, Mockwave re-emits the event to the real broker via the AWS SDK, signed with the configured credential, and relays the broker's real id back to the app. The capture records `forwarded: true`, `forward_target`, and the real response status.
- Document why re-signing is required (the app's SigV4 signature is bound to the original host and the secret never travels — quote the spec's 403 reasoning briefly).
- Document the credential references: `""`/`default` (SDK default chain), `profile:<name>` (shared config profile), `static:<name>` (env `MOCKWAVE_AWS_STATIC_<NAME>_KEY` / `_SECRET` / `_TOKEN`). Give a JSON `event_rules` example with a `forward` block.
- Document forward semantics: a forward failure returns HTTP 502 (never a synthetic success); `delay_ms` injects latency.
- Note the v1 limitations: forwarded message attributes are sent as `String` type only (Number/Binary fidelity deferred); EventBridge per-entry partial failure is not modeled (any entry's forward error 502s the whole request); SQS FIFO `SequenceNumber` from the real broker is not relayed.

- [ ] **Step 2: Update the README feature bullet** — change the "Outgoing event capture (AWS)" bullet to mention forwarding:

```markdown
- **Outgoing event capture (AWS)** — intercept the app's SNS / SQS / EventBridge publishes on the mock port, capture them for assertion, and either return a valid mock response or **forward to the real broker** (re-signed with your credentials, relaying the broker's real id). Point your AWS SDK client at Mockwave via endpoint override (`--protocols http,aws --event-capture`). See [`docs/event-capture.md`](docs/event-capture.md). (Cloud-store capture persistence on the [roadmap](docs/roadmap.md).)
```

Do not touch the README `## Roadmap` or CLI Reference sections.

- [ ] **Step 3: Update `docs/roadmap.md`** — move re-signed forward from deferred to shipped in the "Outgoing event interception (AWS)" section (Phase 3 done). Keep deferred: cloud persistence (Phase 4), Number/Binary attribute forwarding, EventBridge per-entry partial-failure fidelity, SQS FIFO SequenceNumber relay, GCP/Azure, batch ops, consumer side, fault injection, weighted buckets, non-Go SDK fixtures. Add the three new forward-fidelity limitations as explicit roadmap bullets.

- [ ] **Step 4: Update the plan index** — in `docs/plans/2026-06-17-aws-event-interception-index.md`, mark Phase 3 delivered (link `2026-06-17-aws-event-interception-phase3-plan.md`).

- [ ] **Step 5: Verify + commit**

Run: `grep -n "forward\|Forward" docs/event-capture.md | head` and confirm links resolve.

```bash
git add docs/event-capture.md README.md docs/roadmap.md docs/plans/2026-06-17-aws-event-interception-index.md
git commit -m "docs(events): document re-signed forward (Phase 3)"
```
No Co-Authored-By footer.

---

## Self-Review

**Spec coverage (Phase 3 / Forward section):**
- Forward optional per rule, re-signed with Mockwave's own credentials → Tasks 2, 3, 4, 5. ✓
- Re-emit via aws-sdk-go-v2 (SNS/SQS/EventBridge) → Task 3. ✓
- Credential resolution by name (`default`/`profile:`/`static:`), secrets not in the rule → Task 2. ✓
- Region/Endpoint override → Tasks 2, 3. ✓
- Relay the real broker response/id → Tasks 3, 4. ✓
- Propagate failure (502, never mask) → Task 4. ✓
- FIFO passthrough (GroupID/DedupID) on forward → Task 3 (SendMessage/Publish inputs). ✓
- Capture records Forwarded + ForwardTarget + status → Tasks 1, 4, 5. ✓
- DelayMs → Task 4. ✓
- Real-SDK contract e2e proving re-sign → Task 6. ✓
- Deferred (documented): Number/Binary attr forwarding, EB per-entry partial failure, SQS SequenceNumber relay, cloud persistence (Phase 4) → Task 7. ✓

**Type consistency:** `awsforward.New() *Forwarder` with `Forward(ctx, domain.Event, domain.EventForward) (string, error)`; `awsmsg.Forwarder` interface has the identical signature (so `*awsforward.Forwarder` satisfies it); `awsmsg.Capture{Event, RuleID, MessageID, Forwarded, ForwardTarget, Status}`; `awsmsg.CaptureFunc func(Capture)`; `NewHandler(matcher, capture, newID, forwarder)`; `server.captureEvent(awsmsg.Capture)`; `matched.Request.Forwarded/ForwardTarget`; `resolveConfig(ctx, domain.EventForward) (aws.Config, error)`. Names are used identically across tasks. Task 4's awsmsg tests compile against the new signature; Task 5 fixes the `server` package compile so `go build ./...` and the full suite pass.

**Placeholder scan:** No TBD/TODO; every code step is complete. Task 4 Step 4 explicitly notes the expected transient `server`-package build failure resolved in Task 5 — that is a sequencing note, not a missing implementation.

**Ordering note:** Task 4 leaves `go build ./...` red (server package references the old `captureEvent` signature) until Task 5. The awsmsg package's own tests pass in isolation after Task 4. This is the one place the tree is not fully green between tasks; Task 5 is small and immediately restores it.
