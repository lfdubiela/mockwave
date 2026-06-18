package restapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// bareStore: implements ONLY store.DataStore — no EventRuleStore capability.
// Used to verify the 501 path in eventRules / eventRuleByID.
// ---------------------------------------------------------------------------

type bareStore struct{}

func (b *bareStore) GetRules() ([]domain.Rule, error)                 { return nil, nil }
func (b *bareStore) SaveRule(domain.Rule) error                       { return nil }
func (b *bareStore) DeleteRule(string) error                          { return nil }
func (b *bareStore) GetSimulation(string) (*domain.Simulation, error) { return nil, nil }
func (b *bareStore) ListSimulations() ([]domain.Simulation, error)    { return nil, nil }
func (b *bareStore) SaveSimulation(domain.Simulation) error           { return nil }
func (b *bareStore) DeleteSimulation(string) error                    { return nil }

// ---------------------------------------------------------------------------
// eventRuleMemStore: implements store.DataStore + store.EventRuleStore backed
// by an in-memory map. Used for the CRUD happy-path tests.
// ---------------------------------------------------------------------------

type eventRuleMemStore struct {
	rules map[string]domain.EventRule
}

func newEventRuleMemStore() *eventRuleMemStore {
	return &eventRuleMemStore{rules: map[string]domain.EventRule{}}
}

// store.DataStore stubs
func (s *eventRuleMemStore) GetRules() ([]domain.Rule, error)                 { return nil, nil }
func (s *eventRuleMemStore) SaveRule(domain.Rule) error                       { return nil }
func (s *eventRuleMemStore) DeleteRule(string) error                          { return nil }
func (s *eventRuleMemStore) GetSimulation(string) (*domain.Simulation, error) { return nil, nil }
func (s *eventRuleMemStore) ListSimulations() ([]domain.Simulation, error)    { return nil, nil }
func (s *eventRuleMemStore) SaveSimulation(domain.Simulation) error           { return nil }
func (s *eventRuleMemStore) DeleteSimulation(string) error                    { return nil }

// store.EventRuleStore
func (s *eventRuleMemStore) GetEventRules() ([]domain.EventRule, error) {
	out := make([]domain.EventRule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, r)
	}
	return out, nil
}

func (s *eventRuleMemStore) SaveEventRule(r domain.EventRule) error {
	s.rules[r.ID] = r
	return nil
}

func (s *eventRuleMemStore) DeleteEventRule(id string) error {
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("event rule not found: %s", id)
	}
	delete(s.rules, id)
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newEventAPI(st interface {
	GetRules() ([]domain.Rule, error)
	SaveRule(domain.Rule) error
	DeleteRule(string) error
	GetSimulation(string) (*domain.Simulation, error)
	ListSimulations() ([]domain.Simulation, error)
	SaveSimulation(domain.Simulation) error
	DeleteSimulation(string) error
}) *adminAPI {
	// Use store.DataStore via the adminAPI's store field.
	// onReload is set to a no-op so reload() never panics.
	return &adminAPI{
		store:    st,
		onReload: func() {},
	}
}

// ---------------------------------------------------------------------------
// Test: 501 when store doesn't implement EventRuleStore
// ---------------------------------------------------------------------------

