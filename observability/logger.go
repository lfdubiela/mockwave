// observability/logger.go
package observability

import "context"

// Field is a structured log field.
type Field struct {
	Key   string
	Value any
}

// F constructs a Field. Convenience constructor for use at call sites.
func F(key string, value any) Field { return Field{Key: key, Value: value} }

// Logger is the project-wide structured logging interface.
// Every method accepts a context so implementations can extract
// request-scoped values (request ID, trace ID, method, path) and
// attach them as structured fields automatically.
type Logger interface {
	Debug(ctx context.Context, msg string, fields ...Field)
	Info(ctx context.Context, msg string, fields ...Field)
	Warn(ctx context.Context, msg string, fields ...Field)
	// Error includes an explicit err parameter that is always logged
	// even when fields is empty.
	Error(ctx context.Context, msg string, err error, fields ...Field)
}
