# ADR-002: Replace the M8 emulation with a clean daisyUI theme

## Status
Accepted. Supersedes the "look" part of ADR-001; the stack (html/template + htmx + daisyUI on
Tailwind, standalone CLI build) is unchanged.

## Date
2026-10-09

## Context
After using the M8-styled UI on real data the user found it hard to read: pixel font, all caps,
black background and dense rows work for a 320×240 tracker screen, not for tables of file paths
and metadata. The user asked for something clean, modern and classy, and for API key creation to
be obvious in the web UI.

## Decision
Keep daisyUI and switch to its built-in `silk` theme (light) with `dim` for dark mode, system
fonts, sentence-case copy, cards and bordered tables. Drop the stealth57 font and the M8 palette.
Give API keys a dedicated page. Details in `docs/design/ui.md`.

## Alternatives Considered
- **Keep M8, improve legibility** (larger font, fewer caps): still a novelty look; rejected.
- **Switch libraries** (templUI, Shoelace): no gain, since the readability problem was the theme,
  not the components; rejected.
- **Custom palette**: more to maintain than daisyUI's tuned themes; rejected.

## Consequences
- `m8-theme.md` and the M8 CSS are gone from the build; the design doc stays as history.
- No font download; the UI looks native on each OS.
- Dark mode follows the OS; there is no in-app toggle (could be added with daisyUI's
  theme-controller if wanted).
