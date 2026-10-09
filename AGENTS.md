# AGENTS.md — Shelf (drive catalog)

Instructions for AI coding agents working in this repo. `CLAUDE.md` imports this file.
The authoritative design is **[plan.md](plan.md)**. Read it before any non-trivial task. This file
adds working conventions, environment facts, and the current phase; it does not restate the plan.

## What this is

A self-hosted catalog of offline drives full of camera media. Two Go binaries from one module:

- `shelf` — macOS CLI. Walks a mounted drive, groups files into clips, runs metadata extractors
  (ffprobe, ARRI `art-cmd`, REDline, sidecar parsers), and writes a `.shelf` scan bundle or pushes
  it to the server.
- `shelf-server` — Go server on a home server. SQLite + FTS5, REST ingest, embedded htmx web UI,
  and its own scheduled scans of folders mounted on the server (Settings → Server scans).

Working name is **Shelf**; it may be renamed. Grep for the name before hardcoding it anywhere new.

## Current phase

Track progress here. Update this section at the end of each phase, and commit.

- [x] Phase 1 — Skeleton (2026-10-08). Module, `internal/scan` (walk, skip rules, packages,
  xxHash64 fingerprint), `shelf dump`, `internal/store` with the FTS5 trigram check (passes on
  `modernc.org/sqlite`, no cgo needed), `shelf-server` stub with `/healthz`, `./start.sh`, Makefile.
- [x] Phase 2 — Clip grouping + bundle (2026-10-08). `internal/clips` (R3D `.RDC`, ARRIRAW
  frame sequences, Sony `XDROOT/Clip` + `M4ROOT/CLIP`, BRAW `.sidecar`, same-stem sidecars),
  `internal/scan.VolumeInfo` (statfs + `diskutil info -plist`), `internal/bundle` zip writer and
  reader, `shelf scan -o x.shelf --fast`, `shelf dump --clips`. Verified on SSD_RAID: 614k
  entries → 19.9k clips in 10 s, 9 MB bundle. Real camera cards still unverified (none on that
  drive); re-check grouping when one is attached.
- [x] Phase 3 — Server core (2026-10-08). `internal/store`: embedded SQL migrations, one-
  transaction ingest with per-directory totals, path diff against the previous latest scan
  (`scan_changes`), API tokens (sha256 of `shelf_…`), settings. `internal/web`: bearer-token or
  HMAC session-cookie auth, `POST /api/scans`, `GET /api/drives`, `GET /api/me`, placeholder
  login page. CLI: `shelf login`, `upload`, `drives`, and `scan` without `-o` pushes directly.
  `shelf-server token create|list|revoke`, `shelf-server ingest <file>`. Verified end to end
  against `./start.sh`: the 614k-entry RAID bundle ingests in 16 s. Docker still deferred.
- [~] Phase 4 — Extractors (2026-10-08/09). `internal/meta` (normalized fields, priority merge with
  weak fields), `internal/extract` (runner, worker pool, per-clip timeouts, missing tools never
  fatal), extractors: `ffprobe`, `ale`, `bwf` (bext + iXML), `sony-xml`, `braw-sidecar`, `redline`,
  `art-cmd`. `shelf doctor [--json]`; `shelf scan` extracts unless `--fast`; ingest stores fields,
  sources, errors, `clip_raw`.
  **ARRI verified 2026-10-09** with ART CMD 1.0.0: `art-cmd export --input <clip> --duration 1
  --output <tmp>/metadata.json` (0.14 s per clip); mapper in `internal/extract/arri/export.go`
  against `testdata/golden/arri/alexa35_arricore_metadata.json`. On the ALEXA 35 card: 74 clips,
  0 errors, art-cmd wins camera/exposure/lens/colour, ALE supplies duration and frame count.
  **Open:** `redline` mapper still unverified against real `--printMeta` output; Sony XML and
  BRAW sidecar parsers tested on synthetic samples only.
- [x] Phase 5 — Search + browse UI (2026-10-08). `internal/store/query.go` (clip search with every
  plan filter, file search, browse listing, detail, history, copies, facets), `internal/web` pages:
  login, drives (inline label/location), drive (scan history, diff picker, exports), search
  (clips/files, filters, htmx live results), browse, clip detail (grouped fields with source tags,
  raw output), file detail, scan detail, settings (tokens, upload, backup). Real stylesheet built
  with `./build-css.sh` (Tailwind v4.3.3 + daisyUI 5.7.47, cached in `.tools/`), htmx 2.0.11
  vendored under `internal/web/static/`. Verified in the browser pane. Restyled 2026-10-09 (ADR-002).
