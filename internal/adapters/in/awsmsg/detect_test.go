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
