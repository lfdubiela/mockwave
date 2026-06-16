package jsonfile_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mockwave/mockwave/domain"
)

func newStore(t *testing.T) *jsonfile.Store {
	t.Helper()
	s, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	return s
}

func TestJSONMatched_SaveListGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	reqs := []matched.Request{
		{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", RequestBodyID: "rb"},
		{ID: "b", RuleID: "r1", Method: "POST", Path: "/y"},
	}
	require.NoError(t, s.SaveMatched(ctx, reqs, []matched.RequestBody{{ID: "rb", Body: []byte("hi")}}, nil))
	page, err := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID) // newest first (id desc)
	full, err := s.GetMatched(ctx, "r1", "a")
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, []byte("hi"), full.RequestBody)
}

func TestJSONMatched_GetAbsent(t *testing.T) {
	s := newStore(t)
	full, err := s.GetMatched(context.Background(), "r1", "nope")
	require.NoError(t, err)
	assert.Nil(t, full)
}

func TestJSONMatched_Delete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMatched(ctx, []matched.Request{{ID: "a", RuleID: "r1"}}, nil, nil))
	require.NoError(t, s.DeleteMatched(ctx, "r1"))
	page, err := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestJSONMatched_SweepExpired(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMatched(ctx, []matched.Request{
		{ID: "old", RuleID: "r1", TTL: 50},
		{ID: "new", RuleID: "r1", TTL: 200},
	}, nil, nil))
	n, err := s.SweepExpired(ctx, 100) // remove TTL < 100
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	page, _ := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "new", page.Items[0].ID)
}

func TestJSONMatched_ListAllRules(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMatched(ctx, []matched.Request{
		{ID: "a", RuleID: "r1"}, {ID: "b", RuleID: "r2"},
	}, nil, nil))
	page, err := s.ListMatched(ctx, "", store.MatchedQuery{Limit: 100}) // "" = all rules (hydration)
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
}
