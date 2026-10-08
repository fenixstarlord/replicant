# Shelf web UI — Dirtywave M8 look

The web UI emulates the lo-fi look of the Dirtywave M8 tracker: black background, a small bitmap
font, a handful of saturated accent colours, uppercase labels, flat 1-pixel rules, nothing rounded,
nothing animated. This document is the implementable spec. Read it before writing any template or
CSS. The component library is still **DaisyUI 5 on Tailwind CSS 4**; the M8 look is one custom
DaisyUI theme named `m8`, one vendored font, and the rules below.

Decisions (2026-10-08): faithful emulation, dark only, stealth57 font vendored.

## 1. Palette

Values come from the M8 Operation Manual, Theme View page (stock theme). Mapping to DaisyUI tokens:

| M8 field | Hex | Token | Used for |
|---|---|---|---|
| Background | `#000000` | `--color-base-100` | page and panel background |
| Text: Empty | `#1E1E28` | `--color-base-200` | empty rows, dividers, 1px borders, placeholders |
| (derived) | `#2C2C3E` | `--color-base-300` | subtle hover surface, scrollbar track, disabled |
| Text: Default | `#8C8CBA` | `--color-base-content` | body text, table cells |
| Text: Info | `#60608E` | `--color-neutral`, `--color-info` | de-emphasised text, hints, units, field sources |
| Text: Value | `#FAFAFA` | `--color-m8-value` | emphasised values: clip names, numbers, drive names |
| Text: Titles | `#32ECFF` | `--color-primary` | headings, links, focus ring, cursor/hover row |
| Play Markers | `#00FF70` | `--color-accent` | "live" state: latest scan, drive currently online, active scan |
| Cursor | `#32ECFF` | = primary | hover row: primary background, black text |
| Selection | `#FF00D2` | `--color-secondary` | selected/checked rows, active filter chips |
| Scope/Slider | `#32ECFF` | = primary | progress bars, range inputs, capacity bars |
| Meter Low | `#00FF50` | `--color-success` | OK states, "no changes" |
| Meter Mid | `#FFE000` | `--color-warning` | warnings, missing extractor, partial metadata |
| Meter High | `#FF3070` | `--color-error` | errors, removed files, failed ingest |

Content colours: black `#000000` on every bright token (primary, secondary, accent, success,
warning, error); `#FAFAFA` on neutral and info.

Contrast notes: `#8C8CBA` on black is about 6.6:1, fine for body text. `#60608E` on black is about
3.2:1, so use it only for secondary information, never for the only copy of a value.

## 2. Theme CSS

Source file: `internal/web/static/src/app.css`. Built once with the standalone Tailwind CLI into
`internal/web/static/app.css`, which is committed and embedded. This block includes every variable
the daisyUI custom-theme reference lists; do not drop any.

```css
@import "tailwindcss";
@plugin "daisyui" {
  themes: m8 --default;
  logs: false;
}

@plugin "daisyui/theme" {
  name: "m8";
  default: true;
  prefersdark: true;
  color-scheme: dark;

  --color-base-100: #000000;
  --color-base-200: #1E1E28;
  --color-base-300: #2C2C3E;
  --color-base-content: #8C8CBA;
  --color-primary: #32ECFF;
  --color-primary-content: #000000;
  --color-secondary: #FF00D2;
  --color-secondary-content: #000000;
  --color-accent: #00FF70;
  --color-accent-content: #000000;
  --color-neutral: #60608E;
  --color-neutral-content: #FAFAFA;
  --color-info: #60608E;
  --color-info-content: #FAFAFA;
  --color-success: #00FF50;
  --color-success-content: #000000;
  --color-warning: #FFE000;
  --color-warning-content: #000000;
  --color-error: #FF3070;
  --color-error-content: #000000;

  --radius-selector: 0rem;
  --radius-field: 0rem;
  --radius-box: 0rem;
  --size-selector: 0.25rem;
  --size-field: 0.25rem;
  --border: 1px;
  --depth: 0;
  --noise: 0;
}

/* Semantic M8 colours as Tailwind utilities: text-m8-value, bg-m8-selection, border-m8-empty ... */
@theme {
  --color-m8-bg: #000000;
  --color-m8-empty: #1E1E28;
  --color-m8-info: #60608E;
  --color-m8-default: #8C8CBA;
  --color-m8-value: #FAFAFA;
  --color-m8-title: #32ECFF;
  --color-m8-play: #00FF70;
  --color-m8-cursor: #32ECFF;
  --color-m8-selection: #FF00D2;
  --color-m8-scope: #32ECFF;
  --color-m8-meter-low: #00FF50;
  --color-m8-meter-mid: #FFE000;
  --color-m8-meter-high: #FF3070;

  --font-sans: "stealth57", ui-monospace, Menlo, monospace;
  --font-mono: "stealth57", ui-monospace, Menlo, monospace;
}

@font-face {
  font-family: "stealth57";
  src: url("fonts/m8stealth57.woff2") format("woff2");
  font-weight: normal;
  font-style: normal;
  font-display: block;
}

@layer base {
  html {
    background: var(--color-base-100);
    font-size: 16px;                /* 2x the 8px glyph grid; adjust after measuring the font (see §3) */
    text-rendering: optimizeSpeed;
    -webkit-font-smoothing: none;
    font-variant-numeric: tabular-nums;
  }
  h1, h2, h3, h4, th, label, .btn, .tab, .badge, .menu, .navbar, .stat-title {
    text-transform: uppercase;
    letter-spacing: 0;
  }
  h1 { font-size: 24px; }           /* 3x */
  h2, h3 { font-size: 16px; color: var(--color-primary); }
  * { transition: none !important; animation: none !important; box-shadow: none !important; }
  :focus-visible { outline: 1px solid var(--color-primary); outline-offset: 0; }
  ::selection { background: var(--color-secondary); color: #000; }
  ::placeholder { color: var(--color-m8-empty); }
  * { scrollbar-width: thin; scrollbar-color: var(--color-neutral) var(--color-base-300); }
}

@layer components {
  /* Row states: hover = cursor (cyan), selected = selection (magenta) */
  .table tbody tr:hover            { background: var(--color-primary);   color: #000; }
  .table tbody tr[aria-selected="true"],
  .table tbody tr.is-selected      { background: var(--color-secondary); color: #000; }
  .table :where(th, td)            { padding-top: 2px; padding-bottom: 2px; border-color: var(--color-base-200); }
  .table td.num                    { text-align: right; }
  .empty                           { color: var(--color-m8-empty); }   /* render "--" in empty cells */
  /* The one permitted animation: a 1 Hz blinking cursor on the keyboard-focused row */
  .table tbody tr:focus-within::before { content: ">"; color: var(--color-primary); animation: m8blink 1s steps(1) infinite !important; }
  @keyframes m8blink { 50% { opacity: 0; } }
}
```

