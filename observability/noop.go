// observability/noop.go
package observability

import "context"

// NoopLogger discards all log entries. Safe to use as a default when
// no logging is configured.
type NoopLogger struct{}

func (NoopLogger) Debug(_ context.Context, _ string, _ ...Field)          {}
func (NoopLogger) Info(_ context.Context, _ string, _ ...Field)           {}
func (NoopLogger) Warn(_ context.Context, _ string, _ ...Field)           {}
func (NoopLogger) Error(_ context.Context, _ string, _ error, _ ...Field) {}

// NoopTracer creates no-op spans. The returned context is unchanged.
type NoopTracer struct{}

func (NoopTracer) Start(ctx context.Context, _ string, _ ...Attr) (context.Context, Span) {
	return ctx, NoopSpan{}
}

// NoopSpan does nothing. Implements Span.
type NoopSpan struct{}

func (NoopSpan) End()                    {}
func (NoopSpan) SetError(_ error)        {}
func (NoopSpan) SetAttr(_ string, _ any) {}

// NoopMetrics discards all metric observations.
type NoopMetrics struct{}

func (NoopMetrics) RecordRequest(_ context.Context, _ RequestAttrs)   {}
func (NoopMetrics) RecordUnmatched(_ context.Context, _, _, _ string) {}
