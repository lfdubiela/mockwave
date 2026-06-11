// observability/metrics.go
package observability

import "context"

// RequestAttrs carries per-request attributes for metrics recording.
type RequestAttrs struct {
	Protocol  string
	Method    string
	Path      string
	RuleID    string
	RuleName  string
	LatencyMs float64
	// FaultProfileID is the chaos profile selected for this request ("" when none).
	FaultProfileID string
	// FaultType is the fault that fired: "error", "jitter", or "" when none.
	FaultType string
}

// MetricsRecorder records application-level metrics.
// Implementations may push to Prometheus, StatsD, CloudWatch, etc.
type MetricsRecorder interface {
	// RecordRequest records a completed (matched) request.
	RecordRequest(ctx context.Context, attrs RequestAttrs)
	// RecordUnmatched records a request that matched no rule.
	RecordUnmatched(ctx context.Context, method, path, protocol string)
}
