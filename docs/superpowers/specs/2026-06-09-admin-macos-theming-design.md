# Admin UI — macOS Vibrancy Restyle + Theme System

**Date:** 2026-06-09
**Status:** Approved (design)
**Scope:** Whole admin page (`internal/adapters/cfg/restapi/static/index.html`) + server theme injection (`ui.go`).

## Goal

Restyle the entire Mockwave admin UI from its current "developer default" dark look into a polished macOS-vibrancy aesthetic, and introduce a themeable token system so additional themes (retro, dark-monokai, etc.) can be added later as pure token blocks. Ship two themes now: `macos` (default) and `amber` (the current look, preserved).

## Decisions (from brainstorming)

- **Surface scope:** Whole admin page — every component, unified design system. (not modals-only)
- **Material:** Vibrancy — frosted `backdrop-filter` blur, translucent glass panels, centered modal window, traffic-light header, deep soft shadow, spring motion.
- **Accent:** System blue (`#0a84ff`) for the macOS theme. Amber retires into the legacy `amber` theme.
- **Theme system depth:** Token architecture + 2 themes (`macos` default, `amber` legacy) + a theme switcher in the nav.
- **Theme persistence:** Cookie (`mw-theme`), read server-side so the correct `data-theme` is present in first paint (no flash-of-wrong-theme). No localStorage.
- **No build step / no new deps.** Vanilla HTML/CSS/JS, embedded in the Go binary as today.

## Architecture

### Token layers

All styling is driven by CSS custom properties. Restructure the `<style>` block so each theme is a token set selected by an attribute on `<html>`:

```css
[data-theme="macos"] { /* default token set */ }
[data-theme="amber"] { /* current look preserved */ }
```

Components reference **only** tokens, never raw colors or material values. No per-component theme branches exist — themes differ solely by token values.

Token groups:

| Group | Example tokens |
|-------|----------------|
| Color | `--bg --surface --surface-2 --text --text-dim --accent --accent-fg --success --error --border` |
| Material | `--radius --radius-lg --shadow-modal --shadow-card --blur --border-hair` |
| Motion | `--ease-spring --dur-modal` |

- **macos:** `--accent:#0a84ff`, `--blur:24px`, translucent surfaces (`rgba(...)`), deep `--shadow-modal` (e.g. `0 24px 60px rgba(0,0,0,.55)`), `--ease-spring: cubic-bezier(.32,.72,0,1)`.
- **amber:** current values, `--blur:0`, opaque surfaces, amber `--accent`, squarer radii. This is the graceful fallback for browsers without `backdrop-filter`.

### Theme data flow

```
GET /  → ui.go reads cookie "mw-theme"
       → whitelist {macos, amber}; default macos when absent/invalid
       → inject data-theme="<value>" into the <html> tag of index.html
       → serve
User clicks nav switcher
       → JS sets cookie mw-theme=<value> (1yr, path=/, SameSite=Lax)
       → JS sets document.documentElement.dataset.theme = <value>
       → CSS vars swap instantly, no reload
```

## Component restyle (whole page)

All components are re-skinned via tokens. Both themes inherit every change; they differ only by token values.

| Component | Change |
|-----------|--------|
| Modals | Centered (replace bottom-sheet `translateY` with centered scale+fade), traffic-light header bar, frosted blur, deep shadow, spring-in. Esc + backdrop-click close preserved. |
| Nav | Translucent blurred bar; tabs become a segmented-control (pill active state, accent); theme switcher control at the right. |
| Buttons | macOS pill shape; `.btn-primary` accent-filled; `.btn-ghost` hairline; subtle inner highlight; `:active` scale 0.97. |
| Tables | Hairline rows, hover tint, tighter rhythm, tabular-nums, sticky header. |
| Stats rail | Vibrancy cards, softer radii, refined distribution bars. |
| Inputs / selects | Inset fields; focus = accent ring (`box-shadow: 0 0 0 3px <accent-dim>`); rounded. |
| Badges / count pills | macOS capsule style. |
| Font | `-apple-system, "SF Pro", system-ui` stack; tighter letter-spacing on headings. |

## Accessibility

- Existing `prefers-reduced-motion` block is extended to cover all new animations.
- Focus rings remain visible (accent ring on inputs, visible outline on interactive elements).
- Color contrast verified against WCAG AA for both themes.

## Files touched

- `internal/adapters/cfg/restapi/static/index.html` — rewrite `<style>` into token-layer + token-referencing component rules; add theme-switcher markup in nav; add switcher JS (write cookie, set `data-theme`, no reload).
- `internal/adapters/cfg/restapi/ui.go` — currently serves `static/` via a plain `http.FileServer`. Add a dedicated handler for `GET /` (and `/index.html`) that reads the embedded `index.html`, injects `data-theme` derived from the `mw-theme` cookie into the `<html>` tag, and serves it. All other static paths continue through the existing `http.FileServer`. API routes still take precedence.

## Build order

1. Token layers (both themes) + `ui.go` cookie injection + nav switcher → verify switching works and there is no FOUC.
2. Restyle components one group at a time (modals → nav → buttons → tables → rail → inputs → badges), verifying both themes at each step.
3. Motion + accessibility pass.

## Testing

- **Go:** add a `ui.go` test — cookie present / absent / invalid → correct `data-theme` injected; existing `server_test.go` and `eval_test.go` stay green.
- **Visual:** manual verification via playwright-mcp screenshots, both themes, each component group.
- **No new dependencies, no build step.** The embed model is unchanged.

## Risks

- `backdrop-filter` support — fine on modern Safari/Chrome/Firefox. The `amber` theme (`--blur:0`, opaque) is the graceful degradation path.
- `index.html` is a single ~1500-line file; the CSS rewrite is the largest change. Mitigated by the staged build order and per-step visual checks.

## Out of scope

- Additional themes beyond `macos` and `amber` (retro, dark-monokai) — the architecture supports them as future token blocks.
- Any change to admin functionality, API, or data model.
- Light-mode macOS variant (the macos theme is dark vibrancy).
