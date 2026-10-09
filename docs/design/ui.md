# Shelf web UI — design notes

Decided 2026-10-09, replacing the Dirtywave M8 emulation in `m8-theme.md` (kept for history).

**Goal:** clean, modern, readable. Nothing decorative; tables are the product.

- **Library:** daisyUI 5 on Tailwind CSS 4, built with the standalone Tailwind CLI (`./build-css.sh`),
  output committed and embedded. No Node at runtime.
- **Themes:** daisyUI `silk` (light, default) and `dim` (dark, when the OS prefers dark). No custom
  palette. Semantic colours only: success for added/latest, error for removed/revoke, warning for
  changed/old/partial.
- **Type:** system font stack (`ui-sans-serif, system-ui, …`), 15px base, tabular numerals. Paths,
  timecode, hashes and commands use the system monospace (`.mono`).
- **Copy:** sentence case. No shouting. Empty values render an em dash in muted colour.
- **Layout:** page body on `base-200`, content in `base-100` cards and bordered `rounded-box`
  tables. One `h1` per page, `h2` for sections, `.kv` definition lists for metadata groups with the
  source shown as a ghost badge (hidden on phones).
- **Components:** `navbar` with ghost buttons, `table table-sm` with `hover` rows, `stats`,
  `card`, `badge-ghost` for kinds, `collapse-arrow` for filters and raw output, `join` for
  segmented controls and pagers, `form-control` labels, `alert` for one-time messages.
- **Mobile:** navbar wraps, stats stack, detail cards stack, tables scroll horizontally.
- **API keys:** their own page (`/settings/api-keys`) with a name field, the key shown once with a
  copy button and the ready-to-paste `shelf login` line, and a revoke per row.