- [x] Phase 6 — History + duplicates (2026-10-08). Diffs at ingest (`scan_changes`), scan page,
  any-two-scans diff (`/scans/{a}/diff/{b}`, FULL OUTER JOIN by path), duplicates page by
  fingerprint or name+size with wasted bytes, other copies on clip and file pages.
- [x] Phase 7 — Polish (2026-10-08). `shelf login` + direct push, CSV and ALE export, backup
  command + Settings button, label/location editing, README, `make dist` (static linux/amd64 and
  arm64 server binaries + `deploy/shelf-server.service`). Docker image still deferred by decision.

Each phase ends with something runnable and a commit. Do not start the next phase's work in the
same change unless asked.

## Hard rules

1. **Scanning is strictly read-only.** `shelf` must never write to a scanned volume: no temp
   files, no tool output, no sidecars, no `.DS_Store`-style side effects. Vendor tools get a temp
   dir on the Mac (`os.MkdirTemp`), never a path under the mount. A test asserts the volume is
   unchanged after a scan. Treat any violation as a bug, not a config issue.
2. **A missing tool is never fatal.** Extractors report `Available()`; a clip missing one source
   is still recorded with what the other extractors produced plus a note of what was unavailable.
3. **Keep raw extractor output verbatim.** Every extractor's output is stored per source as JSON
   (text wrapped in JSON if needed) so fields can be mapped later without rescanning.
4. **Ingest is one transaction.** A scan lands fully or not at all.
5. **Metadata only.** No thumbnails, no frame decoding, no transcoding, no moving or deleting
   media. These are explicit v1 non-goals; do not add them "while you're there".
6. **No multi-user auth.** One password for the UI, API tokens for the CLI. Do not add roles.
7. **Pure Go.** `modernc.org/sqlite` (no cgo) so the Docker build is a plain static binary.
   Phase 1 verified FTS5 with the trigram tokenizer works on it (`internal/store.CheckFTS5Trigram`).
8. **Clips are media.** Only video and audio files (and the multi-file structures) become
   clips; images, projects, stray sidecars, and symlinks stay plain entries. Clip IDs are a hash
   of kind + root path, so a clip keeps its ID across rescans of the same drive.
9. **Extractors are per format/vendor, never per camera body.** Field mappings are data-driven
   tables, so a new camera's field names are a mapping change, not new code.

## Repo layout

Follow the layout in `plan.md` exactly:

```
cmd/shelf, cmd/shelf-server
internal/{bundle,scan,clips,extract/{ffprobe,arri,red,braw,sony,ale,bwf},meta,store,web}
testdata/
```

Shared code between CLI and server lives in `internal/bundle`, `internal/meta`, and
`internal/scanner` (the whole walk → hash → group → extract pipeline). The server runs scans of
its own attached folders through `internal/sched`, so it does import the extractors; a missing
tool on the server is handled like anywhere else (recorded on the clip, never fatal).

## Go skills: always load

For any Go coding, review, debugging, or setup task, load `golang-how-to` first; it routes to the
other installed Go skills.

## Conventions

- **Architecture:** flat packages under `internal/`, no clean/hexagonal layers, no DI library.
  Wire dependencies by hand in `cmd/*/main.go` with plain constructors. Add structure only when a
  package genuinely outgrows it.
- **Go:** standard library first. Use `log/slog` for logging, `context.Context` on every I/O
  path, `errors.Is/As` with `%w` wrapping. CLI via `spf13/cobra`. Config in
  `~/.config/shelf/config.toml`.
- **Formatting and lint:** `gofmt` and `go vet` must pass. If `golangci-lint` is added, commit
  its config and keep it passing.
- **Tests:** table-driven, `testing` + `testify` is fine. Each extractor parser is unit-tested
  against captured golden output under `testdata/golden/<extractor>/`. Real sidecars (ALE,
  `.sidecar`, Sony XML, RMD, BWF headers) live under `testdata/sidecars/`. Clip grouping is tested
  against synthetic directory trees with zero-byte placeholder files. Never commit real media.
- **Golden fixtures:** when a new extractor is built in Phase 4, capture its real output from the
  user's drives first, commit it, then write the parser against it. Ask the user to attach a
  drive; do not fabricate vendor-tool output.