func TestEventRules_501_NoEventRuleStore(t *testing.T) {
	api := newEventAPI(&bareStore{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/event-rules", nil)
	api.eventRules(rec, req)
	assert.Equal(t, 501, rec.Code)
}

func TestEventRuleByID_501_NoEventRuleStore(t *testing.T) {
	api := newEventAPI(&bareStore{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/event-rules/r1", bytes.NewBufferString(`{"match":{"service":"sns"}}`))
	api.eventRuleByID(rec, req)
	assert.Equal(t, 501, rec.Code)
}

// ---------------------------------------------------------------------------
// Test: event-rules CRUD happy path
// ---------------------------------------------------------------------------

func TestEventRules_CRUD(t *testing.T) {
	st := newEventRuleMemStore()
	api := newEventAPI(st)

	// POST valid rule → 201
	t.Run("POST valid → 201", func(t *testing.T) {
		body, _ := json.Marshal(domain.EventRule{
			ID:    "r1",
			Match: domain.EventMatch{Service: "sns"},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/event-rules", bytes.NewReader(body))
		api.eventRules(rec, req)
		require.Equal(t, 201, rec.Code)
		var got domain.EventRule
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
		assert.Equal(t, "r1", got.ID)
	})

	// GET list → 200, contains r1
	t.Run("GET list → 200 with r1", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/event-rules", nil)
		api.eventRules(rec, req)
		require.Equal(t, 200, rec.Code)
		var rules []domain.EventRule
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&rules))
		require.Len(t, rules, 1)
		assert.Equal(t, "r1", rules[0].ID)
	})

	// POST invalid rule (bad service) → 422
	t.Run("POST invalid service → 422", func(t *testing.T) {
		body, _ := json.Marshal(domain.EventRule{
			ID:    "r2",
			Match: domain.EventMatch{Service: "kinesis"},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/event-rules", bytes.NewReader(body))
		api.eventRules(rec, req)
		assert.Equal(t, 422, rec.Code)
	})

	// PUT update r1 → 200
	t.Run("PUT update r1 → 200", func(t *testing.T) {
		body, _ := json.Marshal(domain.EventRule{
			Match: domain.EventMatch{Service: "sqs"},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/event-rules/r1", bytes.NewReader(body))
		api.eventRuleByID(rec, req)
		require.Equal(t, 200, rec.Code)
		var got domain.EventRule
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
		assert.Equal(t, "r1", got.ID)
		assert.Equal(t, "sqs", got.Match.Service)
	})

	// DELETE r1 → 204
	t.Run("DELETE r1 → 204", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/event-rules/r1", nil)
		api.eventRuleByID(rec, req)
		assert.Equal(t, 204, rec.Code)
	})

	// DELETE non-existent → 404
	t.Run("DELETE nope → 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/event-rules/nope", nil)
		api.eventRuleByID(rec, req)
		assert.Equal(t, 404, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// Test: eventCaptures nil buffer → 404
// ---------------------------------------------------------------------------

func TestEventCaptures_NilBuffer_404(t *testing.T) {
	api := &adminAPI{} // eventCaptureBuf is nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/event-captures/x", nil)
	api.eventCaptures(rec, req)
	assert.Equal(t, 404, rec.Code)
}

// ---------------------------------------------------------------------------
// Test: eventCaptureDetail found and not-found
// ---------------------------------------------------------------------------

func TestEventCaptureDetail(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{
		ID:       "cap1",
		RuleID:   "orders",
		At:       time.Now(),
		Protocol: "aws-sns",
		Method:   "Publish",
		Path:     "arn:orders",
	}, []byte(`{"msg":"hello"}`), nil)

	api := &adminAPI{eventCaptureBuf: buf}

	// List to capture the returned ID (the buffer stores the ID we passed in).
	page := buf.List("orders", matched.Query{})
	require.Len(t, page.Items, 1)
	id := page.Items[0].ID

	// GET existing detail → 200
	t.Run("GET existing detail → 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/event-captures/orders/"+id, nil)
		api.eventCaptures(rec, req)
		require.Equal(t, 200, rec.Code)
		var full matched.FullRequest
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&full))
		assert.Equal(t, id, full.ID)
	})

	// GET non-existent detail → 404
	t.Run("GET missing detail → 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/event-captures/orders/doesnotexist", nil)
		api.eventCaptures(rec, req)
		assert.Equal(t, 404, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// Test: error / edge branches in the event-rule + capture handlers
// ---------------------------------------------------------------------------

func TestEventRules_BadJSON_405(t *testing.T) {
	api := newEventAPI(newEventRuleMemStore())

	t.Run("POST invalid JSON → 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/event-rules", bytes.NewBufferString("{not json"))
		api.eventRules(rec, req)
		assert.Equal(t, 400, rec.Code)
	})

	t.Run("PATCH → 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/event-rules", nil)
		api.eventRules(rec, req)
		assert.Equal(t, 405, rec.Code)
	})
}

func TestEventRuleByID_Branches(t *testing.T) {
	api := newEventAPI(newEventRuleMemStore())

	t.Run("empty id → 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/event-rules/", nil)
		api.eventRuleByID(rec, req)
		assert.Equal(t, 404, rec.Code)
	})

	t.Run("PUT invalid JSON → 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/event-rules/r1", bytes.NewBufferString("{bad"))
		api.eventRuleByID(rec, req)
		assert.Equal(t, 400, rec.Code)
	})

	t.Run("PUT invalid service → 422", func(t *testing.T) {
		body, _ := json.Marshal(domain.EventRule{Match: domain.EventMatch{Service: "kinesis"}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/event-rules/r1", bytes.NewReader(body))
		api.eventRuleByID(rec, req)
		assert.Equal(t, 422, rec.Code)
	})

	t.Run("PATCH → 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/event-rules/r1", nil)
		api.eventRuleByID(rec, req)
		assert.Equal(t, 405, rec.Code)
	})
}

