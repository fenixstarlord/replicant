# ADR-001: Web UI stack — html/template + htmx + DaisyUI with a custom M8 theme

## Status
Accepted

## Date
2026-10-08

## Context
Shelf's server ships as a single Go binary with the web UI embedded. The plan rules out a JS build
step at runtime and multi-user features. The UI is mostly dense tables (drives, search results,
folder trees, metadata) and is often read from a phone. The user asked for a component library so
pages are consistent and quick to build, and for the look to emulate the Dirtywave M8 tracker: black
background, bitmap font, saturated accents, flat and uppercase.

Key requirements:
- Works with Go `html/template` and htmx fragment responses.
- No Node at runtime; ideally no npm in the repo at all.
- Themeable enough to reproduce the M8 palette, zero radius, and no shadows.
- Mobile-friendly tables and filters.

## Decision
- **Rendering:** Go `html/template` + htmx, assets embedded with `embed`.
- **Component library:** DaisyUI 5 on Tailwind CSS 4.
- **Build:** the standalone Tailwind CLI binary compiles `internal/web/static/src/app.css` into a
  committed, embedded `app.css`. One-time build step, wrapped by `make css`; no npm.
- **Theme:** a single custom DaisyUI theme `m8` using the M8 stock palette, `--radius-* 0`,
  `--depth 0`, `--noise 0`, plus `m8-*` semantic Tailwind colours. Dark only.
- **Font:** Trash80's stealth57 (CC BY-SA 3.0), vendored as woff2 with attribution.
- The full spec lives in `docs/design/m8-theme.md`.

## Alternatives Considered

### templUI (templ + Tailwind)
- Pros: richest Go-native component set, shadcn-style, typed templates.
- Cons: replaces `html/template` with templ's code generator and adds a generation step to every
  template change, plus a Tailwind build.
- Rejected: too much tooling for a dense-tables app whose components are mostly tables and forms.

### Shoelace / Web Awesome web components
- Pros: real interactive components, no build step, framework-agnostic.
- Cons: ships a JS bundle, needs hooks so htmx swaps re-initialise components, and its visual
  language is rounded and shadowed; fighting that to get an M8 look is more work than theming CSS.
- Rejected.

### Pico CSS
- Pros: tiny, classless, zero build.
- Cons: a stylesheet more than a component library; tables, modals, drawers and badges are thin,
  and the semantic-element approach limits control over the M8 look.
- Rejected.

### Hand-rolled CSS
- Pros: total control, smallest output.
- Cons: every component is bespoke; slower to build the nine plan.md pages consistently.
- Rejected, but the M8 rules (§4 of the design doc) are enforced on top of DaisyUI so the result
  stays close to this in spirit.

## Consequences
- A one-time CSS build step exists. The compiled CSS is committed so `go build` alone still
  produces a working binary; CI should fail if the committed CSS is stale.
- Templates must use DaisyUI classes and the `m8-*` colours; raw Tailwind utilities are for layout
  only. Agents read `docs/design/m8-theme.md` before touching templates.
- Bitmap-font legibility constrains type sizes to integer multiples of the glyph grid. Body text is
  larger than a typical web UI, which costs some table density.
- Dark only. Users who want a light UI are out of luck by design.
- stealth57 is CC BY-SA; attribution lives in the README and an About page. The font file is the
  only third-party asset in the binary besides htmx.
- Tailwind standalone CLI and DaisyUI versions are pinned in the `make css` target so the CSS build
  is reproducible.