- **Bounded concurrency:** extractor work goes through a worker pool with per-file timeouts. One
  bad file must not hang a scan.
- **Schema changes:** add a numbered migration under `internal/store/migrations/`; never edit a
  shipped migration.
- **Web UI:** Go `html/template` + htmx, embedded with `embed`. Handlers return HTML fragments
  for htmx requests (`HX-Request` header), full pages otherwise.
- **Component library: DaisyUI on Tailwind CSS.** Use DaisyUI components (`table`, `modal`,
  `drawer`, `badge`, `stats`, `card`, `tabs`, form controls, `navbar`) rather than hand-rolled
  CSS; reach for raw Tailwind utilities only for spacing and layout. CSS is built once with the
  **standalone Tailwind CLI binary** (no npm, no Node at runtime) from
  `internal/web/static/src/app.css` into `internal/web/static/app.css`, which is committed and
  embedded. A `make css` (or `./build-css.sh`) target wraps the command and documents the pinned
  Tailwind/DaisyUI versions. htmx and DaisyUI's JS-free design mean no bundler; `htmx.min.js` is
  vendored under `internal/web/static/`. Themes are daisyUI built-ins (see next item).
- **Visual design: clean and readable.** Read `docs/design/ui.md` before writing any template or
  CSS. daisyUI `silk` (light) + `dim` (dark via OS preference), system fonts, sentence case, cards
  and bordered `table-sm` tables, `.kv` definition lists for metadata with the source as a ghost
  badge, em dash for empty values, semantic colours only. The M8 emulation (ADR-001,
  `docs/design/m8-theme.md`) was replaced by ADR-002 on 2026-10-09.
- **Running the server locally:** `./start.sh` at the repo root builds and runs `shelf-server`
  against a local data dir (`./data`, gitignored) with dev-friendly env defaults
  (`SHELF_PASSWORD`, `SHELF_DATA_DIR=./data`, `SHELF_LISTEN=:8080`). This is the primary way to
  run the server during development and testing. **Docker comes later**: the Dockerfile and
  compose file are built only after the server has been proven to work via `./start.sh`. Do not
  make `start.sh` depend on Docker.
- **Commits:** at the end of each phase at minimum. Conventional short subject, body explains why.
  Don't commit `.shelf` bundles, databases, or anything under `data/`.
- **Decisions:** record non-obvious architectural decisions as ADRs under `docs/decisions/`
  (`ADR-NNN-title.md`; the `documentation-and-adrs` skill covers the format). ADR-001 covers the
  web UI stack.

## Environment (this Mac, verified 2026-10-07)

| Tool | Status | Notes |
|---|---|---|
| Go | 1.27.x via Homebrew (`/opt/homebrew/bin/go`) | Installed 2026-10-08. |
| ffprobe / ffmpeg | 8.0.1 at `/opt/homebrew/bin` | Used for the ffprobe extractor and for generating test fixtures. |
| Docker | 29.x at `/opt/homebrew/bin/docker` | For the server image and the end-to-end suite. |
| xxhsum | present | Handy for cross-checking fingerprints in tests. |
| sqlite3 CLI | present (miniconda) | Ad-hoc DB inspection only. |
| ARRI Reference Tool CMD (`art-cmd`) | **not installed yet** (2026-10-09) | Only the ART **GUI** app is in `/Applications`; it does not contain `art-cmd`. ART CMD is a separate download. Install per `docs/tools.md`, then re-check with `shelf doctor`. |
| REDCINE-X PRO / REDline | **not installed yet** (2026-10-09) | `RED Tools.app` in `/Applications` is the iPad app wrapper, not REDCINE-X PRO. Install per `docs/tools.md`; REDline lives inside the REDCINE-X PRO app bundle. |
| mediainfo | present (Homebrew) | Not used by Shelf. Handy for cross-checks: it reads the ARRICORE descriptor and R3D version. |
| Blackmagic RAW SDK | not installed | Only needed if the Phase 5 decision says the `.sidecar` + ffprobe path is too thin. |
| Arch | Apple Silicon (arm64) | Verify `art-cmd` runs natively; note if it needs Rosetta. |

Shell is zsh. There is no GNU `timeout`; use `go test -timeout` or `context.WithTimeout` instead.

## Common commands

Once the module exists:

