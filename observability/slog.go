// observability/slog.go
package observability

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// SlogLogger is a Logger backed by the standard library's log/slog package.
// It writes JSON to stdout and automatically extracts request-scoped fields
// (request_id, method, path, protocol, trace_id) from the context.
type SlogLogger struct {
	handler slog.Handler
}

// NewSlogLogger returns a SlogLogger that writes JSON to stdout at Debug level.
func NewSlogLogger() *SlogLogger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	return &SlogLogger{handler: h}
}

// NewSlogLoggerWith returns a SlogLogger using the provided slog.Handler.
// Useful in tests to inject a capturing handler.
func NewSlogLoggerWith(h slog.Handler) *SlogLogger {
	return &SlogLogger{handler: h}
}

func (l *SlogLogger) log(ctx context.Context, level slog.Level, msg string, err error, fields []Field) {
	if !l.handler.Enabled(ctx, level) {
		return
	}
	attrs := contextAttrs(ctx)
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}
	for _, f := range fields {
		attrs = append(attrs, slog.Any(f.Key, f.Value))
	}
	// pc=0 intentionally omits source-file/line from log output.
	// Source location adds overhead (runtime.Callers) that outweighs its value
	// in a mock server where all logging flows through this single method.
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	_ = l.handler.Handle(ctx, r)
}

// Debug logs at DEBUG level.
func (l *SlogLogger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelDebug, msg, nil, fields)
}

// Info logs at INFO level.
func (l *SlogLogger) Info(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelInfo, msg, nil, fields)
}

// Warn logs at WARN level.
func (l *SlogLogger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelWarn, msg, nil, fields)
}

// Error logs at ERROR level. The err parameter is always included in the output.
func (l *SlogLogger) Error(ctx context.Context, msg string, err error, fields ...Field) {
	l.log(ctx, slog.LevelError, msg, err, fields)
}

// contextAttrs extracts request-scoped slog attributes from ctx.
// Only non-empty fields are included to keep log lines clean.
func contextAttrs(ctx context.Context) []slog.Attr {
	ri := FromContext(ctx)
	var attrs []slog.Attr
	if ri.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", ri.RequestID))
	}
	if ri.Method != "" {
		attrs = append(attrs, slog.String("method", ri.Method))
	}
	if ri.Path != "" {
		attrs = append(attrs, slog.String("path", ri.Path))
	}
	if ri.Protocol != "" {
		attrs = append(attrs, slog.String("protocol", ri.Protocol))
	}
	if ri.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", ri.TraceID))
	}
	return attrs
}
