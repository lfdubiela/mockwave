# Dashboard Per-Rule TPS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show admin-dashboard traffic as per-rule throughput (req/s) over the last 30 minutes with a multi-line chart, top-10 filter chips, hover tooltips, and a current-TPS value in the Rule Hit Distribution panel.

**Architecture:** Extend the in-memory `metrics.Collector` with a per-rule minute-bucket ring (Approach A), reusing the existing `histRing` type. Add `RuleHistory(topN)` returning per-rule series and a `CurrentTPS` field on the per-rule snapshot. The `/api/metrics/history` endpoint returns the per-rule shape; the SSE Snapshot carries `current_tps`. The bundled `static/index.html` renders N SVG line paths, filter chips, a tooltip, and a TPS suffix on distribution rows.

**Tech Stack:** Go (stdlib `sync`, `time`, `sort`, `net/http`, `encoding/json`), vanilla JS + inline SVG.

---

## Reference: current code

`internal/metrics/history.go` defines:

```go
type MinuteBucket struct {
	Ts    time.Time `json:"ts"`
	Count int64     `json:"count"`
}

type histRing struct {
	mu      sync.Mutex
	slots   [30]MinuteBucket
	current int
	filled  bool
}
// func (h *histRing) record(at time.Time)
// func (h *histRing) snapshot() []MinuteBucket   // oldest→newest, zero slots skipped
```

`internal/metrics/collector.go` current `Collector`:

```go
type Collector struct {
	mu        sync.Mutex
	total     int64
	misses    int64
	latencies map[string][]float64
	names     map[string]string
	hist      histRing
}
```

---

## Task 1: histRing query helpers (window sum + last completed minute)

**Files:**
- Modify: `internal/metrics/history.go`
- Test: `internal/metrics/history_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/metrics/history_test.go`:

```go
func TestHistRing_WindowHits(t *testing.T) {
	var h histRing
	base := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	h.record(base)                       // minute 10:00 -> 1
	h.record(base.Add(10 * time.Second)) // minute 10:00 -> 2
	h.record(base.Add(time.Minute))      // minute 10:01 -> 1
	if got := h.windowHits(); got != 3 {
		t.Fatalf("windowHits = %d, want 3", got)
	}
}

func TestHistRing_LastCompletedCount(t *testing.T) {
	var h histRing
	base := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	h.record(base)                  // 10:00 -> 1
	h.record(base)                  // 10:00 -> 2
	h.record(base.Add(time.Minute)) // 10:01 -> 1 (in-progress when now is 10:01:30)

	now := base.Add(time.Minute + 30*time.Second) // 10:01:30 -> last completed minute is 10:00
	if got := h.lastCompletedCount(now); got != 2 {
		t.Fatalf("lastCompletedCount = %d, want 2", got)
	}

	// When only the in-progress minute exists, there is no completed minute.
	var h2 histRing
	h2.record(now) // 10:01
	if got := h2.lastCompletedCount(now); got != 0 {
		t.Fatalf("lastCompletedCount (only current) = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/metrics/ -run 'TestHistRing_(WindowHits|LastCompletedCount)' -v`
Expected: FAIL — `h.windowHits undefined`, `h.lastCompletedCount undefined`.

- [ ] **Step 3: Implement the helpers**

Append to `internal/metrics/history.go`:

```go
// windowHits returns the total count across all retained buckets.
func (h *histRing) windowHits() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var total int64
	for i := range h.slots {
		if !h.slots[i].Ts.IsZero() {
			total += h.slots[i].Count
		}
	}
	return total
}

// lastCompletedCount returns the count of the most recent bucket strictly
// before the minute containing now (i.e. the last fully-elapsed minute), or 0
// if no such bucket exists.
func (h *histRing) lastCompletedCount(now time.Time) int64 {
	curMinute := now.UTC().Truncate(time.Minute)
	h.mu.Lock()
	defer h.mu.Unlock()
	var best MinuteBucket
	for i := range h.slots {
		b := h.slots[i]
		if b.Ts.IsZero() || !b.Ts.Before(curMinute) {
			continue
		}
		if best.Ts.IsZero() || b.Ts.After(best.Ts) {
			best = b
		}
	}
	return best.Count
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/metrics/ -run 'TestHistRing_(WindowHits|LastCompletedCount)' -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/metrics/history.go internal/metrics/history_test.go
git commit -m "feat(metrics): add histRing window-hits and last-completed-minute helpers"
```