## 3. Font

- **stealth57** by Trash80 (Timothy Lamb, creator of the M8). Source:
  <https://fontstruct.com/fontstructions/show/413734/stealth57>, Creative Commons Attribution
  Share-Alike 3.0. A `m8stealth57.woff2` build ships in Dirtywave's MIT-licensed M8WebDisplay repo
  (<https://github.com/Dirtywave/M8WebDisplay>).
- **Vendoring (Phase 5 to-do, needs a download, ask first):** place the woff2 at
  `internal/web/static/fonts/m8stealth57.woff2` and add `internal/web/static/fonts/LICENSE-stealth57.txt`
  containing the attribution below and the CC BY-SA 3.0 notice.
- **Sizing:** bitmap fonts only look right at integer multiples of their native pixel size. After
  vendoring, measure the font's native em (the FontStruct grid; expected 8px) and set `html`
  font-size to exactly 2x, headings to 3x. Never use fractional rem sizes or `text-sm`/`text-xs`
  utilities that would land off-grid; define `.text-1x`, `.text-2x`, `.text-3x` helpers instead.
- **Fallback:** `ui-monospace, Menlo, monospace` so the layout survives if the font fails to load.
- **Attribution text** (README "Credits" and the About page):

  > Font: stealth57 by Trash80 (Timothy Lamb), <https://fontstruct.com/fontstructions/show/413734/stealth57>,
  > licensed CC BY-SA 3.0. The Dirtywave M8 is a product of Dirtywave; this project is not affiliated
  > with Dirtywave.

## 4. Rules

1. **Dark only.** One theme, `m8`. No theme controller, no light variant, no `prefers-color-scheme`
   branches.
2. **Uppercase** for headings, table headers, labels, buttons, tabs, badges, nav. Body text, file
   names and paths keep their original case (case is data).
3. **Flat.** No rounded corners, no shadows, no gradients, no `--depth`, no `--noise`. Borders are
   1px in `base-200`.
4. **No motion** except the blinking focus cursor. htmx swaps use no transition classes.
5. **Colour has meaning.** Cyan = title/cursor/focus. Magenta = selection. Green = live/OK.
   Yellow = warning. Pink-red = error/removed. Do not use colours decoratively.
6. **Value vs default vs info** text: the thing the user is looking for (clip name, drive name,
   number) is `text-m8-value`; surrounding text is default; units, sources and hints are info.
7. **Empty is visible.** An empty cell renders `--` in `m8-empty`, like an empty step on the M8.
8. **Density.** Tables are the primary surface. 2px vertical cell padding, tabular numerics,
   right-aligned numbers, fixed-width columns for timecode, fps, resolution.
9. **Mobile.** The 2x font is legible on phones. Filters move into a `drawer`; tables scroll
   horizontally inside `overflow-x-auto`.
10. **Non-goals.** No CRT/scanline overlay, no pixel-art icons beyond simple glyphs, no sound.

## 5. Component mapping (plan.md pages → DaisyUI)

Use these components and nothing hand-rolled:

| Page | Components |
|---|---|
| Shell | `navbar` (uppercase section names), `menu` for nav on mobile inside a `drawer` |
| Login | `fieldset`, `input`, `btn btn-primary` |
| Drives | `table`, `badge` (fs type, online/offline in accent), `progress progress-primary` for used capacity, `stat` tiles for totals, inline `input input-sm` for label/location editing |
| Search | `input` with htmx live search, `select`/`range`/`input` filters in a `drawer` on mobile and a side column on desktop, `badge badge-secondary` for active filter chips, `table` with sortable `th`, `toggle` for "include old scans" |
| Browse | `breadcrumbs`, `table` for the folder listing, `collapse` for expanding a clip's member files |
| Clip/file detail | `tabs` (Clip / Format / Camera / Exposure / Lens / Audio), `table` key-value rows, `tooltip` for field source, `collapse` for raw extractor JSON inside `mockup-code`, `list` for other copies and history |
| Scan history | `table`, `modal` to pick two scans, `badge` for added/removed/changed counts in success/error/warning |
| Duplicates | `table` grouped rows, `stat` for wasted bytes |
| Settings | `fieldset`, `file-input` for bundle upload, `btn btn-error` for revoke, `toast` for results |
| Export | `btn` group via `join` |
| Global | `kbd` for shortcuts, `loading loading-bars` (the one allowed indicator) for htmx `hx-indicator`, `alert` for errors |
