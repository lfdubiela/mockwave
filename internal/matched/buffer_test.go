package matched_test

import (
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_AddAndListNewestFirst(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID)
	assert.Equal(t, "a", page.Items[1].ID)
}

func TestBuffer_ListScopedToRule(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r2", At: time.Unix(2, 0)}, nil, nil)
	assert.Len(t, b.List("r1", matched.Query{}).Items, 1)
	assert.Len(t, b.List("r2", matched.Query{}).Items, 1)
	assert.Empty(t, b.List("nope", matched.Query{}).Items)
}

func TestBuffer_ListAppliesFilter(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", At: time.Unix(2, 0)}, nil, nil)
	page := b.List("r1", matched.Query{Method: "POST"})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestBuffer_Pagination(t *testing.T) {
	b := matched.NewBuffer(10)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		b.Add(matched.Request{ID: id, RuleID: "r1", At: time.Unix(int64(i+1), 0)}, nil, nil)
	}
	first := b.List("r1", matched.Query{Limit: 2})
	require.Len(t, first.Items, 2)
	assert.Equal(t, "e", first.Items[0].ID)
	assert.Equal(t, "d", first.Items[1].ID)
	require.NotEmpty(t, first.NextCursor)

	second := b.List("r1", matched.Query{Limit: 2, Cursor: first.NextCursor})
	require.Len(t, second.Items, 2)
	assert.Equal(t, "c", second.Items[0].ID)
	assert.Equal(t, "b", second.Items[1].ID)

	third := b.List("r1", matched.Query{Limit: 2, Cursor: second.NextCursor})
	require.Len(t, third.Items, 1)
	assert.Equal(t, "a", third.Items[0].ID)
	assert.Empty(t, third.NextCursor)
}

func TestBuffer_GlobalCapacityEvictsOldest(t *testing.T) {
	b := matched.NewBuffer(2)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)
	b.Add(matched.Request{ID: "c", RuleID: "r1", At: time.Unix(3, 0)}, nil, nil)
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 2)
	assert.Equal(t, "c", page.Items[0].ID)
	assert.Equal(t, "b", page.Items[1].ID)
}

func TestBuffer_ListSkipsExpired(t *testing.T) {
	b := matched.NewBuffer(10)
	now := time.Unix(100, 0)
	b.SetClock(func() time.Time { return now })
	b.Add(matched.Request{ID: "live", RuleID: "r1", At: time.Unix(99, 0), TTL: 200}, nil, nil)
	b.Add(matched.Request{ID: "dead", RuleID: "r1", At: time.Unix(1, 0), TTL: 50}, nil, nil)
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "live", page.Items[0].ID)
}

func TestBuffer_GetFull(t *testing.T) {
	b := matched.NewBuffer(10)
	r := matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0), RequestBodyID: "rb", ResponseBodyID: "sb"}
	b.Add(r, []byte(`{"in":1}`), map[string]any{"out": 2})
	full, ok := b.Get("r1", "a")
	require.True(t, ok)
	assert.Equal(t, "a", full.ID)
	assert.JSONEq(t, `{"in":1}`, string(full.RequestBody))
	assert.Equal(t, map[string]any{"out": 2}, full.ResponseBody)
}

func TestBuffer_GetMissing(t *testing.T) {
	b := matched.NewBuffer(10)
	_, ok := b.Get("r1", "nope")
	assert.False(t, ok)
}

func TestBuffer_ClearRule(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r2", At: time.Unix(2, 0)}, nil, nil)
	b.Clear("r1")
	assert.Empty(t, b.List("r1", matched.Query{}).Items)
	assert.Len(t, b.List("r2", matched.Query{}).Items, 1)
}

func TestBuffer_ClearAll(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Clear("")
	assert.Empty(t, b.List("r1", matched.Query{}).Items)
}

func TestBuffer_DrainReturnsAllAndKeeps(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0), RequestBodyID: "rb"}, []byte("x"), nil)
	reqs, bodies, respBodies := b.Drain()
	require.Len(t, reqs, 1)
	require.Len(t, bodies, 1)
	assert.Equal(t, "rb", bodies[0].ID)
	assert.Len(t, respBodies, 0)
	assert.Len(t, b.List("r1", matched.Query{}).Items, 1)
}

func TestBuffer_SweepExpired(t *testing.T) {
	b := matched.NewBuffer(10)
	now := time.Unix(100, 0)
	b.SetClock(func() time.Time { return now })
	b.Add(matched.Request{ID: "live", RuleID: "r1", At: time.Unix(99, 0), TTL: 200}, nil, nil)
	b.Add(matched.Request{ID: "dead1", RuleID: "r1", At: time.Unix(1, 0), TTL: 50}, nil, nil)
	b.Add(matched.Request{ID: "dead2", RuleID: "r2", At: time.Unix(2, 0), TTL: 10}, nil, nil)
	n := b.SweepExpired()
	assert.Equal(t, 2, n)
	assert.Len(t, b.List("r1", matched.Query{}).Items, 1)
	assert.Empty(t, b.List("r2", matched.Query{}).Items)
}
