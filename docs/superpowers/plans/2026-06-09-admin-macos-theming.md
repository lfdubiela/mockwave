# Admin macOS Vibrancy Restyle + Theme System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the entire Mockwave admin UI into a macOS-vibrancy aesthetic and add a cookie-backed theme system with two themes (`macos` default, `amber` legacy) selectable from the nav.

**Architecture:** Single embedded `index.html` (no build step). All styling is driven by CSS custom properties scoped under `[data-theme="..."]` on `<html>`. The Go server reads a `mw-theme` cookie and injects the matching `data-theme` attribute into the served HTML so the correct theme paints on first load. A nav switcher writes the cookie and flips the attribute live. Components reference tokens only — themes differ solely by token values, no per-component branches.

**Tech Stack:** Go (`net/http`, `embed`), vanilla HTML/CSS/JS, `testify` for Go tests, playwright-mcp for visual checks.

---

## Notes on conventions

- The CSS lives inline in `internal/adapters/cfg/restapi/static/index.html` inside a `<style>` block (lines ~7-340). JS is in a `<script>` block (lines ~341-1509).
- **Token strategy to avoid breakage:** existing component rules use `var(--bg/--surface/--border/--text/--accent/--accent-dim/--muted/--success/--error)`. We KEEP all those names and ADD new ones (`--surface-2 --text-dim --border-hair --accent-fg --radius --radius-lg --shadow-modal --shadow-card --blur --ease-spring --dur-modal --font`). The `amber` theme reproduces today's exact values, so the page looks identical under `amber` after Task 2. The `macos` theme supplies new values. Later tasks migrate hardcoded values to tokens.
- CSS visual tasks cannot be unit-tested; they use a **manual playwright-mcp verification step** instead of an automated test. Only the Go injection task uses real TDD.
- Commit after every task.

---

## File Structure

- `internal/adapters/cfg/restapi/ui.go` — MODIFY. Add a `GET /` handler that injects `data-theme` from the `mw-theme` cookie; keep the existing `http.FileServer` for other static assets.
- `internal/adapters/cfg/restapi/ui_test.go` — CREATE. Tests for cookie → injected attribute.
- `internal/adapters/cfg/restapi/static/index.html` — MODIFY. Token layers, nav switcher + JS, and all component restyling.

---

## Task 1: Server-side theme injection

**Files:**
- Modify: `internal/adapters/cfg/restapi/ui.go`
- Test: `internal/adapters/cfg/restapi/ui_test.go` (create)

The current `serveUI` registers `http.FileServer` at `/`. We replace it with a handler that, for the index page only, reads `index.html` from the embed FS, rewrites the `<html lang="en">` opening tag to include `data-theme`, and serves the result. All other paths fall through to the file server.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/cfg/restapi/ui_test.go`:

```go
package restapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/stretchr/testify/require"
)

func newUIServer(t *testing.T) http.Handler {
	t.Helper()
	mux := restapi.NewMux(&memStore{}, func() error { return nil }, nil, nil, nil, nil)
	return mux
}

