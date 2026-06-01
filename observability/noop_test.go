// observability/noop_test.go
package observability_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mockwave/mockwave/observability"
)

// Compile-time interface checks — test fails to build if any impl is wrong.
var (
	_ observability.Logger          = observability.NoopLogger{}
	_ observability.Tracer          = observability.NoopTracer{}
	_ observability.Span            = observability.NoopSpan{}
	_ observability.MetricsRecorder = observability.NoopMetrics{}
)

func TestNoopLogger_DoesNotPanic(t *testing.T) {
	l := observability.NoopLogger{}
	ctx := context.Background()
	l.Debug(ctx, "debug msg", observability.F("k", "v"))
	l.Info(ctx, "info msg")
	l.Warn(ctx, "warn msg")
	l.Error(ctx, "error msg", errors.New("oops"))
}

func TestNoopTracer_ReturnsContextAndSpan(t *testing.T) {
	tr := observability.NoopTracer{}
	ctx, span := tr.Start(context.Background(), "test-span", observability.A("key", "val"))
	if ctx == nil {
		t.Fatal("Start must return non-nil context")
	}
	span.End()
	span.SetError(errors.New("err"))
	span.SetAttr("k", 42)
}

func TestNoopMetrics_DoesNotPanic(t *testing.T) {
	m := observability.NoopMetrics{}
	ctx := context.Background()
	m.RecordRequest(ctx, observability.RequestAttrs{Method: "GET", Path: "/ping"})
	m.RecordUnmatched(ctx, "POST", "/missing", "http")
}
