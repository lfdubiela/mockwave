package matched_test

import (
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func req(id, method, path string, status int, h map[string]string) matched.Request {
	return matched.Request{ID: id, Method: method, Path: path, ResponseStatus: status, Headers: h, At: time.Unix(1, 0)}
}

func TestQuery_Matches_MethodPathStatus(t *testing.T) {
	q := matched.Query{Method: "POST", Path: "/users/*", Status: 201}
	assert.True(t, q.Matches(req("1", "POST", "/users/42", 201, nil)))
	assert.False(t, q.Matches(req("2", "GET", "/users/42", 201, nil)))
	assert.False(t, q.Matches(req("3", "POST", "/orders/1", 201, nil)))
	assert.False(t, q.Matches(req("4", "POST", "/users/42", 500, nil)))
}

func TestQuery_Matches_HeadersAND(t *testing.T) {
	q := matched.Query{Headers: map[string]string{"x-cid": "abc", "x-foo": "bar"}}
	assert.True(t, q.Matches(req("1", "GET", "/x", 200, map[string]string{"x-cid": "abc", "x-foo": "bar", "x-extra": "y"})))
	assert.False(t, q.Matches(req("2", "GET", "/x", 200, map[string]string{"x-cid": "abc"})))
	assert.False(t, q.Matches(req("3", "GET", "/x", 200, map[string]string{"x-cid": "zzz", "x-foo": "bar"})))
}

func TestQuery_HeaderMatch_CaseInsensitiveKey(t *testing.T) {
	q := matched.Query{Headers: map[string]string{"X-CID": "abc"}}
	assert.True(t, q.Matches(req("1", "GET", "/x", 200, map[string]string{"x-cid": "abc"})))
}

func TestQuery_EmptyMatchesAll(t *testing.T) {
	q := matched.Query{}
	assert.True(t, q.Matches(req("1", "GET", "/anything", 999, nil)))
}

func TestCursor_RoundTrip(t *testing.T) {
	c := matched.EncodeCursor("01900000-0000-7000-8000-000000000000")
	got, err := matched.DecodeCursor(c)
	require.NoError(t, err)
	assert.Equal(t, "01900000-0000-7000-8000-000000000000", got)
}

func TestDecodeCursor_Empty(t *testing.T) {
	got, err := matched.DecodeCursor("")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestDecodeCursor_Invalid(t *testing.T) {
	_, err := matched.DecodeCursor("!!!not-base64!!!")
	assert.Error(t, err)
}
