package matched_test

import (
	"encoding/json"
	"testing"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID_Unique(t *testing.T) {
	a := matched.NewID()
	b := matched.NewID()
	require.NotEmpty(t, a)
	assert.NotEqual(t, a, b)
}

func TestNewID_TimeOrdered(t *testing.T) {
	// UUID v7 is lexicographically time-ordered; later IDs sort after earlier.
	first := matched.NewID()
	second := matched.NewID()
	assert.Less(t, first, second)
}

func TestRequestIdentityRoundTrip(t *testing.T) {
	r := matched.Request{ID: "1", RuleID: "r", Protocol: "aws-sns", Identity: "AKIDEXAMPLE"}
	b, _ := json.Marshal(r)
	var out matched.Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Identity != "AKIDEXAMPLE" {
		t.Fatalf("identity = %q", out.Identity)
	}
}
