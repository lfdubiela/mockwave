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

// FIX D: dirty-tracking tests.

func TestBuffer_Drain_DirtyTracking(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)

	// First drain: 2 new entries.
	reqs, _, _ := b.Drain()
	assert.Len(t, reqs, 2)

	// Second drain with no new adds: 0 entries.
	reqs2, _, _ := b.Drain()
	assert.Empty(t, reqs2)

	// Add one more and drain: only the new one.
	b.Add(matched.Request{ID: "c", RuleID: "r1", At: time.Unix(3, 0)}, nil, nil)
	reqs3, _, _ := b.Drain()
	assert.Len(t, reqs3, 1)
	assert.Equal(t, "c", reqs3[0].ID)

	// Entries are still queryable after drains.
	page := b.List("r1", matched.Query{})
	assert.Len(t, page.Items, 3)
}

func TestBuffer_Drain_HydrateNotDirty(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Hydrate([]matched.Request{
		{ID: "h1", RuleID: "r1", At: time.Unix(1, 0)},
		{ID: "h2", RuleID: "r1", At: time.Unix(2, 0)},
	})

	// Hydrated entries must NOT appear in Drain.
	reqs, _, _ := b.Drain()
	assert.Empty(t, reqs)

	// But they are still queryable.
	page := b.List("r1", matched.Query{})
	assert.Len(t, page.Items, 2)
}

func TestBuffer_Drain_EvictedNotDirty(t *testing.T) {
	// Cap of 1: adding a second entry evicts the first.
	b := matched.NewBuffer(1)
	b.Add(matched.Request{ID: "old", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "new", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)

	// Only the surviving (new) entry should be dirty.
	reqs, _, _ := b.Drain()
	require.Len(t, reqs, 1)
	assert.Equal(t, "new", reqs[0].ID)
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

func TestListBodyFilter(t *testing.T) {
	b := matched.NewBuffer(100)
	add := func(id, body string) {
		b.Add(matched.Request{ID: id, RuleID: "r", Protocol: "aws-sns", RequestBodyID: id}, []byte(body), nil)
	}
	add("3", `{"correlation_id":"abc","total":42}`)
	add("2", `{"correlation_id":"xyz","total":7}`)
	add("1", `not json`)

	page := b.List("r", matched.Query{Body: map[string]string{"$.correlation_id": "abc"}})
	if len(page.Items) != 1 || page.Items[0].ID != "3" {
		t.Fatalf("body filter = %+v", page.Items)
	}
	if got := b.List("r", matched.Query{Body: map[string]string{"$.correlation_id": "abc", "$.total": "42"}}); len(got.Items) != 1 {
		t.Fatalf("AND body filter = %+v", got.Items)
	}
	if got := b.List("r", matched.Query{Body: map[string]string{"$.correlation_id": "nope"}}); len(got.Items) != 0 {
		t.Fatalf("mismatch should be empty, got %+v", got.Items)
	}
	if got := b.List("r", matched.Query{Body: map[string]string{"$.missing": "x"}}); len(got.Items) != 0 {
		t.Fatalf("missing path should be empty")
	}
}

func TestListBodyFilterNoBody(t *testing.T) {
	b := matched.NewBuffer(100)
	b.Add(matched.Request{ID: "1", RuleID: "r", Protocol: "http"}, nil, nil)
	if got := b.List("r", matched.Query{Body: map[string]string{"$.x": "1"}}); len(got.Items) != 0 {
		t.Fatalf("no-body request must not match a body filter: %+v", got.Items)
	}
}

func TestListBodyFilterPagination(t *testing.T) {
	b := matched.NewBuffer(100)
	add := func(id, body string) {
		b.Add(matched.Request{ID: id, RuleID: "r", Protocol: "aws-sns", RequestBodyID: id}, []byte(body), nil)
	}
	// 3 matching (type=order) interleaved with 2 non-matching (type=other).
	add("5", `{"type":"order","n":5}`)
	add("4", `{"type":"other","n":4}`)
	add("3", `{"type":"order","n":3}`)
	add("2", `{"type":"other","n":2}`)
	add("1", `{"type":"order","n":1}`)

	bodyFilter := map[string]string{"$.type": "order"}

	// Page 1: limit 2 → ids 5,3 (newest-first, only matching), cursor present.
	p1 := b.List("r", matched.Query{Limit: 2, Body: bodyFilter})
	if len(p1.Items) != 2 || p1.Items[0].ID != "5" || p1.Items[1].ID != "3" {
		t.Fatalf("page1 = %+v", p1.Items)
	}
	if p1.NextCursor == "" {
		t.Fatal("page1 must have a next cursor (more matching items remain)")
	}

	// Page 2: → id 1 (the last matching), no further cursor.
	p2 := b.List("r", matched.Query{Limit: 2, Cursor: p1.NextCursor, Body: bodyFilter})
	if len(p2.Items) != 1 || p2.Items[0].ID != "1" {
		t.Fatalf("page2 = %+v", p2.Items)
	}
	if p2.NextCursor != "" {
		t.Fatal("page2 must NOT have a next cursor (no more matching items)")
	}

	// Sanity: all three matching ids covered, no non-matching leaked.
	seen := map[string]bool{p1.Items[0].ID: true, p1.Items[1].ID: true, p2.Items[0].ID: true}
	for _, id := range []string{"1", "3", "5"} {
		if !seen[id] {
			t.Fatalf("missing matching id %s", id)
		}
	}
}
