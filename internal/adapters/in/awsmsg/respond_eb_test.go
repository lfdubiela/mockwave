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
