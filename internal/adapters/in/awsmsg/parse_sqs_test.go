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
