package metrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

// RuleStats is the per-rule metrics snapshot.
type RuleStats struct {
	RuleID   string  `json:"rule_id"`
	RuleName string  `json:"rule_name"`
	Hits     int64   `json:"hits"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

// Snapshot is a point-in-time view of all metrics.
type Snapshot struct {
	TotalRequests int64       `json:"total_requests"`
	Misses        int64       `json:"misses"`
	Rules         []RuleStats `json:"rules"`
	At            time.Time   `json:"at"`
}

// Collector accumulates request metrics in memory.
// All methods are safe for concurrent use.
type Collector struct {
	mu        sync.Mutex
	total     int64
	misses    int64
	latencies map[string][]float64 // ruleID -> latency samples in ms
	names     map[string]string    // ruleID -> rule name
}

// NewCollector creates an empty Collector.
func NewCollector() *Collector {
	return &Collector{
		latencies: make(map[string][]float64),
		names:     make(map[string]string),
	}
}

// RecordHit records a matched request.
func (c *Collector) RecordHit(ruleID, ruleName string, latencyMs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	c.latencies[ruleID] = append(c.latencies[ruleID], latencyMs)
	c.names[ruleID] = ruleName
}

// RecordMiss records a request that matched no rule.
func (c *Collector) RecordMiss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	c.misses++
}

// Snapshot returns a consistent point-in-time view, sorted by hits descending.
func (c *Collector) Snapshot() Snapshot {
	at := time.Now() // capture timestamp before taking the lock
	c.mu.Lock()
	defer c.mu.Unlock()

	rules := make([]RuleStats, 0, len(c.latencies))
	for id, lats := range c.latencies {
		sorted := make([]float64, len(lats))
		copy(sorted, lats)
		sort.Float64s(sorted)
		rules = append(rules, RuleStats{
			RuleID:   id,
			RuleName: c.names[id],
			Hits:     int64(len(sorted)),
			P50Ms:    percentile(sorted, 50),
			P95Ms:    percentile(sorted, 95),
		})
	}
	// Sort by hits descending for stable UI ordering.
	sort.Slice(rules, func(i, j int) bool { return rules[i].Hits > rules[j].Hits })

	return Snapshot{
		TotalRequests: c.total,
		Misses:        c.misses,
		Rules:         rules,
		At:            at,
	}
}

// percentile returns the p-th percentile of a sorted slice (e.g. p=95 -> P95).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(sorted))*p/100)) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}
