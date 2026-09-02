package matched_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDrain_BodiesInheritRequestTTL pins that drained bodies carry the expiry
// of the request that owns them.
//
// Stores with native TTL expire each row independently, so a body drained
// without a TTL outlives its request forever. The buffer is where the two are
// still associated, so it is where the expiry has to be attached.
func TestDrain_BodiesInheritRequestTTL(t *testing.T) {
	const ttl = int64(1893456000)
	b := matched.NewBuffer(100)
	b.Add(matched.Request{
		ID: "req-1", RuleID: "rule-1", TTL: ttl,
		RequestBodyID: "rb-1", ResponseBodyID: "sb-1",
	}, []byte(`{"a":1}`), map[string]any{"b": 2})

	reqs, reqBodies, respBodies := b.Drain()
	require.Len(t, reqs, 1)
	require.Len(t, reqBodies, 1)
	require.Len(t, respBodies, 1)

	assert.Equal(t, ttl, reqs[0].TTL)
	assert.Equal(t, ttl, reqBodies[0].TTL,
		"request body must expire with its request, not outlive it")
	assert.Equal(t, ttl, respBodies[0].TTL,
		"response body must expire with its request, not outlive it")
}

// TestDrain_BodiesWithoutTTLStayZero keeps "no expiry configured" meaning no
// expiry, rather than inventing one.
func TestDrain_BodiesWithoutTTLStayZero(t *testing.T) {
	b := matched.NewBuffer(100)
	b.Add(matched.Request{
		ID: "req-1", RuleID: "rule-1",
		RequestBodyID: "rb-1", ResponseBodyID: "sb-1",
	}, []byte(`{}`), map[string]any{})

	_, reqBodies, respBodies := b.Drain()
	require.Len(t, reqBodies, 1)
	require.Len(t, respBodies, 1)
	assert.Zero(t, reqBodies[0].TTL)
	assert.Zero(t, respBodies[0].TTL)
}
