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

func TestHandlerUnsupportedService(t *testing.T) {
	h := NewHandler(func() Matcher { return fakeMatcher{} }, func(domain.Event, string, string) {}, func() string { return "x" })
	r := httptest.NewRequest("POST", "http://mock/", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r, DetectResult{Service: domain.EventServiceSQS})
	if rec.Code != 501 {
		t.Fatalf("expected 501 for sqs in phase 1, got %d", rec.Code)
	}
}
