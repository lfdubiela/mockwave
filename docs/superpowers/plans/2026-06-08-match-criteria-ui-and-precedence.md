# Match Criteria UI + Tiered Precedence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose Headers/Query/Body match criteria in the admin rule form, implement JSONPath body matching in the matcher, and replace weighted-sum rule precedence with strict tiered (lexicographic) ordering.

**Architecture:** Backend changes are in `internal/domain/matching/matcher.go` (a hand-rolled JSON dotted-path evaluator for body matching + a tuple-based `specificity`/sort). Frontend changes are entirely in the single-file admin UI `internal/adapters/cfg/restapi/static/index.html` (three repeatable key/value groups mirroring the existing `_buckets` pattern, plus table chips). Backend is TDD with Go tests; UI is manually verified.

**Tech Stack:** Go 1.x (stdlib `encoding/json`, `strconv`, `path`, `strings`; testify for tests). Vanilla HTML/CSS/JS (no build step).

---

## File Structure

- `internal/domain/matching/matcher.go` — add body matching to `matchRule`; add dotted-path evaluator helpers; replace `specificity()` with tuple scoring and update `sortBySpecificity`.
- `internal/domain/matching/matcher_test.go` — new tests for body matching and tiered precedence.
- `internal/adapters/cfg/restapi/static/index.html` — Headers/Query/Body form groups, save/edit/reset wiring, rules-table matcher chips.

Backend tasks (1–3) land first and independently. UI tasks (4–7) depend only on the model fields, which already exist — they can proceed in parallel, but are ordered after backend here for a coherent commit history.

---

## Task 1: Body matcher — dotted-path evaluator + matchRule integration

**Files:**
- Modify: `internal/domain/matching/matcher.go`
- Test: `internal/domain/matching/matcher_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/matching/matcher_test.go`:

