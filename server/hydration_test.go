package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowMatchedStore makes hydration observably expensive and records which rule
// ids it was asked for.
type slowMatchedStore struct {
	delay time.Duration

	mu       sync.Mutex
	ruleIDs  []string
	callDone chan struct{}
}

func newSlowMatchedStore(d time.Duration) *slowMatchedStore {
	return &slowMatchedStore{delay: d, callDone: make(chan struct{}, 64)}
}

func (s *slowMatchedStore) ListMatched(_ context.Context, ruleID string, _ store.MatchedQuery) (store.MatchedPage, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.ruleIDs = append(s.ruleIDs, ruleID)
	s.mu.Unlock()
	select {
	case s.callDone <- struct{}{}:
	default:
	}
	return store.MatchedPage{}, nil
}

func (s *slowMatchedStore) seenRuleIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ruleIDs...)
}

func (s *slowMatchedStore) SaveMatched(context.Context, []matched.Request, []matched.RequestBody, []matched.ResponseBody) error {
	return nil
}
func (s *slowMatchedStore) GetMatched(context.Context, string, string) (*matched.FullRequest, error) {
	return nil, nil
}
func (s *slowMatchedStore) DeleteMatched(context.Context, string) error      { return nil }
func (s *slowMatchedStore) SweepExpired(context.Context, int64) (int, error) { return 0, nil }

// ruleStore serves a couple of rules so hydration has ids to query.
type ruleStore struct{ stubStore }

func (r *ruleStore) GetRules() ([]domain.Rule, error) {
	return []domain.Rule{
		{ID: "rule-a", Match: domain.MatchCriteria{Protocol: "http", Path: "/a"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s"}}},
		{ID: "rule-b", Match: domain.MatchCriteria{Protocol: "http", Path: "/b"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s"}}},
	}, nil
}

// TestNew_DoesNotBlockOnMatchedHydration pins that startup is independent of
// how long hydration takes.
//
// Hydration used to run synchronously inside New with context.Background(), so
// a slow store delayed the server binding its ports. Against a large table
// that delay was long enough for the readiness probe to fail and the pod to be
// killed before it ever served a request.
func TestNew_DoesNotBlockOnMatchedHydration(t *testing.T) {
	ms := newSlowMatchedStore(2 * time.Second)

	start := time.Now()
	srv, err := server.New(server.Config{
		Store: &ruleStore{},
		Matched: server.MatchedConfig{
			Enabled: true, BufferSize: 10, Store: ms,
		},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, srv)
	assert.Less(t, elapsed, 1*time.Second,
		"New must not wait for hydration; it took %v against a 2s-per-call store", elapsed)
}

// TestHydration_QueriesPerRuleNotWholeTable pins that hydration asks for
// specific rules.
//
// An empty ruleID makes the DynamoDB store fall back to a table Scan. Querying
// each known rule instead uses the partition key, which is both bounded and
// already ordered newest-first.
func TestHydration_QueriesPerRuleNotWholeTable(t *testing.T) {
	ms := newSlowMatchedStore(0)

	_, err := server.New(server.Config{
		Store: &ruleStore{},
		Matched: server.MatchedConfig{
			Enabled: true, BufferSize: 10, Store: ms,
		},
	})
	require.NoError(t, err)

	// Hydration is asynchronous now, so wait for it rather than racing it.
	deadline := time.After(5 * time.Second)
	for {
		if len(ms.seenRuleIDs()) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("hydration never queried the rules; saw %v", ms.seenRuleIDs())
		case <-time.After(20 * time.Millisecond):
		}
	}

	seen := ms.seenRuleIDs()
	assert.NotContains(t, seen, "",
		"an empty ruleID triggers a full table scan; hydration must name each rule")
	assert.Subset(t, seen, []string{"rule-a", "rule-b"})
}
