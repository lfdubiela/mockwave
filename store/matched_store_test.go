package store_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
)

type fakeMatched struct{ saved []matched.Request }

func (f *fakeMatched) SaveMatched(_ context.Context, reqs []matched.Request, _ []matched.RequestBody, _ []matched.ResponseBody) error {
	f.saved = append(f.saved, reqs...)
	return nil
}
func (f *fakeMatched) ListMatched(_ context.Context, ruleID string, _ store.MatchedQuery) (store.MatchedPage, error) {
	return store.MatchedPage{}, nil
}
func (f *fakeMatched) GetMatched(_ context.Context, _, _ string) (*matched.FullRequest, error) {
	return nil, nil
}
func (f *fakeMatched) DeleteMatched(_ context.Context, _ string) error      { return nil }
func (f *fakeMatched) SweepExpired(_ context.Context, _ int64) (int, error) { return 0, nil }

func TestMatchedStore_Satisfiable(t *testing.T) {
	var s store.MatchedStore = &fakeMatched{}
	err := s.SaveMatched(context.Background(), []matched.Request{{ID: "a"}}, nil, nil)
	assert.NoError(t, err)
}