```go
func mkBodyRule(id string, body map[string]string) domain.Rule {
	return domain.Rule{
		ID:      id,
		Match:   domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/orders", Body: body},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
}

func bodyReq(raw string) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "POST", Path: "/orders", Body: []byte(raw),
	}}
}

func TestConditionMatchStage_BodyTopLevel(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`{"status":"paid"}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyNested(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.user.id": "7"})})
	pctx := bodyReq(`{"user":{"id":7}}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyArrayIndex(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.items.0.id": "42"})})
	pctx := bodyReq(`{"items":[{"id":42}]}`)
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "r1", pctx.Matched.ID)
}

func TestConditionMatchStage_BodyMismatch(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`{"status":"pending"}`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestConditionMatchStage_BodyMalformedJSON(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.status": "paid"})})
	pctx := bodyReq(`not json`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestConditionMatchStage_BodyNonLeaf(t *testing.T) {
	stage := matching.NewConditionMatchStage([]domain.Rule{mkBodyRule("r1", map[string]string{"$.user": "x"})})
	pctx := bodyReq(`{"user":{"id":7}}`)
	assert.Error(t, stage.Execute(context.Background(), pctx))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/matching/ -run TestConditionMatchStage_Body -v`
Expected: FAIL — body conditions currently ignored, so `BodyMismatch`/`BodyMalformedJSON`/`BodyNonLeaf` match when they should not.

- [ ] **Step 3: Add body matching + path evaluator to `matcher.go`**

Add `encoding/json` and `strconv` to the import block:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)
```

In `matchRule`, insert the body block immediately before the final `return true`:

```go
	if len(m.Body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(req.Body, &parsed); err != nil {
			return false
		}
		for pathExpr, want := range m.Body {
			leaf, ok := resolvePath(parsed, pathExpr)
			if !ok || leafToString(leaf) != want {
				return false
			}
		}
	}
	return true
```

Add these helpers at the end of the file:

```go
// resolvePath walks a dotted JSON path (optionally prefixed with "$." or "$")
// and returns the leaf value. Numeric segments index into arrays. The second
// return is false if the path does not resolve to a scalar leaf.
func resolvePath(root interface{}, expr string) (interface{}, bool) {
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, false
	}
	cur := root
	for _, seg := range strings.Split(expr, ".") {
		switch node := cur.(type) {
		case map[string]interface{}:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []interface{}:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	// Reject non-scalar leaves (objects/arrays).
	switch cur.(type) {
	case map[string]interface{}, []interface{}:
		return nil, false
	}
	return cur, true
}

// leafToString stringifies a scalar JSON value for exact comparison. JSON
// numbers decode to float64; render integers without a trailing ".0".
func leafToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/matching/ -run TestConditionMatchStage_Body -v`
Expected: PASS (all six body tests).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/matching/matcher.go internal/domain/matching/matcher_test.go
git commit -m "feat(matching): JSONPath body matching with hand-rolled path evaluator"
```

---

## Task 2: Tiered precedence — tuple scoring + stable sort

**Files:**
- Modify: `internal/domain/matching/matcher.go:64-80` (`sortBySpecificity`, `specificity`)
- Test: `internal/domain/matching/matcher_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/matching/matcher_test.go`:

```go
// helper: rule with explicit match criteria and an id
func mkMatchRule(id string, m domain.MatchCriteria) domain.Rule {
	return domain.Rule{ID: id, Match: m, Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}}}
}

func TestPrecedence_HeaderBeatsExactPath(t *testing.T) {
	wild := mkMatchRule("wild", domain.MatchCriteria{Protocol: "http", Path: "/*", Headers: map[string]string{"x-cid": "1"}})
	exact := mkMatchRule("exact", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{exact, wild})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders", Headers: map[string]string{"x-cid": "1"},
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "wild", pctx.Matched.ID)
}

func TestPrecedence_ExactBeatsWildcard(t *testing.T) {
	wild := mkMatchRule("wild", domain.MatchCriteria{Protocol: "http", Path: "/*"})
	exact := mkMatchRule("exact", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{wild, exact})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/orders"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "exact", pctx.Matched.ID)
}

func TestPrecedence_DeeperWildcardBeatsRootWildcard(t *testing.T) {
	root := mkMatchRule("root", domain.MatchCriteria{Protocol: "http", Path: "/*"})
	deep := mkMatchRule("deep", domain.MatchCriteria{Protocol: "http", Path: "/api/v1/*"})
	stage := matching.NewConditionMatchStage([]domain.Rule{root, deep})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/api/v1/x"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "deep", pctx.Matched.ID)
}

func TestPrecedence_BodyBeatsQuery(t *testing.T) {
	q := mkMatchRule("q", domain.MatchCriteria{Protocol: "http", Path: "/orders", Query: map[string]string{"a": "1"}})
	b := mkMatchRule("b", domain.MatchCriteria{Protocol: "http", Path: "/orders", Body: map[string]string{"$.k": "v"}})
	stage := matching.NewConditionMatchStage([]domain.Rule{q, b})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders",
		Query: map[string]string{"a": "1"}, Body: []byte(`{"k":"v"}`),
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "b", pctx.Matched.ID)
}

func TestPrecedence_StableWhenEqual(t *testing.T) {
	r1 := mkMatchRule("first", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	r2 := mkMatchRule("second", domain.MatchCriteria{Protocol: "http", Path: "/orders"})
	stage := matching.NewConditionMatchStage([]domain.Rule{r1, r2})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Protocol: "http", Method: "GET", Path: "/orders"}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "first", pctx.Matched.ID)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/matching/ -run TestPrecedence -v`
Expected: FAIL — `HeaderBeatsExactPath` fails because current weighted sum gives exact-path `headers*10`-less score vs header rule; `BodyBeatsQuery` fails because body has no specificity weight today.

- [ ] **Step 3: Replace `specificity` + `sortBySpecificity`**

Replace lines 64-80 (`sortBySpecificity` and `specificity`) with:

```go
// sortBySpecificity orders rules most-specific first using a strict tiered
// (lexicographic) comparison. Stable: equal rules keep input order.
func sortBySpecificity(rules []domain.Rule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0; j-- {
			if moreSpecific(rules[j], rules[j-1]) {
				rules[j], rules[j-1] = rules[j-1], rules[j]
			} else {
				break
			}
		}
	}
}

// moreSpecific reports whether a ranks strictly above b under the tiered order
// (headers, url, body, query) compared element-wise.
func moreSpecific(a, b domain.Rule) bool {
	ta, tb := specTuple(a), specTuple(b)
	for i := range ta {
		if ta[i] != tb[i] {
			return ta[i] > tb[i]
		}
	}
	return false
}

// specTuple builds the precedence tuple: (headers, url, body, query).
func specTuple(r domain.Rule) [4]int {
	return [4]int{
		len(r.Match.Headers),
		urlScore(r.Match.Path),
		len(r.Match.Body),
		len(r.Match.Query),
	}
}

// urlScore: exact non-empty path -> 1000; wildcard -> 100 + literal segment
// count; empty -> 0. Exact always outranks any wildcard.
func urlScore(p string) int {
	if p == "" {
		return 0
	}
	if !strings.Contains(p, "*") {
		return 1000
	}
	literal := 0
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && !strings.Contains(seg, "*") {
			literal++
		}
	}
	return 100 + literal
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/matching/ -run TestPrecedence -v`
Expected: PASS (all five precedence tests).

- [ ] **Step 5: Run the full matching package to catch regressions**

Run: `go test ./internal/domain/matching/ -v`
Expected: PASS — all existing tests still green under the new ordering.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/matching/matcher.go internal/domain/matching/matcher_test.go
git commit -m "feat(matching): strict tiered precedence (headers > url > body > query)"
```

---

## Task 3: Full backend verification

**Files:** none (verification only)

- [ ] **Step 1: Run the whole suite**

Run: `go test ./...`
Expected: PASS for all packages (no regression from precedence change in integration tests).

- [ ] **Step 2: If any integration test fails**

Inspect the failure. The only behavioral change is rule ordering. If an integration fixture relied on the old weighted-sum order, update the fixture's expected rule to match tiered precedence and note it in the commit. Do NOT weaken the new ordering to fit a stale fixture without confirming the new order is correct per the spec tiers.

- [ ] **Step 3: Commit (only if fixtures changed)**

```bash
git add -A
git commit -m "test: align integration fixtures with tiered precedence"
```

---

## Task 4: UI — Headers & Query match groups in the rule modal

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`

- [ ] **Step 1: Add the markup containers**

In the `<!-- Rule modal -->` section, find the Match Criteria `form-row` ending at the Path `form-group` (around line 299-304, the `<div class="chip-row" id="path-chips"></div>` then two closing `</div>`). Immediately after that closing `form-row` `</div>` and before the `<div class="section-divider" ...>Response Buckets`, insert:

```html
    <div class="form-group" style="margin-top:0.5rem">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <label style="margin-bottom:0">Header Matches (exact)</label>
        <button class="btn-sm btn-ghost" type="button" onclick="addHeader()">+ Add header</button>
      </div>
      <div id="headers-container"></div>
    </div>

    <div class="form-group" style="margin-top:0.5rem">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <label style="margin-bottom:0">Query Matches (exact)</label>
        <button class="btn-sm btn-ghost" type="button" onclick="addQuery()">+ Add query param</button>
      </div>
      <div id="query-container"></div>
    </div>

    <div class="form-group" style="margin-top:0.5rem">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <label style="margin-bottom:0">Body Matches (JSONPath, exact value)</label>
        <button class="btn-sm btn-ghost" type="button" onclick="addBody()">+ Add body match</button>
      </div>
      <div id="body-container"></div>
      <p style="color:var(--muted);font-size:0.72rem;margin-top:0.25rem">Matches JSON request bodies. JSONPath like <span class="mono">$.user.id</span>, exact value.</p>
    </div>
```

- [ ] **Step 2: Add the key/value state + render helpers**

In the `<script>` block, immediately after the `let _buckets = [];` line (around line 364), add:

```javascript
  // ── Match criteria key/value groups ──────────────────────────────────────────
  let _headers = []; // [{key, value}]
  let _query   = [];
  let _body    = []; // body uses JSONPath in `key`

  function renderKV(arr, containerId, arrName, keyPlaceholder) {
    const c = document.getElementById(containerId);
    if (!c) return;
    if (arr.length === 0) { c.innerHTML = ''; return; }
    c.innerHTML = arr.map((row, i) => `
      <div class="form-row" style="margin-top:0.35rem">
        <div class="form-group" style="margin-bottom:0">
          <input value="${escapeAttr(row.key)}" placeholder="${escapeAttr(keyPlaceholder)}"
            oninput="${arrName}[${i}].key=this.value">
        </div>
        <div class="form-group" style="margin-bottom:0">
          <input value="${escapeAttr(row.value)}" placeholder="value"
            oninput="${arrName}[${i}].value=this.value">
        </div>
        <button class="btn-sm btn-danger" type="button" style="align-self:center"
          onclick="removeKV('${arrName}', ${i})">×</button>
      </div>`).join('');
  }

  function renderHeaders() { renderKV(_headers, 'headers-container', '_headers', 'X-Client-Id'); }
  function renderQuery()   { renderKV(_query,   'query-container',   '_query',   'q'); }
  function renderBody()    { renderKV(_body,    'body-container',    '_body',    '$.user.id'); }

  function addHeader() { _headers.push({key:'',value:''}); renderHeaders(); }
  function addQuery()  { _query.push({key:'',value:''});   renderQuery(); }
  function addBody()   { _body.push({key:'',value:''});    renderBody(); }

  function removeKV(arrName, i) {
    const map = { _headers, _query, _body };
    map[arrName].splice(i, 1);
    if (arrName === '_headers') renderHeaders();
    else if (arrName === '_query') renderQuery();
    else renderBody();
  }

  // Build a {key:value} object from a KV array, skipping empty keys.
  function kvToObject(arr) {
    const out = {};
    for (const row of arr) {
      const k = (row.key || '').trim();
      if (k) out[k] = row.value || '';
    }
    return out;
  }

  // Populate a KV array from a {key:value} object.
  function objectToKV(obj) {
    return Object.entries(obj || {}).map(([key, value]) => ({ key, value: String(value) }));
  }
```

- [ ] **Step 3: Render the groups when opening for a NEW rule**

In `openRuleModal(prefill)`, after `_buckets = [defaultBucket()];` and before `renderBuckets();`, add:

```javascript
    _headers = objectToKV(prefill?.headers);
    _query   = objectToKV(prefill?.query);
    _body    = [];
    renderHeaders(); renderQuery(); renderBody();
```

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): header/query/body match groups in rule modal (markup + state)"
```

---

## Task 5: UI — wire match groups into Save

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`

- [ ] **Step 1: Replace the hardcoded match object in `saveRule()`**

In `saveRule()`, find the `match:` block (around lines 874-879):

```javascript
        match: {
          protocol: document.getElementById('r-protocol').value,
          method:   document.getElementById('r-method').value,
          path:     document.getElementById('r-path').value.trim(),
          headers: {}, query: {}
        },
```

Replace with:

```javascript
        match: {
          protocol: document.getElementById('r-protocol').value,
          method:   document.getElementById('r-method').value,
          path:     document.getElementById('r-path').value.trim(),
          headers: kvToObject(_headers),
          query:   kvToObject(_query),
          body:    kvToObject(_body)
        },
```

- [ ] **Step 2: Manual verify — create a rule with all three matchers**

Run the server (per project run instructions), open the admin UI, click **+ Add Rule**:
- Path `/orders`, add header `X-Client-Id` = `1`, query `mode` = `test`, body `$.status` = `paid`.
- Set one bucket weight to 100, Save.

Then inspect: `curl -s localhost:<port>/api/rules | jq '.[] | select(.id==...) | .match'`
Expected: `match.headers`, `match.query`, `match.body` all populated with the entered values; empty rows absent.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): persist header/query/body matchers on save"
```

---

## Task 6: UI — round-trip match groups on Edit

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`

