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
	hist      histRing
	ruleHist  map[string]*histRing // ruleID -> per-minute ring
}

// NewCollector creates an empty Collector.
func NewCollector() *Collector {
	return &Collector{
		latencies: make(map[string][]float64),
		names:     make(map[string]string),
		ruleHist:  make(map[string]*histRing),
	}
}

// RecordHit records a matched request.
func (c *Collector) RecordHit(ruleID, ruleName string, latencyMs float64) {
	c.mu.Lock()
	c.total++
	c.latencies[ruleID] = append(c.latencies[ruleID], latencyMs)
	c.names[ruleID] = ruleName
	rh, ok := c.ruleHist[ruleID]
	if !ok {
		rh = &histRing{}
		c.ruleHist[ruleID] = rh
	}
	c.mu.Unlock()

	now := time.Now()
	c.recordHistory(now)
	rh.record(now)
}

// RecordMiss records a request that matched no rule.
func (c *Collector) RecordMiss() {
	c.mu.Lock()
	c.total++
	c.misses++
	c.mu.Unlock()
	c.recordHistory(time.Now())
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

// RuleSeries is a per-rule per-minute time series for the retained window.
type RuleSeries struct {
	RuleID   string         `json:"rule_id"`
	RuleName string         `json:"rule_name"`
	Buckets  []MinuteBucket `json:"buckets"`
}

// RuleHistory returns per-rule minute series ranked by total hits within the
// retained window (busiest first). topN<=0 returns all rules. Ties are broken
// by ruleID for deterministic ordering.
func (c *Collector) RuleHistory(topN int) []RuleSeries {
	c.mu.Lock()
	type entry struct {
		id, name string
		ring     *histRing
	}
	entries := make([]entry, 0, len(c.ruleHist))
	for id, ring := range c.ruleHist {
		entries = append(entries, entry{id: id, name: c.names[id], ring: ring})
	}
	c.mu.Unlock()

	sort.Slice(entries, func(i, j int) bool {
		hi, hj := entries[i].ring.windowHits(), entries[j].ring.windowHits()
		if hi != hj {
			return hi > hj
		}
		return entries[i].id < entries[j].id
	})

	if topN > 0 && len(entries) > topN {
		entries = entries[:topN]
	}

	out := make([]RuleSeries, 0, len(entries))
	for _, e := range entries {
		out = append(out, RuleSeries{
			RuleID:   e.id,
			RuleName: e.name,
			Buckets:  e.ring.snapshot(),
		})
	}
	return out
}

func (c *Collector) recordHistory(at time.Time) {
	c.hist.record(at)
}

// History returns a chronological snapshot of per-minute request counts
// for up to the last 30 minutes.
func (c *Collector) History() []MinuteBucket {
	return c.hist.snapshot()
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