func getIndex(t *testing.T, h http.Handler, cookie string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "mw-theme", Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

func TestThemeInjection_DefaultMacos(t *testing.T) {
	body := getIndex(t, newUIServer(t), "")
	require.Contains(t, body, `data-theme="macos"`)
}

func TestThemeInjection_AmberCookie(t *testing.T) {
	body := getIndex(t, newUIServer(t), "amber")
	require.Contains(t, body, `data-theme="amber"`)
}

func TestThemeInjection_InvalidCookieFallsBackToMacos(t *testing.T) {
	body := getIndex(t, newUIServer(t), "bogus")
	require.Contains(t, body, `data-theme="macos"`)
	require.NotContains(t, body, `data-theme="bogus"`)
}
```

> Note: confirm the `NewMux` signature/arg count in `server.go:19` and the `OnReload` type when wiring `newUIServer`. If `nil` collaborators panic at construction, pass minimal real values used elsewhere in `server_test.go` instead. Adjust the helper, not the assertions.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestThemeInjection -v`
Expected: FAIL — body contains `<html lang="en">` with no `data-theme`.

- [ ] **Step 3: Implement the injection handler**

In `ui.go`, replace the body of `serveUI` and add helpers:

```go
// serveUI registers the admin UI on mux. The index page gets a data-theme
// attribute injected from the mw-theme cookie so the correct theme paints
// on first load. All other static assets are served verbatim.
func serveUI(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("restapi: embed static subtree: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("restapi: read index.html: " + err.Error())
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			fileServer.ServeHTTP(w, r)
			return
		}
		theme := themeFromRequest(r)
		out := injectTheme(indexHTML, theme)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(out)
	})
}

var allowedThemes = map[string]bool{"macos": true, "amber": true}

func themeFromRequest(r *http.Request) string {
	if c, err := r.Cookie("mw-theme"); err == nil && allowedThemes[c.Value] {
		return c.Value
	}
	return "macos"
}

func injectTheme(html []byte, theme string) []byte {
	return []byte(strings.Replace(
		string(html),
		`<html lang="en">`,
		`<html lang="en" data-theme="`+theme+`">`,
		1,
	))
}
```

Add `"strings"` to the import block in `ui.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestThemeInjection -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the full package to confirm no regressions**

Run: `go test ./internal/adapters/cfg/restapi/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/cfg/restapi/ui.go internal/adapters/cfg/restapi/ui_test.go
git commit -m "feat(admin): inject data-theme from mw-theme cookie"
```

---

## Task 2: Token layers (macos + amber)

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (the `:root` block, ~lines 8-12, and `body` font, ~line 15)

Convert the single `:root` into two `[data-theme]` token sets. The `amber` set reproduces today's exact values; `macos` is the new vibrancy set. Keep all existing token names; add the new ones. Point `body` and the page background at tokens.

- [ ] **Step 1: Replace the `:root` block**

Replace:

```css
  :root {
    --bg: #111016; --surface: #181720; --border: #2a2838;
    --text: #e2e8f0; --accent: #f59e0b; --accent-dim: rgba(245,158,11,0.1);
    --muted: #6b7280; --success: #4ade80; --error: #f87171;
  }
```

with:

```css
  /* ── amber (legacy) — reproduces the original look ──────────────────────── */
  [data-theme="amber"] {
    --bg: #111016; --surface: #181720; --surface-2: #1f1e29;
    --border: #2a2838; --border-hair: #232231;
    --text: #e2e8f0; --text-dim: #6b7280; --muted: #6b7280;
    --accent: #f59e0b; --accent-fg: #111016; --accent-dim: rgba(245,158,11,0.1);
    --success: #4ade80; --error: #f87171;
    --radius: 0.375rem; --radius-lg: 0.75rem;
    --shadow-modal: 0 10px 40px rgba(0,0,0,0.5); --shadow-card: none;
    --blur: 0px;
    --ease-spring: ease-out; --dur-modal: 200ms;
    --font: system-ui, sans-serif;
  }

  /* ── macos (default) — dark vibrancy ────────────────────────────────────── */
  [data-theme="macos"] {
    --bg: #1c1b22; --surface: rgba(36,34,46,0.62); --surface-2: rgba(255,255,255,0.06);
    --border: rgba(255,255,255,0.10); --border-hair: rgba(255,255,255,0.07);
    --text: #f3f4f6; --text-dim: #9aa0aa; --muted: #9aa0aa;
    --accent: #0a84ff; --accent-fg: #ffffff; --accent-dim: rgba(10,132,255,0.25);
    --success: #30d158; --error: #ff453a;
    --radius: 8px; --radius-lg: 14px;
    --shadow-modal: 0 24px 60px rgba(0,0,0,0.55); --shadow-card: 0 1px 3px rgba(0,0,0,0.3);
    --blur: 24px;
    --ease-spring: cubic-bezier(.32,.72,0,1); --dur-modal: 260ms;
    --font: -apple-system, "SF Pro Text", system-ui, sans-serif;
  }

  /* fallback if no data-theme attribute is present */
  :root:not([data-theme]) {
    --bg: #1c1b22; --surface: rgba(36,34,46,0.62); --surface-2: rgba(255,255,255,0.06);
    --border: rgba(255,255,255,0.10); --border-hair: rgba(255,255,255,0.07);
    --text: #f3f4f6; --text-dim: #9aa0aa; --muted: #9aa0aa;
    --accent: #0a84ff; --accent-fg: #ffffff; --accent-dim: rgba(10,132,255,0.25);
    --success: #30d158; --error: #ff453a;
    --radius: 8px; --radius-lg: 14px;
    --shadow-modal: 0 24px 60px rgba(0,0,0,0.55); --shadow-card: 0 1px 3px rgba(0,0,0,0.3);
    --blur: 24px;
    --ease-spring: cubic-bezier(.32,.72,0,1); --dur-modal: 260ms;
    --font: -apple-system, "SF Pro Text", system-ui, sans-serif;
  }
```

- [ ] **Step 2: Point body at the font token**

Replace in the `body` rule:

```css
  body { font-family: system-ui, sans-serif; background: var(--bg); color: var(--text); min-height: 100vh; display: flex; flex-direction: column; }
```

with:

```css
  body { font-family: var(--font); background: var(--bg); color: var(--text); min-height: 100vh; display: flex; flex-direction: column; -webkit-font-smoothing: antialiased; }
```

- [ ] **Step 3: Add a macOS desktop backdrop (so vibrancy reads as glass)**

Add this rule right after the `body` rule:

```css
  [data-theme="macos"] body { background: radial-gradient(140% 120% at 20% 0%, #2b2740 0%, #15131f 60%, #100e18 100%); background-attachment: fixed; }
```

- [ ] **Step 4: Verify both themes render and switching by attribute works**

Build and run the server (e.g. `make run` or `go run ./cmd/...` — check the Makefile target), open the admin page with playwright-mcp.
- Confirm default load shows `data-theme="macos"` (system blue accents, gradient backdrop).
- In devtools/console, run `document.documentElement.dataset.theme='amber'` and confirm the page returns to the original amber/dark look with no broken styles.
Expected: both themes render cleanly; no missing-color artifacts.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): add macos + amber theme token layers"
```

---

## Task 3: Nav restyle + theme switcher

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (nav CSS ~lines 17-22, nav markup ~lines 168-172, init JS near end ~line 1507)

Translucent blurred nav, segmented-control tabs, and a theme switcher on the right that writes the `mw-theme` cookie and flips `data-theme` live.

- [ ] **Step 1: Restyle nav CSS**

Replace the nav block:

```css
  /* Nav */
  nav { background: var(--surface); border-bottom: 1px solid var(--border); padding: 0 1.5rem; display: flex; gap: 0.25rem; align-items: center; flex-shrink: 0; }
  nav h1 { font-size: 1rem; font-weight: 700; color: var(--accent); padding: 1rem 0; margin-right: 1rem; }
  .tab { padding: 1rem 0.75rem; cursor: pointer; border-bottom: 2px solid transparent; color: var(--muted); font-size: 0.875rem; user-select: none; transition: color 120ms, border-color 120ms; }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--accent); border-bottom-color: var(--accent); }
```

with:

```css
  /* Nav */
  nav { background: var(--surface); backdrop-filter: blur(var(--blur)); -webkit-backdrop-filter: blur(var(--blur)); border-bottom: 1px solid var(--border-hair); padding: 0 1.25rem; display: flex; gap: 0.4rem; align-items: center; flex-shrink: 0; position: sticky; top: 0; z-index: 50; }
  nav h1 { font-size: 0.95rem; font-weight: 700; color: var(--text); padding: 0.85rem 0; margin-right: 0.75rem; letter-spacing: -0.01em; }
  .nav-tabs { display: flex; gap: 0.15rem; padding: 0.2rem; background: var(--surface-2); border-radius: var(--radius); }
  .tab { padding: 0.4rem 0.85rem; cursor: pointer; border-radius: calc(var(--radius) - 2px); color: var(--text-dim); font-size: 0.82rem; font-weight: 500; user-select: none; transition: color 120ms, background-color 120ms; }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--accent-fg); background: var(--accent); }
  .nav-spacer { flex: 1; }
  .theme-switch { display: flex; gap: 0.15rem; padding: 0.2rem; background: var(--surface-2); border-radius: var(--radius); }
  .theme-opt { padding: 0.35rem 0.7rem; cursor: pointer; border-radius: calc(var(--radius) - 2px); color: var(--text-dim); font-size: 0.78rem; font-weight: 500; user-select: none; transition: color 120ms, background-color 120ms; }
  .theme-opt:hover { color: var(--text); }
  .theme-opt.active { color: var(--text); background: var(--surface); }
```

- [ ] **Step 2: Update nav markup**

Replace the `<nav>...</nav>` block:

```html
<nav>
  <h1>⚡ Mockwave</h1>
  <div class="tab active" data-tab="dashboard">Dashboard <span id="unmatched-badge" style="display:none;background:var(--error);color:#fff;border-radius:9999px;padding:0.05rem 0.45rem;font-size:0.65rem;margin-left:0.35rem;vertical-align:middle"></span></div>
  <div class="tab" data-tab="rules">Rules</div>
</nav>
```

with:

```html
<nav>
  <h1>⚡ Mockwave</h1>
  <div class="nav-tabs">
    <div class="tab active" data-tab="dashboard">Dashboard <span id="unmatched-badge" style="display:none;background:var(--error);color:#fff;border-radius:9999px;padding:0.05rem 0.45rem;font-size:0.65rem;margin-left:0.35rem;vertical-align:middle"></span></div>
    <div class="tab" data-tab="rules">Rules</div>
  </div>
  <div class="nav-spacer"></div>
  <div class="theme-switch" id="theme-switch">
    <div class="theme-opt" data-theme-opt="macos">macOS</div>
    <div class="theme-opt" data-theme-opt="amber">Amber</div>
  </div>
</nav>
```

> If the tab click handler selects `.tab` via `document.querySelectorAll('.tab')` it still works (tabs are now nested but still `.tab`). Verify the handler does not rely on `nav > .tab` direct-child selectors; if it does, loosen to `.tab`.

- [ ] **Step 3: Add theme-switcher JS**

Add before the `// ── Init ──` comment near the end of the `<script>`:

```javascript
  // ── Theme switcher ─────────────────────────────────────────────────────────
  function setThemeCookie(name) {
    document.cookie = 'mw-theme=' + name + ';path=/;max-age=31536000;SameSite=Lax';
  }
  function applyTheme(name) {
    document.documentElement.dataset.theme = name;
    document.querySelectorAll('.theme-opt').forEach(o => {
      o.classList.toggle('active', o.dataset.themeOpt === name);
    });
  }
  document.querySelectorAll('.theme-opt').forEach(opt => {
    opt.addEventListener('click', () => {
      const name = opt.dataset.themeOpt;
      setThemeCookie(name);
      applyTheme(name);
    });
  });
  // mark the active switch on load (data-theme already set server-side)
  applyTheme(document.documentElement.dataset.theme || 'macos');
```

- [ ] **Step 4: Verify**

Reload via playwright-mcp. Confirm: nav is a translucent blurred bar with segmented tabs; clicking **Amber** switches the whole page live and the choice survives a hard reload (cookie round-trips through the server). Switch back to **macOS**.
Expected: live switch + persistence both work; active tab/switch highlighting correct.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): segmented nav + live theme switcher"
```

---

## Task 4: Modals — centered vibrancy

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (modal CSS ~lines 115-119; modal markup ~line 257-259 inline `style="width:52rem"`)

Replace the bottom-sheet with a centered, frosted, traffic-light modal that scales+fades in.

- [ ] **Step 1: Replace modal CSS**

Replace:

```css
  .modal-overlay { visibility: hidden; pointer-events: none; display: flex; align-items: flex-end; justify-content: center; position: fixed; inset: 0; background: rgba(0,0,0,0); z-index: 100; transition: background-color 200ms, visibility 200ms; }
  .modal-overlay.open { visibility: visible; pointer-events: auto; background: rgba(0,0,0,0.5); }
  .modal { background: var(--surface); border: 1px solid var(--border); border-top-left-radius: 0.75rem; border-top-right-radius: 0.75rem; padding: 1.5rem; width: 52rem; max-width: 100vw; max-height: 92vh; overflow-y: auto; transform: translateY(100%); transition: transform 200ms ease-out; }
  .modal-overlay.open .modal { transform: translateY(0); }
  .modal h2 { margin-bottom: 1.25rem; font-size: 1rem; }
```

with:

```css
  .modal-overlay { visibility: hidden; pointer-events: none; display: flex; align-items: center; justify-content: center; position: fixed; inset: 0; background: rgba(0,0,0,0); backdrop-filter: blur(0px); -webkit-backdrop-filter: blur(0px); z-index: 100; transition: background-color var(--dur-modal), backdrop-filter var(--dur-modal), visibility var(--dur-modal); }
  .modal-overlay.open { visibility: visible; pointer-events: auto; background: rgba(0,0,0,0.45); backdrop-filter: blur(4px); -webkit-backdrop-filter: blur(4px); }
  .modal { background: var(--surface); backdrop-filter: blur(var(--blur)); -webkit-backdrop-filter: blur(var(--blur)); border: 1px solid var(--border); border-radius: var(--radius-lg); box-shadow: var(--shadow-modal); padding: 0; width: 52rem; max-width: calc(100vw - 2rem); max-height: 92vh; overflow: hidden; display: flex; flex-direction: column; transform: scale(0.96); opacity: 0; transition: transform var(--dur-modal) var(--ease-spring), opacity var(--dur-modal) var(--ease-spring); }
  .modal-overlay.open .modal { transform: scale(1); opacity: 1; }
  .modal-titlebar { display: flex; align-items: center; gap: 0.5rem; padding: 0.7rem 0.85rem; border-bottom: 1px solid var(--border-hair); flex-shrink: 0; }
  .modal-lights { display: flex; gap: 0.5rem; }
  .modal-lights > span { width: 12px; height: 12px; border-radius: 50%; }
  .modal-lights > .l-close { background: #ff5f57; }
  .modal-lights > .l-min { background: #febc2e; }
  .modal-lights > .l-max { background: #28c840; }
  .modal-titlebar h2 { font-size: 0.85rem; font-weight: 600; margin: 0 auto; padding-right: 52px; color: var(--text); }
  .modal-body { padding: 1.5rem; overflow-y: auto; }
```

- [ ] **Step 2: Wrap each modal's heading in a titlebar**

Each modal currently has `<div class="modal" ...><h2 id="...">Title</h2> ...content... </div>`. For the rule modal (and any other `.modal`), restructure to:

```html
<div class="modal">
  <div class="modal-titlebar">
    <div class="modal-lights"><span class="l-close"></span><span class="l-min"></span><span class="l-max"></span></div>
    <h2 id="rule-modal-title">Add Rule</h2>
  </div>
  <div class="modal-body">
    <!-- existing modal inner content (everything that was after the old <h2>) -->
  </div>
</div>
```

Apply the same wrapping to every `.modal` in the file (search for `class="modal"`). Remove the now-redundant inline `style="width:52rem"` (width is in CSS). Keep all existing inner content and element IDs unchanged — only move them inside `.modal-body` and move the `<h2>` into `.modal-titlebar`.

- [ ] **Step 3: Verify**

Open each modal (Add Rule, Edit Rule, Copy Rule) via the UI with playwright-mcp under the `macos` theme: confirm centered frosted window, traffic lights, title centered, scale+fade in, backdrop blur. Press Esc and click backdrop — both still close (handlers unchanged). Switch to `amber`: modal is opaque, centered, no blur, original feel.
Expected: both themes correct; all three modals open/close normally.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): centered vibrancy modals with traffic-light titlebar"
```

---

## Task 5: Buttons

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (button CSS ~lines 51-58)

macOS pill buttons using accent tokens and an active-press scale.

- [ ] **Step 1: Replace button CSS**

Replace:

```css
  button { cursor: pointer; border: none; border-radius: 0.375rem; padding: 0.4rem 0.75rem; font-size: 0.8rem; font-family: inherit; transition: opacity 120ms, background-color 120ms; }
  button:hover:not(:disabled) { opacity: 0.85; }
  button:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #111016; font-weight: 600; }
  .btn-danger { background: var(--error); color: #fff; font-weight: 600; }
  .btn-ghost { background: transparent; border: 1px solid var(--border); color: var(--text); }
  .btn-success-state { background: rgba(74,222,128,0.15); border: 1px solid rgba(74,222,128,0.3); color: var(--success); }
  .btn-sm { padding: 0.2rem 0.5rem; font-size: 0.75rem; }
```

with:

```css
  button { cursor: pointer; border: 1px solid transparent; border-radius: var(--radius); padding: 0.45rem 0.9rem; font-size: 0.8rem; font-weight: 500; font-family: inherit; transition: filter 120ms, background-color 120ms, transform 80ms; }
  button:hover:not(:disabled) { filter: brightness(1.08); }
  button:active:not(:disabled) { transform: scale(0.97); }
  button:disabled { opacity: 0.45; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: var(--accent-fg); font-weight: 600; box-shadow: inset 0 1px 0 rgba(255,255,255,0.18); }
  .btn-danger { background: var(--error); color: #fff; font-weight: 600; }
  .btn-ghost { background: var(--surface-2); border: 1px solid var(--border-hair); color: var(--text); }
  .btn-success-state { background: rgba(48,209,88,0.15); border: 1px solid rgba(48,209,88,0.3); color: var(--success); }
  .btn-sm { padding: 0.25rem 0.6rem; font-size: 0.75rem; }
```

- [ ] **Step 2: Verify**

Via playwright-mcp under both themes: primary button reads as filled accent with subtle top highlight; ghost button has hairline; press animates a slight shrink; disabled state dims. Amber theme primary still has dark text on amber.
Expected: both themes correct.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): macOS pill buttons"
```

---

## Task 6: Tables + stats rail

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (table CSS ~lines 45-49; stats-rail CSS ~lines 31-43)

Hairline tables with hover tint and sticky header; vibrancy dist-cards.

- [ ] **Step 1: Replace table CSS**

Replace:

```css
  /* Tables */
  table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  th, td { padding: 0.6rem 0.75rem; text-align: left; border-bottom: 1px solid var(--border); }
  th { color: var(--muted); font-weight: 500; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
  tr:last-child td { border-bottom: none; }
```

with:

```css
  /* Tables */
  table { width: 100%; border-collapse: collapse; font-size: 0.86rem; font-variant-numeric: tabular-nums; }
  th, td { padding: 0.6rem 0.8rem; text-align: left; border-bottom: 1px solid var(--border-hair); }
  th { color: var(--text-dim); font-weight: 600; font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.05em; position: sticky; top: 0; background: var(--surface); backdrop-filter: blur(var(--blur)); -webkit-backdrop-filter: blur(var(--blur)); }
  tbody tr { transition: background-color 100ms; }
  tbody tr:hover { background: var(--surface-2); }
  tr:last-child td { border-bottom: none; }
```

- [ ] **Step 2: Update stats-rail dist-card CSS**

Replace the `.dist-card` rule:

```css
  .dist-card { background: var(--surface); border: 1px solid var(--border); border-radius: 0.5rem; padding: 0.6rem 0.7rem; }
```

with:

```css
  .dist-card { background: var(--surface-2); border: 1px solid var(--border-hair); border-radius: var(--radius); padding: 0.6rem 0.7rem; box-shadow: var(--shadow-card); }
```

And update the rail border on the `.stats-rail` rule: change `border-left: 1px solid var(--border);` to `border-left: 1px solid var(--border-hair);` (edit only that property within the existing `.stats-rail` rule).

- [ ] **Step 3: Verify**

Via playwright-mcp under both themes: table headers stick on scroll, rows tint on hover, numbers align; rail cards read as soft panels.
Expected: both themes correct.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): hairline tables + vibrancy stats rail"
```

---

## Task 7: Inputs, selects, and badges

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (input/select/textarea CSS — search for `input`, `select`, `textarea` rules in the `<style>` block; count-badge/pill rules — search for `badge` and `count` classes)

Inset fields with an accent focus ring; capsule badges.

- [ ] **Step 1: Locate the current form-control and badge rules**

Run a search to find exact selectors and line numbers:

```bash
grep -nE "input|select|textarea|\.badge|count-badge|pill" internal/adapters/cfg/restapi/static/index.html | grep -iE "\{|background|border" | head -40
```

- [ ] **Step 2: Normalize form controls to tokens**

For each `input`, `select`, `textarea` style rule found, ensure it uses:

```css
  input, select, textarea { background: var(--surface-2); border: 1px solid var(--border); color: var(--text); border-radius: var(--radius); padding: 0.45rem 0.6rem; font-family: inherit; font-size: 0.82rem; transition: border-color 120ms, box-shadow 120ms; }
  input:focus, select:focus, textarea:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-dim); }
```

If existing rules already target these elements, edit those rules to match the declarations above (replace hardcoded colors/radii with the tokens, add the `:focus` ring). Do not duplicate selectors — modify in place. Preserve any element-specific sizing (e.g. textarea `min-height`, `font-family: monospace` on code fields) by keeping those extra declarations.

- [ ] **Step 3: Capsule badges**

For the count-badge / pill rules found in Step 1, set `border-radius: 9999px;` and ensure colors use tokens (`background: var(--surface-2); color: var(--text-dim);` for neutral counts, keeping any semantic error/success variants on `var(--error)`/`var(--success)`). Edit in place; don't introduce new class names.

- [ ] **Step 4: Verify**

Via playwright-mcp under both themes: focus an input — accent ring appears; selects/textareas match; count badges are pill-shaped.
Expected: both themes correct.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): inset inputs with focus ring + capsule badges"
```

---

## Task 8: Motion + accessibility pass

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (the existing `prefers-reduced-motion` rule ~line 13)

Ensure new animations honor reduced-motion and focus is always visible.

- [ ] **Step 1: Extend the reduced-motion rule**

The existing rule already zeroes `transition-duration` and `animation-duration` globally with `!important`, which covers the new spring transitions. Add transform suppression so the modal scale and button press don't jump for reduced-motion users. Replace:

```css
  @media (prefers-reduced-motion: reduce) { *, *::before, *::after { transition-duration: 0ms !important; animation-duration: 0ms !important; } }
```

with:

```css
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { transition-duration: 0ms !important; animation-duration: 0ms !important; }
    .modal { transform: none !important; }
  }
```

- [ ] **Step 2: Add a global focus-visible outline**

Add near the top of the `<style>` block (after the `* { box-sizing... }` reset):

```css
  :focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  button:focus-visible, .tab:focus-visible, .theme-opt:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
```

- [ ] **Step 3: Verify reduced-motion and focus**

Via playwright-mcp: emulate `prefers-reduced-motion: reduce` and confirm modals appear without scale animation. Tab through controls and confirm a visible focus ring on buttons, tabs, theme options, and inputs, in both themes.
Expected: no motion when reduced; focus always visible.

- [ ] **Step 4: Full visual regression sweep**

With playwright-mcp, screenshot Dashboard and Rules tabs and each modal in **both** themes. Eyeball for: contrast (text legible on glass), no clipped/overflowing panels, accent consistency.
Expected: clean in both themes.

- [ ] **Step 5: Run the Go test suite**

Run: `go test ./...`
Expected: PASS (no functional regressions; theme injection tests green).

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): reduced-motion + focus-visible a11y pass"
```

---

## Self-Review

- **Spec coverage:** token architecture (Task 2), cookie + server injection (Task 1), nav switcher (Task 3), centered vibrancy modals (Task 4), buttons (5), tables + rail (6), inputs + badges (7), motion + a11y (8), Go injection test (1), visual verification (each task), both themes preserved (Task 2 amber set). All spec sections mapped.
- **Placeholder scan:** Tasks 6/7 use a `grep` discovery step because exact line numbers for those rules weren't captured; the engineer is told the exact selectors and declarations to end up with, so no behavioral placeholder remains.
- **Type/name consistency:** cookie name `mw-theme`, themes `macos`/`amber`, attribute `data-theme`, JS `applyTheme`/`setThemeCookie`, classes `.nav-tabs`/`.theme-switch`/`.theme-opt`/`.modal-titlebar`/`.modal-body`/`.modal-lights` used consistently across tasks. Go `themeFromRequest`/`injectTheme`/`allowedThemes` consistent with the test.