```bash
go build ./... && go vet ./...
go test ./...
go test -run Integration -tags integration ./...   # slow, needs hdiutil / real tools
go run ./cmd/shelf dump /Volumes/<drive> | head
go run ./cmd/shelf doctor
./start.sh                                          # run shelf-server locally (dev)
./build-css.sh                                      # rebuild internal/web/static/app.css after CSS/template edits
make dist                                           # linux server binaries + systemd unit in dist/
docker compose up --build                           # later, once the server is proven
```

## Skills installed in `.claude/skills/`

Installed via skills.sh (`npx skills add …`). Use them when the task matches:

- `golang-how-to` — meta-router; start here for any Go question.
- `golang-cli`, `golang-spf13-cobra` — the `shelf` CLI: commands, flags, exit codes, signals.
- `golang-project-layout`, `golang-naming`, `golang-code-style`, `golang-structs-interfaces`,
  `golang-design-patterns` — structure and idiom.
- `golang-concurrency`, `golang-context`, `golang-safety` — the extractor worker pool, timeouts,
  cancellation.
- `golang-error-handling` — wrapping, `slog`, recording per-clip errors without aborting.
- `golang-database` — `database/sql` with SQLite, transactions, NULL handling.
- `golang-testing`, `golang-lint` — table tests, fixtures, vet/lint setup.
- `sqlite-expert` — WAL, PRAGMAs, FTS5, `VACUUM INTO` backups.
- `htmx` — hx-* attributes, swap strategies, fragment responses.
- `daisyui` — official daisyUI 5 component reference (class names, variants, theming). Use for
  every template that renders UI.
- `multi-stage-dockerfile` — the server image.
- `documentation-and-adrs` (user-level) — ADR format.

Add a skill with `npx skills add <owner/repo> -s <skill> -a claude-code -y` and list it here.

## Decisions so far (2026-10-08)

- **Name:** Shelf. Binaries `shelf` and `shelf-server`.
- **Module path:** `github.com/fenixstarlord/indexserver` (the GitHub repo). Binaries and the
  product are still called Shelf; imports look like `github.com/fenixstarlord/indexserver/internal/scan`.
- **Home server:** amd64 (x86_64). Still build the image multi-arch (amd64 + arm64) so it also
  runs locally on this Apple Silicon Mac for the end-to-end suite.
- **UI component library:** DaisyUI on Tailwind, built with the standalone Tailwind CLI, output
  committed. Chosen over templUI, Shoelace, and Pico to keep html/template and the no-runtime-build
  design.
- **Look (2026-10-09):** clean daisyUI `silk`/`dim` with system fonts, replacing the M8 emulation
  of 2026-10-08. Spec: `docs/design/ui.md`, decision: `docs/decisions/ADR-002-clean-ui-theme.md`.
  API keys have their own page at `/settings/api-keys`.
- **Vendor tools:** the user installs `art-cmd` and REDCINE-X PRO before Phase 4. Build the
  ffprobe, ALE, Sony XML, BRAW sidecar and BWF extractors first; ARRI and RED extractors last.

## Phase 4 findings (real media)

- ffprobe reads BRAW (`brhq` tag: resolution, fps, timecode in the tmcd stream, audio) and Canon
  CRM (`CRAW`: resolution, fps, format-level timecode) container basics. It cannot resolve the
  ARRICORE/ARRIRAW MXF video descriptor (no codec/size), but does return the MXF timecode and audio.
- ARRI ALEs (ALEXA 35, SUP 6) carry 58 columns; the mapping lives in `internal/extract/ale`.
  Audio_sr is in kHz. Start/End matched the embedded timecode on this card, but are marked weak.
- Sound Devices 688 WAVs: bext description holds `sSCENE=`/`sTAKE=` lines and iXML holds the same
  plus track names; `time_reference` at 48 kHz converts to timecode with the iXML rate.
- Resolve Cloud mirrors are symlink farms; `.d.mts` TypeScript files are classified as video by
  extension and fail ffprobe harmlessly (error recorded on the clip).

## Performance notes

- Walk: ~70k entries/s. Fingerprint: ~2.8 GB/s on internal SSD, I/O bound on external drives.
- Ingest of 614k entries: 16 s and ~600 MB of database per scan on `modernc.org/sqlite`. Typical
  shelf drives (a few thousand files) ingest in well under a second.
