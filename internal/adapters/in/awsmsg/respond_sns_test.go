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