- [ ] **Step 1: Populate the groups in `editRule(r)`**

In `editRule(r)`, after `document.getElementById('r-path').value = r.match?.path || '';` (around line 741), add:

```javascript
    _headers = objectToKV(r.match?.headers);
    _query   = objectToKV(r.match?.query);
    _body    = objectToKV(r.match?.body);
    renderHeaders(); renderQuery(); renderBody();
```

- [ ] **Step 2: Manual verify — edit round-trips**

Reload the admin UI, go to **Rules**, click **Edit** on the rule from Task 5.
Expected: Header row `X-Client-Id`/`1`, Query row `mode`/`test`, Body row `$.status`/`paid` all pre-filled. Change the body value to `pending`, Save, re-edit — confirm `pending` persisted.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): round-trip header/query/body matchers on edit"
```

---

## Task 7: UI — matcher chips in the rules table

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`

- [ ] **Step 1: Add a chip-summary helper**

In the `<script>` block, after `methodBadge` (around line 602), add:

```javascript
  // Compact summary of configured header/query/body matchers for the rules table.
  function matchChips(match) {
    const parts = [];
    for (const [k, v] of Object.entries(match?.headers || {})) parts.push('H:' + k + '=' + v);
    for (const [k, v] of Object.entries(match?.query   || {})) parts.push('Q:' + k + '=' + v);
    for (const [k, v] of Object.entries(match?.body    || {})) parts.push('B:' + k + '=' + v);
    if (parts.length === 0) return '';
    const shown = parts.slice(0, 3).map(p =>
      `<span class="chip" style="cursor:default">${escapeHtml(p)}</span>`).join('');
    const extra = parts.length > 3 ? `<span class="chip" style="cursor:default">+${parts.length - 3}</span>` : '';
    return `<div class="chip-row" style="margin-top:0.3rem">${shown}${extra}</div>`;
  }
```

