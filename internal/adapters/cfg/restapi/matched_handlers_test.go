package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMatchedAPI(buf *matched.Buffer) *adminAPI {
	return &adminAPI{matchedBuf: buf}
}

func TestMatched_List(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", At: time.Unix(1, 0)}, nil, nil)
	buf.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", Path: "/x", At: time.Unix(2, 0)}, nil, nil)
	api := newMatchedAPI(buf)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)
	var page matched.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestMatched_List_Filters(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", At: time.Unix(1, 0)}, nil, nil)
	buf.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", Path: "/x", Headers: map[string]string{"x-cid": "uuid-1"}, At: time.Unix(2, 0)}, nil, nil)
	api := newMatchedAPI(buf)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1?method=POST&headers=x-cid:uuid-1", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)
	var page matched.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestMatched_Detail(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "POST", Path: "/x", RequestBodyID: "rb", At: time.Unix(1, 0)}, []byte(`{"k":1}`), nil)
	api := newMatchedAPI(buf)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1/a", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)
	var full matched.FullRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))
	assert.Equal(t, "a", full.ID)
	assert.JSONEq(t, `{"k":1}`, string(full.RequestBody))
}

func TestMatched_Detail_NotFound(t *testing.T) {
	api := newMatchedAPI(matched.NewBuffer(10))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1/missing", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 404, rec.Code)
}

func TestMatched_DeleteRule(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	api := newMatchedAPI(buf)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 204, rec.Code)
	assert.Empty(t, buf.List("r1", matched.Query{}).Items)
}

func TestMatched_Disabled(t *testing.T) {
	api := newMatchedAPI(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 404, rec.Code)
}
