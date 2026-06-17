package restapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
)

func TestEventCapturesList(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "1", RuleID: "orders", At: time.Now(), Protocol: "aws-sns", Method: "Publish", Path: "arn:orders"}, []byte(`{"id":1}`), nil)
	api := &adminAPI{eventCaptureBuf: buf}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/event-captures/orders", nil)
	api.eventCaptures(rec, r)

	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var page matched.Page
	_ = json.NewDecoder(rec.Body).Decode(&page)
	if len(page.Items) != 1 || page.Items[0].Path != "arn:orders" {
		t.Fatalf("page = %+v", page)
	}
}