- [ ] **Step 2: Render chips under the Path cell in `loadRules()`**

In `loadRules()`, find the Path cell (around line 693):

```javascript
          <td class="mono">${r.match?.path || '/'}</td>
```

Replace with:

```javascript
          <td class="mono">${r.match?.path || '/'}${matchChips(r.match)}</td>
```

- [ ] **Step 3: Manual verify — chips render**

Reload the admin UI **Rules** tab.
Expected: the rule from Task 5 shows chips `H:X-Client-Id=1  Q:mode=test  B:$.status=paid` under its path. A rule with no matchers shows no chips.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): show configured matchers as chips in rules table"
```

---

## Task 8: End-to-end precedence verification

**Files:** none (verification only)

- [ ] **Step 1: Create two competing rules via the UI**

- Rule A: path `/*`, header `X-Client-Id` = `1`, bucket returns status `201`.
- Rule B: path `/orders`, no header, bucket returns status `200`.

- [ ] **Step 2: Send a request that matches both**

Run: `curl -s -o /dev/null -w "%{http_code}\n" -H "X-Client-Id: 1" localhost:<port>/orders`
Expected: `201` — Rule A (header tier) wins over Rule B (exact path), confirming `headers > url`.

- [ ] **Step 3: Send a request that matches only B**

Run: `curl -s -o /dev/null -w "%{http_code}\n" localhost:<port>/orders`
Expected: `200` — no header, so Rule A's header condition fails; Rule B matches.

---

## Self-Review Notes

- **Spec coverage:** UI Headers/Query/Body groups (Tasks 4-6), table chips (Task 7), body JSONPath matcher (Task 1), tiered precedence (Task 2), tests (Tasks 1-3, 8). All spec sections mapped.
- **Type consistency:** `_headers`/`_query`/`_body` arrays of `{key,value}`; `kvToObject`/`objectToKV` used consistently across open/save/edit; `resolvePath`/`leafToString`/`specTuple`/`urlScore`/`moreSpecific` names consistent across tasks.
- **Behavioral note:** Task 2 changes existing ordering; Task 3 gates on full suite to catch fixture dependence.
