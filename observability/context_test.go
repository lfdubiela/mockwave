// observability/context_test.go
package observability_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

func TestStampRequest_RoundTrip(t *testing.T) {
	ctx := observability.StampRequest(context.Background(), "POST", "/orders/42", "http")
	ri := observability.FromContext(ctx)

	assert.Equal(t, "POST", ri.Method)
	assert.Equal(t, "/orders/42", ri.Path)
	assert.Equal(t, "http", ri.Protocol)
	assert.NotEmpty(t, ri.RequestID, "request ID must be generated")
}

func TestStampTraceID(t *testing.T) {
	ctx := observability.StampRequest(context.Background(), "GET", "/ping", "http")
	ctx = observability.StampTraceID(ctx, "trace-abc")
	ri := observability.FromContext(ctx)

	assert.Equal(t, "trace-abc", ri.TraceID)
}

func TestFromContext_EmptyContext(t *testing.T) {
	ri := observability.FromContext(context.Background())
	assert.Empty(t, ri.RequestID)
	assert.Empty(t, ri.Method)
	assert.Empty(t, ri.Path)
	assert.Empty(t, ri.Protocol)
	assert.Empty(t, ri.TraceID)
}