func TestEventCaptures_MethodAndDelete(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "1", RuleID: "orders", At: time.Now(), Protocol: "aws-sns", Method: "Publish", Path: "arn:orders"}, []byte(`{"id":1}`), nil)
	api := &adminAPI{eventCaptureBuf: buf}

	t.Run("DELETE rule captures → 204 and clears", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/event-captures/orders", nil)
		api.eventCaptures(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
		page := buf.List("orders", matched.Query{})
		assert.Len(t, page.Items, 0)
	})

	t.Run("PATCH → 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/event-captures/orders", nil)
		api.eventCaptures(rec, req)
		assert.Equal(t, 405, rec.Code)
	})

	t.Run("GET too many path parts → 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/event-captures/orders/abc/extra", nil)
		api.eventCaptures(rec, req)
		assert.Equal(t, 404, rec.Code)
	})
}

func TestEventCaptureList_LimitQuery(t *testing.T) {
	buf := matched.NewBuffer(10)
	for i := 0; i < 3; i++ {
		buf.Add(matched.Request{ID: fmt.Sprintf("c%d", i), RuleID: "orders", At: time.Now(), Protocol: "aws-sns", Method: "Publish", Path: "arn:orders"}, nil, nil)
	}
	api := &adminAPI{eventCaptureBuf: buf}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/event-captures/orders?limit=2&method=Publish&path=arn:orders", nil)
	api.eventCaptures(rec, req)
	require.Equal(t, 200, rec.Code)
	var page matched.Page
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&page))
	assert.Len(t, page.Items, 2)
}

// ---------------------------------------------------------------------------
// Original test (preserved)
// ---------------------------------------------------------------------------

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

func TestEventCaptureListBodyAndAttrFilter(t *testing.T) {
	buf := matched.NewBuffer(100)
	buf.Add(matched.Request{ID: "2", RuleID: "orders", At: time.Now(), Protocol: "aws-sns",
		Query: map[string]string{"attr.tenant": "acme"}, RequestBodyID: "2"},
		[]byte(`{"correlation_id":"abc"}`), nil)
	buf.Add(matched.Request{ID: "1", RuleID: "orders", At: time.Now(), Protocol: "aws-sns",
		Query: map[string]string{"attr.tenant": "other"}, RequestBodyID: "1"},
		[]byte(`{"correlation_id":"xyz"}`), nil)
	api := &adminAPI{eventCaptureBuf: buf}

	get := func(qs string) matched.Page {
		rec := httptest.NewRecorder()
		api.eventCaptures(rec, httptest.NewRequest("GET", "/api/event-captures/orders?"+qs, nil))
		var p matched.Page
		_ = json.NewDecoder(rec.Body).Decode(&p)
		return p
	}

	if p := get("body=$.correlation_id:abc"); len(p.Items) != 1 || p.Items[0].ID != "2" {
		t.Fatalf("body filter = %+v", p.Items)
	}
	if p := get("attr=tenant:acme"); len(p.Items) != 1 || p.Items[0].ID != "2" {
		t.Fatalf("attr filter = %+v", p.Items)
	}
	if p := get("attr=tenant:acme&body=$.correlation_id:xyz"); len(p.Items) != 0 {
		t.Fatalf("combined should be empty: %+v", p.Items)
	}
}