---

## Task 2: Per-rule ring + RuleSeries + RuleHistory(topN)

**Files:**
- Modify: `internal/metrics/collector.go`
- Test: `internal/metrics/collector_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/metrics/collector_test.go`:

```go
func TestCollector_RuleHistory_PerRuleAndRanking(t *testing.T) {
	c := metrics.NewCollector()
	// r1: 3 hits, r2: 1 hit, miss: 5 (must not appear as a rule)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r2", "Rule Two", 1)
	for i := 0; i < 5; i++ {
		c.RecordMiss()
	}

	series := c.RuleHistory(0) // all rules
	if len(series) != 2 {
		t.Fatalf("got %d rule series, want 2", len(series))
	}
	// Ranked by window hits desc: r1 first.
	if series[0].RuleID != "r1" || series[0].RuleName != "Rule One" {
		t.Fatalf("series[0] = %+v, want r1/Rule One", series[0])
	}
	if series[1].RuleID != "r2" {
		t.Fatalf("series[1] = %+v, want r2", series[1])
	}
	var r1Hits int64
	for _, b := range series[0].Buckets {
		r1Hits += b.Count
	}
	if r1Hits != 3 {
		t.Fatalf("r1 bucket total = %d, want 3", r1Hits)
	}
}

func TestCollector_RuleHistory_TopN(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("a", "A", 1)
	c.RecordHit("a", "A", 1)
	c.RecordHit("b", "B", 1)
	c.RecordHit("c", "C", 1) // single hit, lowest

	series := c.RuleHistory(2)
	if len(series) != 2 {
		t.Fatalf("topN=2 returned %d series", len(series))
	}
	if series[0].RuleID != "a" {
		t.Fatalf("top series = %s, want a", series[0].RuleID)
	}
}

func TestCollector_RuleHistory_Empty(t *testing.T) {
	c := metrics.NewCollector()
	if got := c.RuleHistory(10); len(got) != 0 {
		t.Fatalf("empty collector RuleHistory len = %d, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/metrics/ -run TestCollector_RuleHistory -v`
Expected: FAIL — `c.RuleHistory undefined`, `RuleSeries` unknown.

- [ ] **Step 3: Implement per-rule ring + RuleSeries + RuleHistory**

In `internal/metrics/collector.go`, add the `ruleHist` field to `Collector`:

```go
type Collector struct {
	mu        sync.Mutex
	total     int64
	misses    int64
	latencies map[string][]float64
	names     map[string]string
	hist      histRing
	ruleHist  map[string]*histRing // ruleID -> per-minute ring
}
```

Initialise it in `NewCollector`:

```go
func NewCollector() *Collector {
	return &Collector{
		latencies: make(map[string][]float64),
		names:     make(map[string]string),
		ruleHist:  make(map[string]*histRing),
	}
}
```

Record into the rule ring inside `RecordHit` (after the existing `c.mu.Unlock()`,
mirroring how `recordHistory` is called outside the main lock). Replace the body
of `RecordHit` with:

```go
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
```

Add the `RuleSeries` type and `RuleHistory` method (place after the `Snapshot`
method):

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/metrics/ -run TestCollector_RuleHistory -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full metrics package with the race detector**

Run: `go test ./internal/metrics/ -race`
Expected: `ok` (no data races; rings are mutex-guarded).

- [ ] **Step 6: Commit**

```bash
git add internal/metrics/collector.go internal/metrics/collector_test.go
git commit -m "feat(metrics): track per-rule minute history and expose RuleHistory(topN)"
```