- **Do not trigram-index full paths.** It cost 35 s and ~2 GB per 600k-entry scan because every
  path repeats its parent prefix. `entries_fts` indexes names only (external-content table over
  `entries.id`); a search term containing `/` falls back to `LIKE` on `entries.path` (~0.1 s over
  600k rows). `clips_fts` is small and indexes the clip root path.
- Multi-row batched inserts (`batchInserter`) are used for entries, clips and changes.

## Drive survey (Phase 1, 2026-10-08)

`shelf dump --no-fingerprint` on `/Volumes/SSD_RAID` (16 TB HFS+ RAID, mixed work drive, not a
pure camera drive): 614k entries, 13.6 TB, walked in 8.6 s. Counts that matter for Phases 2 and 4:

- Video by extension: mov 5252, mxf 3814, mp4 2542, mts 1978, **crm 108** (Canon Cinema RAW
  Light, not in the plan's format table yet), r3d 4, braw 2.
- 798 `.RDC` directories but only 4 real `.r3d` files. The rest are DaVinci Resolve cache
  stubs (`Resolve Cloud/`, `Scratch/`) holding a single `.rtn` file, or `.RDC` folders whose R3D
  segments are **symlinks** into other volumes. Grouping ignores both on purpose: an RDC folder
  with no regular R3D file is not a clip, and symlinks never join clips. That drive has 3457
  symlinks; a future option could record symlink targets.
- 6 Sony card roots (`XDROOT` / `M4ROOT`), all under `Scratch/CacheClip/audio/` (Resolve audio
  cache mirroring card paths), not real cards.
- Audio: wav 2420 (plus music formats). Sidecars: json 2033, xml 1718, cdl 1626, cube 279,
  mhl 20, ale 3.
- Walk speed: roughly 70k entries/s on this volume; fingerprinting ran about 2.8 GB/s on the
  internal SSD. Scans of multi-TB drives are I/O bound on the head+tail reads, not the walk.

Extractor order suggested by this drive: ffprobe (mov/mxf/mp4/mts) → Sony XML → ALE/CDL →
R3D → BRAW → ARRI, with Canon CRM added to the format table. Re-survey when real camera drives
are attached; this RAID is not representative of the shelf drives.

## Menu bar app (2026-10-09)

`macos/ShelfMenu` is a SwiftUI `MenuBarExtra` app (SwiftPM, macOS 14+, built with the Command Line
Tools; no Xcode project). It does no scanning itself: it runs the bundled Go `shelf` binary
(`Contents/MacOS/shelf`) as a subprocess (`shelf scan <path>`, `shelf login <url>` with the key on
stdin) and streams its stderr as progress. Server and key therefore live in the CLI's
`config.toml`; the ignore list and launch-at-login are app-only (UserDefaults, SMAppService).
`macos/build-app.sh` (`make app`) builds a universal CLI and app and assembles `dist/Shelf.app`
with an ad-hoc signature. Keep CLI output lines stable; the app shows the last stderr line, and
reads `shelf doctor --json` to report missing metadata tools with a link to `docs/tools.md`.

## Decisions, continued

- **Server scans (2026-10-09):** `scan_jobs` table + `internal/sched`; one job at a time, 30 s
  tick, "Scan now" from Settings; `is_partial` applies to server scans the same way.
- **Groups and clients (2026-10-09):** two independent taxonomies, `drive_groups`/`drives.group_id`
  and `clients`/`drives.client_id`, both handled by `store.Taxonomy` (`ByGroup`, `ByClient`). The
  Drives page toggles between them, creates sets with the + button, and moves drives by
  drag-and-drop (`POST /drives/{id}/group` with `kind`). Empty sets persist until removed.
- **Default explorer view** is a server setting (`default_view`, list unless changed) that a
  browser cookie can override; the Duplicates page was removed (other copies remain on clip and
  file pages).

- **Partial scans (decided 2026-10-08):** a scan whose root is not the mount point is `is_partial`;
  latest and diffs are tracked per (drive, root); drive aggregates and default browsing prefer the
  latest full scan (`store.LatestScan`). Migration `0002_partial_scans.sql`.
- **Home server:** cross-compiled static binary + systemd unit (`make dist`), not Docker, for now.

## Open questions (ask the user, don't guess)

- NetBird hostname format for the server, for the README and `shelf login` examples.
- Which camera formats are actually present on the user's drives (drives the Phase 4 build order).
