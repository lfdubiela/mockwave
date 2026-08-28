// observability/slog_test.go
package observability_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHandler accumulates slog.Record values for inspection in tests.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler         { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler              { return h }

func (h *captureHandler) last() slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records[len(h.records)-1]
}

func (h *captureHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

func attrMap(r slog.Record) map[string]any {
	m := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	return m
}

func TestSlogLogger_InfoWritesMessage(t *testing.T) {
	h := &captureHandler{}
	l := observability.NewSlogLoggerWith(h)

	ctx := context.Background()
	l.Info(ctx, "hello world")

	require.Equal(t, 1, h.count())
	rec := h.last()
	assert.Equal(t, "hello world", rec.Message)
	assert.Equal(t, slog.LevelInfo, rec.Level)
}

func TestSlogLogger_ErrorIncludesErr(t *testing.T) {
	h := &captureHandler{}
	l := observability.NewSlogLoggerWith(h)

	ctx := context.Background()
	l.Error(ctx, "something broke", errors.New("disk full"))

	require.Equal(t, 1, h.count())
	rec := h.last()
	attrs := attrMap(rec)
	assert.Equal(t, "disk full", attrs["error"].(error).Error())
}

func TestSlogLogger_ExtractsContextFields(t *testing.T) {
	h := &captureHandler{}
	l := observability.NewSlogLoggerWith(h)

	ctx := observability.StampRequest(context.Background(), "DELETE", "/users/7", "http")
	ctx = observability.StampTraceID(ctx, "trace-xyz")
	l.Info(ctx, "request handled")

	attrs := attrMap(h.last())
	assert.Equal(t, "DELETE", attrs["method"])
	assert.Equal(t, "/users/7", attrs["path"])
	assert.Equal(t, "http", attrs["protocol"])
	assert.Equal(t, "trace-xyz", attrs["trace_id"])
	assert.NotEmpty(t, attrs["request_id"])
}

func TestSlogLogger_WarnWritesWarnLevel(t *testing.T) {
	h := &captureHandler{}
	l := observability.NewSlogLoggerWith(h)

	l.Warn(context.Background(), "almost out of memory")

	require.Equal(t, 1, h.count())
	assert.Equal(t, slog.LevelWarn, h.last().Level)
}

func TestSlogLogger_FieldsAttached(t *testing.T) {
	h := &captureHandler{}
	l := observability.NewSlogLoggerWith(h)

	l.Debug(context.Background(), "msg", observability.F("rule_id", "r42"), observability.F("latency_ms", 12.5))

	attrs := attrMap(h.last())
	assert.Equal(t, "r42", attrs["rule_id"])
	assert.Equal(t, 12.5, attrs["latency_ms"])
}
