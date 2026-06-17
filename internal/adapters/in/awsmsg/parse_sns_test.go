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

func TestParseSNSErrorsAndFallback(t *testing.T) {
	if _, err := parseSNS(url.Values{}); err == nil {
		t.Fatal("expected error when Action is missing")
	}
	form := url.Values{"Action": {"Publish"}, "TargetArn": {"arn:target"}, "Message": {"x"}}
	ev, err := parseSNS(form)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Target != "arn:target" {
		t.Fatalf("expected TargetArn fallback, got %q", ev.Target)
	}
}
