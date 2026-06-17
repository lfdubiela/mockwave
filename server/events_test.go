package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
)

func memStore() *jsonfile.Store { return jsonfile.NewMemStore(domain.Config{}) }

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

func TestEventQueryFIFOFields(t *testing.T) {
	q := eventQuery(domain.Event{GroupID: "g1", DedupID: "d1"})
	if q["group_id"] != "g1" || q["dedup_id"] != "d1" {
		t.Fatalf("fifo query = %v", q)
	}
}

func TestResolveEventConfigEnv(t *testing.T) {
	t.Setenv("MOCKWAVE_EVENT_CAPTURE", "true")
	t.Setenv("MOCKWAVE_EVENT_TTL", "120")
	t.Setenv("MOCKWAVE_EVENT_BUFFER_SIZE", "55")
	t.Setenv("MOCKWAVE_EVENT_SYNC_INTERVAL", "7")
	c := resolveEventConfig(EventConfig{})
	if !c.Enabled || c.ttlSeconds() != 120 || c.BufferSize != 55 || c.SyncInterval != 7*time.Second {
		t.Fatalf("env config = %+v ttl %ds", c, c.ttlSeconds())
	}
}

func TestCaptureEvent(t *testing.T) {
	srv, err := New(Config{
		Store: memStore(),
		Event: EventConfig{Enabled: true, BufferSize: 16, SyncInterval: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ev := domain.Event{
		Service:   domain.EventServiceSNS,
		Operation: "Publish",
		Target:    "arn:aws:sns:us-east-1:123456789012:orders",
		Identity:  "AKIDEXAMPLE",
		Message:   []byte(`{"k":"v"}`),
	}
	srv.captureEvent(ev, "orders", "msg-123")

	page := srv.eventCaptureBuf.List("orders", matched.Query{})
	if len(page.Items) != 1 {
		t.Fatalf("items = %d", len(page.Items))
	}
	got := page.Items[0]
	if got.Protocol != "aws-sns" || got.Method != "Publish" ||
		got.Path != ev.Target || got.Identity != "AKIDEXAMPLE" || got.ResponseStatus != 200 {
		t.Fatalf("captured = %+v", got)
	}
	full, ok := srv.eventCaptureBuf.Get("orders", got.ID)
	if !ok || string(full.RequestBody) != `{"k":"v"}` {
		t.Fatalf("full = %+v ok=%v", full, ok)
	}
}

func TestCaptureEventEmptyMessage(t *testing.T) {
	srv, err := New(Config{
		Store: memStore(),
		Event: EventConfig{Enabled: true, BufferSize: 4, SyncInterval: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.captureEvent(domain.Event{Service: domain.EventServiceSNS, Operation: "Publish"}, "r", "m")
	page := srv.eventCaptureBuf.List("r", matched.Query{})
	if len(page.Items) != 1 || page.Items[0].RequestBodyID != "" {
		t.Fatalf("empty-message capture = %+v", page.Items)
	}
}

// TestMockHandlerAWSBranch drives a form-encoded SNS publish through the full
// MockHandler so the aws-first protocolMux branch, currentEventMatcher, the
// awsmsg handler, and captureEvent are exercised end to end at the server layer.
func TestMockHandlerAWSBranch(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders"},
		}},
	})
	srv, err := New(Config{
		Store: st,
		Event: EventConfig{Enabled: true, BufferSize: 16, SyncInterval: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if srv.currentEventMatcher() == nil {
		t.Fatal("event matcher should be built")
	}

	h := srv.MockHandler([]string{"http", "aws"}, srv.NewProxy())
	form := url.Values{
		"Action":   {"Publish"},
		"TopicArn": {"arn:aws:sns:us-east-1:123456789012:orders"},
		"Message":  {`{"hello":"world"}`},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20240101/us-east-1/sns/aws4_request, SignedHeaders=host, Signature=abc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "PublishResponse") {
		t.Fatalf("response not SNS XML: %s", w.Body.String())
	}

	page := srv.eventCaptureBuf.List("orders", matched.Query{})
	if len(page.Items) != 1 {
		t.Fatalf("captured items = %d", len(page.Items))
	}
	if got := page.Items[0]; got.Protocol != "aws-sns" || got.Method != "Publish" || got.Identity != "AKIDEXAMPLE" {
		t.Fatalf("captured = %+v", got)
	}
}

func TestCaptureEventDisabledNoop(t *testing.T) {
	srv, err := New(Config{Store: memStore()})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.eventCaptureBuf != nil {
		t.Fatal("buffer should be nil when capture disabled")
	}
	// Must not panic on the nil buffer.
	srv.captureEvent(domain.Event{Operation: "Publish"}, "r", "m")
}
