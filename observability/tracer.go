// observability/tracer.go
package observability

import "context"

// Attr is a tracing attribute key-value pair.
type Attr struct {
	Key   string
	Value any
}

// A is a convenience constructor for Attr.
func A(key string, value any) Attr { return Attr{Key: key, Value: value} }

// Tracer creates spans for distributed tracing.
type Tracer interface {
	// Start begins a new span and returns a child context carrying it.
	// Callers must call span.End() when the unit of work completes.
	Start(ctx context.Context, spanName string, attrs ...Attr) (context.Context, Span)
}

// Span represents a single unit of work in a trace.
type Span interface {
	End()
	SetError(err error)
	SetAttr(key string, value any)
}
