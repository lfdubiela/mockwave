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