---

## Task 3: CurrentTPS on per-rule Snapshot

**Files:**
- Modify: `internal/metrics/collector.go`
- Test: `internal/metrics/collector_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/metrics/collector_test.go`:

```go
func TestCollector_Snapshot_CurrentTPS(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("r1", "Rule One", 1)

	snap := c.Snapshot()
	if len(snap.Rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(snap.Rules))
	}
	// Only the in-progress minute has data, so no completed minute -> 0 tps.
	if snap.Rules[0].CurrentTPS != 0 {
		t.Fatalf("CurrentTPS = %v, want 0 (only in-progress minute)", snap.Rules[0].CurrentTPS)
	}
}
```

(Time-travel for a non-zero TPS is covered by `TestHistRing_LastCompletedCount`
in Task 1; the Snapshot test asserts the wiring and the zero-when-only-current
contract, which is what the collector can exercise with the real clock.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/metrics/ -run TestCollector_Snapshot_CurrentTPS -v`
Expected: FAIL — `snap.Rules[0].CurrentTPS undefined` (field does not exist).

- [ ] **Step 3: Add the field and compute it in Snapshot**

In `internal/metrics/collector.go`, add `CurrentTPS` to `RuleStats`:

```go
type RuleStats struct {
	RuleID     string  `json:"rule_id"`
	RuleName   string  `json:"rule_name"`
	Hits       int64   `json:"hits"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	CurrentTPS float64 `json:"current_tps"`
}
```

In `Snapshot()`, compute `CurrentTPS` for each rule using the rule ring's last
completed minute. Replace the rule-building loop body so each appended
`RuleStats` includes the TPS:

```go
	rules := make([]RuleStats, 0, len(c.latencies))
	for id, lats := range c.latencies {
		sorted := make([]float64, len(lats))
		copy(sorted, lats)
		sort.Float64s(sorted)

		var tps float64
		if rh := c.ruleHist[id]; rh != nil {
			tps = float64(rh.lastCompletedCount(at)) / 60.0
		}

		rules = append(rules, RuleStats{
			RuleID:     id,
			RuleName:   c.names[id],
			Hits:       int64(len(sorted)),
			P50Ms:      percentile(sorted, 50),
			P95Ms:      percentile(sorted, 95),
			CurrentTPS: tps,
		})
	}
```

Note: `at` is already captured at the top of `Snapshot()` before the lock, and
`c.ruleHist[id]` is read while holding `c.mu` (Snapshot holds the lock for the
whole body), so this is safe.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/metrics/ -run TestCollector_Snapshot_CurrentTPS -v`
Expected: PASS.

- [ ] **Step 5: Run the full metrics package**

Run: `go test ./internal/metrics/ -race`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/metrics/collector.go internal/metrics/collector_test.go
git commit -m "feat(metrics): add per-rule current_tps to snapshot"
```

---

## Task 4: History endpoint returns per-rule shape

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go:288-301`
- Modify: `internal/adapters/cfg/restapi/openapi.yaml`
- Test: `internal/adapters/cfg/restapi/server_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/cfg/restapi/server_test.go`:

```go
func TestAdminAPI_MetricsHistory_PerRule(t *testing.T) {
	col := metrics.NewCollector()
	col.RecordHit("r1", "Rule One", 2)
	col.RecordHit("r1", "Rule One", 3)
	col.RecordHit("r2", "Rule Two", 1)

	mux := restapi.NewMux(&memStore{}, nil, col, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	var body struct {
		Rules []struct {
			RuleID   string `json:"rule_id"`
			RuleName string `json:"rule_name"`
			Buckets  []struct {
				Count int64 `json:"count"`
			} `json:"buckets"`
		} `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Len(t, body.Rules, 2)
	assert.Equal(t, "r1", body.Rules[0].RuleID)        // busiest first
	assert.Equal(t, "Rule One", body.Rules[0].RuleName)
	var r1 int64
	for _, b := range body.Rules[0].Buckets {
		r1 += b.Count
	}
	assert.Equal(t, int64(2), r1)
}

func TestAdminAPI_MetricsHistory_EmptyRules(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, metrics.NewCollector(), nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"rules":[]}`, w.Body.String())
}
```

Confirm the test file already imports `"github.com/mockwave/mockwave/internal/metrics"`; if not, add it.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestAdminAPI_MetricsHistory -v`
Expected: FAIL — response still has `buckets`, not `rules`.

- [ ] **Step 3: Update the handler**

Replace `metricsHistory` (`internal/adapters/cfg/restapi/server.go:288-301`) with:

```go
func (a *adminAPI) metricsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	var rules []metrics.RuleSeries
	if a.collector != nil {
		rules = a.collector.RuleHistory(10)
	}
	if rules == nil {
		rules = []metrics.RuleSeries{}
	}
	writeJSON(w, 200, map[string]interface{}{"rules": rules})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestAdminAPI_MetricsHistory -v`
Expected: PASS (both).

- [ ] **Step 5: Document the new shape in openapi.yaml**

In `internal/adapters/cfg/restapi/openapi.yaml`, find the `/api/metrics/history`
path's 200 response schema (it currently documents a `buckets` array). Replace
its schema with:

```yaml
                rules:
                  type: array
                  description: >-
                    Per-rule per-minute request series for the top 10 rules by
                    hits within the retained 30-minute window, busiest first.
                  items:
                    type: object
                    properties:
                      rule_id:
                        type: string
                      rule_name:
                        type: string
                      buckets:
                        type: array
                        items:
                          type: object
                          properties:
                            ts:
                              type: string
                              format: date-time
                            count:
                              type: integer
```

If the existing block references a `MinuteBucket`/`buckets` schema only here,
leave any shared schema definitions untouched — only the history response body
changes.

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/adapters/cfg/restapi/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/cfg/restapi/server.go internal/adapters/cfg/restapi/server_test.go internal/adapters/cfg/restapi/openapi.yaml
git commit -m "feat(admin): metrics history endpoint returns top-10 per-rule series"
```

---

## Task 5: UI — multi-line chart, filter chips, tooltip

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (chart markup ~155-172, `loadChart`/`renderChart` ~979-1022)

- [ ] **Step 1: Replace the chart markup**

In `internal/adapters/cfg/restapi/static/index.html`, replace the chart block
(the `<svg id="req-chart">…</svg>` and surrounding container, lines ~155-172)
with a multi-path SVG, a tooltip div, and a chips container:

```html
  <!-- Multi-line throughput chart: req/s per rule -->
  <div style="margin-bottom:1.5rem;position:relative">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:0.5rem">
      <span style="color:var(--muted);font-size:0.75rem;text-transform:uppercase;letter-spacing:0.05em">Throughput per Rule — req/s (last 30 min)</span>
    </div>
    <div id="chart-skeleton" style="height:80px" class="sk"></div>
    <svg id="req-chart" width="100%" height="80" style="display:none;overflow:visible" viewBox="0 0 600 80" preserveAspectRatio="none">
      <g id="chart-lines"></g>
    </svg>
    <div id="chart-tooltip" style="display:none;position:absolute;pointer-events:none;background:var(--bg);border:1px solid var(--border);border-radius:0.25rem;padding:0.25rem 0.5rem;font-size:0.75rem;white-space:nowrap;z-index:5"></div>
    <div id="chart-no-data" style="display:none;color:var(--muted);font-size:0.8rem;text-align:center;padding:1.5rem 0">No request history yet.</div>
    <div id="chart-chips" style="display:flex;flex-wrap:wrap;gap:0.4rem;margin-top:0.5rem"></div>
  </div>
```

- [ ] **Step 2: Replace loadChart + renderChart with the multi-line renderer**

Replace `loadChart` and `renderChart` (lines ~979-1022) with:

```javascript
  const CHART_COLORS = ['#f59e0b','#3b82f6','#22c55e','#ef4444','#a855f7','#14b8a6','#eab308','#ec4899','#64748b','#f97316'];
  let _series = [];        // [{rule_id, rule_name, buckets:[{ts,count}]}]
  let _hidden = new Set(); // rule_ids toggled off

  async function loadChart() {
    try {
      const res = await fetch('/api/metrics/history');
      if (!res.ok) throw new Error('no history');
      const { rules } = await res.json();
      _series = rules || [];
      renderChart();
      renderChips();
    } catch (_) {
      document.getElementById('chart-skeleton').style.display = 'none';
      document.getElementById('chart-no-data').style.display = 'block';
    }
  }

  function renderChart() {
    const skeleton = document.getElementById('chart-skeleton');
    const svg = document.getElementById('req-chart');
    const noData = document.getElementById('chart-no-data');
    const g = document.getElementById('chart-lines');
    skeleton.style.display = 'none';
    noData.style.display = 'none';

    const visible = _series.filter(s => !_hidden.has(s.rule_id));
    if (_series.length === 0) {
      svg.style.display = 'none';
      g.innerHTML = '';
      noData.style.display = 'block';
      return;
    }
    svg.style.display = 'block';

    const W = 600, H = 80, pad = 4;
    // Max req/s across visible series (count/60), min 1 to avoid divide-by-zero.
    let maxTps = 0;
    for (const s of visible) {
      for (const b of s.buckets) maxTps = Math.max(maxTps, b.count / 60);
    }
    maxTps = Math.max(maxTps, 0.0001);

    g.innerHTML = visible.map(s => {
      const idx = _series.findIndex(x => x.rule_id === s.rule_id);
      const color = CHART_COLORS[idx % CHART_COLORS.length];
      const n = s.buckets.length;
      const pts = s.buckets.map((b, i) => {
        const x = n === 1 ? W / 2 : (i / (n - 1)) * W;
        const y = H - pad - ((b.count / 60 / maxTps) * (H - 2 * pad));
        return [x, y];
      });
      if (pts.length === 1) {
        return `<circle cx="${pts[0][0]}" cy="${pts[0][1]}" r="2" fill="${color}"/>`;
      }
      const d = pts.map((p, i) => (i === 0 ? `M${p[0]},${p[1]}` : `L${p[0]},${p[1]}`)).join(' ');
      return `<path d="${d}" fill="none" stroke="${color}" stroke-width="1.5"/>`;
    }).join('');

    attachChartTooltip(visible, W, maxTps);
  }

  function attachChartTooltip(visible, W, maxTps) {
    const svg = document.getElementById('req-chart');
    const tip = document.getElementById('chart-tooltip');
    svg.onmousemove = (ev) => {
      if (visible.length === 0) { tip.style.display = 'none'; return; }
      const rect = svg.getBoundingClientRect();
      const fx = (ev.clientX - rect.left) / rect.width; // 0..1
      // Nearest minute column across the longest visible series.
      let best = null, bestDist = Infinity;
      for (const s of visible) {
        const n = s.buckets.length;
        for (let i = 0; i < n; i++) {
          const x = n === 1 ? 0.5 : i / (n - 1);
          const dist = Math.abs(x - fx);
          if (dist < bestDist) {
            bestDist = dist;
            best = { name: s.rule_name || s.rule_id, count: s.buckets[i].count };
          }
        }
      }
      if (!best) { tip.style.display = 'none'; return; }
      tip.textContent = `requests: ${best.count} · ${best.name}`;
      tip.style.left = (ev.clientX - rect.left + 8) + 'px';
      tip.style.top = (ev.clientY - rect.top - 8) + 'px';
      tip.style.display = 'block';
    };
    svg.onmouseleave = () => { tip.style.display = 'none'; };
  }

  function renderChips() {
    const el = document.getElementById('chart-chips');
    if (!el) return;
    el.innerHTML = _series.map((s, idx) => {
      const color = CHART_COLORS[idx % CHART_COLORS.length];
      const off = _hidden.has(s.rule_id);
      const name = s.rule_name || s.rule_id;
      return `<button type="button" class="chart-chip" data-rule="${escapeAttr(s.rule_id)}"
        onclick="toggleSeries('${escapeAttr(s.rule_id)}')"
        style="display:inline-flex;align-items:center;gap:0.35rem;border:1px solid var(--border);background:var(--bg);
        border-radius:9999px;padding:0.15rem 0.6rem;font-size:0.75rem;cursor:pointer;opacity:${off ? '0.4' : '1'}">
        <span style="width:0.6rem;height:0.6rem;border-radius:50%;background:${color}"></span>${name}</button>`;
    }).join('');
  }

  function toggleSeries(ruleID) {
    if (_hidden.has(ruleID)) _hidden.delete(ruleID); else _hidden.add(ruleID);
    renderChart();
    renderChips();
  }
```

- [ ] **Step 3: Build the binary to confirm the embedded UI still compiles**

Run: `go build ./cmd/mockwave/`
Expected: builds with no error (the UI is embedded; a syntax-broken HTML still
compiles, so also run the smoke test in Task 6).

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): multi-line per-rule throughput chart with filter chips and tooltip"
```

---

## Task 6: UI — distribution TPS + smoke-test assertions

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (`renderBars` ~1059-1074)
- Test: `internal/adapters/cfg/restapi/server_test.go` (`TestAdminAPI_ServesUI`)

- [ ] **Step 1: Add TPS to the distribution rows**

Replace the `bar-meta` span inside `renderBars` (line ~1072) so it includes the
current TPS:

```javascript
          <span class="bar-meta">${r.hits} hits · ${(r.current_tps ?? 0).toFixed(1)} tps · p95 ${r.p95_ms != null ? r.p95_ms.toFixed(0) : '—'}ms</span>
```

- [ ] **Step 2: Extend the UI smoke test**

In `internal/adapters/cfg/restapi/server_test.go`, find `TestAdminAPI_ServesUI`
and add assertions for the new hooks (place after the existing
`assert.Contains(t, body, "btn-save-rule")` / weight-sum asserts):

```go
	assert.Contains(t, body, "chart-chips")
	assert.Contains(t, body, "toggleSeries")
	assert.Contains(t, body, "chart-tooltip")
	assert.Contains(t, body, "tps")
```

- [ ] **Step 3: Run the smoke test**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestAdminAPI_ServesUI -v`
Expected: PASS.

- [ ] **Step 4: Run the full suite**

Run: `go test ./...`
Expected: all `ok` (no failures).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html internal/adapters/cfg/restapi/server_test.go
git commit -m "feat(ui): show current tps per rule in hit distribution"
```

---

## Task 7: Manual verification

**Files:** none (verification only)

- [ ] **Step 1: Build and run with a couple of rules**

Run: `go build ./cmd/mockwave/ && ./mockwave --help`
Expected: builds and prints help (confirms binary is healthy).

- [ ] **Step 2: Confirm the full suite + vet are clean**

Run: `go vet ./... && go test ./...`
Expected: vet silent; all packages `ok`.

- [ ] **Step 3: (Optional, manual) eyeball the dashboard**

Start the server (e.g. `./mockwave --store json` with seeded rules), send traffic
to two rules, open the admin UI, and confirm: multiple colored lines, chips that
toggle lines, hover tooltip showing `requests: N · <rule>`, and `N.N tps` in the
Rule Hit Distribution rows.

---

## Notes for the implementer

- Keep all rings mutex-guarded; never read `histRing.slots` without the lock.
- `RuleHistory` reads each ring's `windowHits()`/`snapshot()` outside the
  collector lock — that is intentional and safe because each `histRing` has its
  own mutex; do not hold `c.mu` while calling them (avoids lock-ordering issues).
- The chart y-axis is req/s = `count / 60`; the tooltip shows raw `count`
  (requests in that minute), matching the spec wording `requests: 25`.
- Do not add a Misses or Total line — rules only (per the approved design).
