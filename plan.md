# Drive Catalog — Build Plan

Working name: **Replicant** (rename freely). A self-hosted catalog of offline drives full of camera media. A Mac CLI indexes a mounted drive and pulls technical and camera metadata, including from cinema raw formats. A Dockerized server on a home server stores every scan and makes it searchable and browsable in a web UI.

## Goals

- Index a mounted drive on macOS and get its contents into the server, either by pushing directly or by exporting a bundle to upload later.
- Extract rich metadata from camera originals: ARRIRAW, ARRICORE, ProRes (ARRI and others), R3D, BRAW, Sony X-OCN / Sony RAW, plus common delivery formats and audio. Use embedded metadata, vendor CLI tools, and sidecar files (ALE, `.sidecar`, Sony XML, RMD).
- Treat multi-file clips (R3D spans, ARRIRAW frame sequences, camera card structures) as single clips.
- Search across all drives by filename, path, and metadata, with filters and substring/fuzzy matching.
- Browse any drive's folder tree in the browser while the drive is offline.
- Keep full scan history per drive and show what changed between scans.

## Non-goals (v1)

- Thumbnails or any frame decoding. Too slow on large raw media; metadata only.
- Multi-user accounts or permissions. One shared password.
- A desktop GUI. The indexer is a CLI. A menu-bar wrapper can come later.
- Windows/Linux indexer builds.
- Playing, transcoding, moving, renaming, or deleting media.

## Scale assumptions

- A few thousand files per drive, mostly very large media. Exception: legacy ARRIRAW `.ari` is one file per frame, so a single clip can be tens of thousands of files. Clip grouping (below) keeps the catalog at clip granularity for those.
- Tens to low hundreds of drives. SQLite is sufficient; no separate search engine.

## Architecture

```
 Mac (drive mounted)                        Home server (Docker)
┌───────────────────────────┐  HTTP(S) + ┌─────────────────────────────┐
│ replicant CLI (Go)            │  API token │ replicant-server (Go)           │
│  - walk filesystem        │ ─────────▶ │  - REST API (ingest)        │
│  - group files into clips │            │  - web UI (Go templates +   │
│  - run extractors:        │  or bundle │    htmx, embedded)          │
│    ffprobe, art-cmd,      │ ─ upload ▶ │  - SQLite + FTS5            │
│    REDline, sidecar parse │            │  /data: replicant.db            │
│  - write scan bundle      │            └─────────────────────────────┘
└───────────────────────────┘
```

All media tooling runs on the Mac. The server never touches media and needs no vendor tools.

### Repo layout (monorepo)

```
/cmd/replicant               indexer CLI
/cmd/replicant-server        server
/internal/bundle         scan bundle format, shared by both sides
/internal/scan           filesystem walk, skip rules, fingerprinting
/internal/clips          multi-file clip / card-structure detection
/internal/extract        extractor interface + one package per extractor
  /ffprobe /arri /red /braw /sony /ale /bwf
/internal/meta           normalized metadata model + field mapping
/internal/store          SQLite schema, migrations, queries
/internal/web            HTTP handlers, templates, static assets (embed)
/testdata                fixtures (see Testing)
Dockerfile, docker-compose.yml
```

## Tech choices

| Area | Choice | Why |
|---|---|---|
| Language | Go for both indexer and server | One static binary each, fast filesystem walk, shared bundle code |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no cgo) | Plenty for this scale, single file, trivial backups |
| Search | SQLite FTS5 with `trigram` tokenizer | Substring and fuzzy-ish matching on names, paths, and key metadata |
| Web UI | Go `html/template` + htmx, embedded with `embed` | No JS build step, single binary |
| Auth | Single password (env var) → session cookie; API tokens for the CLI | Matches the one-password requirement |

**Verify in Phase 1:** `modernc.org/sqlite` supports FTS5 with the trigram tokenizer. If not, switch to `mattn/go-sqlite3` with the `sqlite_fts5` build tag (requires cgo in the Docker build).

## Metadata extraction

### Extractor design

Each extractor implements:

```go
type Extractor interface {
    Name() string
    Available() (ok bool, version string) // is the external tool installed?
    Matches(c *Clip) bool                 // by extension, container, or card structure
    Extract(ctx context.Context, c *Clip) (*meta.Result, error)
}
```

- Several extractors can match one clip. Results merge in priority order: vendor tool > sidecar > ffprobe. Every field records which source it came from.
- Each extractor's raw output is stored verbatim (JSON, or text wrapped in JSON) per source, so new fields can be mapped later without rescanning.
- A missing tool is never fatal. The clip is recorded with whatever other extractors produced, plus a note of which extractor was unavailable.
- `replicant doctor` lists every extractor, whether its tool was found, and its version.
- Tool paths are configurable in `~/.config/replicant/config.toml`, with sensible macOS defaults (e.g. REDline inside the REDCINE-X PRO app bundle).
- Bounded worker pool. Per-file timeouts so one bad file can't hang a scan.

### Per-format plan

| Format | Container | Primary source | Secondary sources | Notes |
|---|---|---|---|---|
| ARRICORE | MXF | **ARRI Reference Tool CMD (`art-cmd`)** — free; exports static camera metadata as JSON or CSV | ALE, ffprobe (container/timecode) | Metadata is embedded in the MXF header per SMPTE RDD 61/55 |
| ARRIRAW (MXF) | MXF | `art-cmd` | ALE, ffprobe | ALEXA 35 / Mini LF era |
| ARRIRAW legacy `.ari` / HDE `.arx` | frame sequence | `art-cmd` (supports ARI/ARX single-frame sequences as input) | ALE | Group the sequence into one clip; run `art-cmd` once per clip, not per frame |
| ARRI ProRes | MOV / MXF | `art-cmd` for camera metadata | ffprobe, ALE | ffprobe gives the technical basics |
| R3D | `.RDC` folder of `_001.R3D`, `_002.R3D`… | **REDline `--printMeta 2`** (CSV) — free with REDCINE-X PRO | `.RMD` sidecar (XML), ffprobe not useful | One clip per `.RDC`; run on the first segment |
| BRAW | QuickTime-style `.braw` | **`.sidecar` JSON** (no tool needed) + ffprobe for container basics | Optional helper `replicant-braw` built on the Blackmagic RAW SDK | Blackmagic ships no CLI. The SDK includes an ExtractMetadata sample; build a tiny helper in Phase 5 if ffprobe + sidecar aren't enough |
| Sony X-OCN / Sony RAW | MXF in card structure (`XDROOT/Clip`, `PRIVATE/M4ROOT`) | **Clip XML sidecar** (NonRealTimeMeta: device, lens, capture settings) | ffprobe (container, duration, timecode) | ffprobe can read the MXF wrapper but can't decode X-OCN; that's fine since we don't decode |
| ProRes / H.264 / H.265 / DNx / XAVC | MOV / MP4 / MXF | ffprobe | Sony XML when on a card | |
| WAV / BWF / AIFF | | ffprobe | Dedicated BWF `bext` + iXML parser (scene, take, tape, notes, timecode) | Production audio carries a lot in iXML |
| ALE | text | Parsed directly (tab-delimited Heading/Column/Data) | | Matched to clips by clip name / source file columns; fills gaps from any extractor |

**Unknowns to resolve with real media in Phase 4:**
- Exact `art-cmd` flags for a metadata-only export, its speed per clip, and whether it runs on Apple Silicon without Rosetta.
- Whether ffprobe returns resolution, fps, and timecode for `.braw` and X-OCN MXF.
- Which ALE columns your cameras/DITs actually populate.
- Whether `REDline --printMeta` needs a license or a display (it should not).

### Normalized fields

Common fields extracted from any source, stored as columns so they can be filtered:

- **Clip:** clip name, reel/roll, camera index (A/B cam), scene, take, circled/flag, recording date, start timecode, end timecode, duration, frame count
- **Format:** container, codec (incl. ARRICORE / R3D / BRAW / X-OCN flavor and compression ratio or quality), resolution, sensor mode / recording area, fps (project and sensor/capture), bit depth, color space/gamma (LogC3/LogC4/Log3G10/BMD Film/S-Log3), anamorphic squeeze
- **Camera:** make, model, serial, firmware
- **Exposure:** ISO/EI, white balance (K), tint, shutter (angle and speed), ND
- **Lens:** lens model, focal length, T-stop/f-stop, focus distance (static value at start)
- **Audio:** channels, sample rate, bit depth
- **Look:** look file / LUT name

Everything else stays in the per-source raw JSON, which is shown on the file detail page and partially indexed for search.

## Clip grouping

Done before extraction, so extractors receive a `Clip` (one or more files) rather than a single file:

- **R3D:** each `.RDC` directory → one clip containing its `.R3D` segments and `.RMD`.
- **ARRIRAW `.ari` / `.arx` sequences:** a directory of sequentially numbered frames → one clip. Store the frame count and first/last frame; individual frames are **not** stored as entries (configurable).
- **Sony card structures:** an MXF and its matching XML in `XDROOT/Clip` or `M4ROOT/CLIP` → one clip.
- **Sidecars next to a file** (`.sidecar`, `.xml`, `.RMD`) are attached to the clip they describe.
- Everything else: one file = one clip.

Folders still appear in the browse tree normally. The clip just becomes the unit for search results and metadata.

## Indexer CLI (`replicant`)

```
replicant login <server-url>          # paste an API token, saved to ~/.config/replicant/config.toml
replicant doctor                      # show extractors, tool availability, versions
replicant scan /Volumes/X             # scan and push to server (default when logged in)
replicant scan /Volumes/X -o x.replicant  # scan to a bundle file, no network
replicant scan /Volumes/X --fast      # filesystem only, no metadata extraction
replicant upload x.replicant              # push a bundle later
replicant drives                      # list known drives on the server
replicant dump /Volumes/X [--clip P]  # print what would be indexed as JSON (debugging)
```

### Scan behavior

- **Strictly read-only.** Never write to the scanned volume, including no temp files or tool output on it. Vendor tools write to a temp dir on the Mac. A test asserts the volume is unchanged after a scan.
- **Drive identity:** volume UUID from `diskutil info -plist <mount>`. Also capture volume name, filesystem, total capacity, free space, and device/model if available.
- **Skip:** `.Spotlight-V100`, `.fseventsd`, `.Trashes`, `.TemporaryItems`, `._*`, `.DS_Store`. Configurable.
- **Packages:** treat macOS bundles (`.fcpbundle`, `.app`, `.photoslibrary`, Resolve `.drp` folders if any) as single items by default. Flag to descend.
- **Per entry:** relative path, name, extension, is_dir, size, mtime, birthtime, kind (`video`, `audio`, `sidecar`, `image`, `project`, `other`).
- **Fingerprint:** xxHash64 of the first 1 MB + the last 1 MB + the size. Fast on huge files, good enough for duplicate detection. `--full-hash` computes a full xxHash64 (slow, optional). If an MHL (media hash list) exists on the drive, record its hashes too; they're free and DIT-verified.
- **Progress:** files, bytes, clips extracted, current file, ETA.
- **Resilience:** a file or clip that fails extraction is still recorded, with the error.

### Scan bundle format (`.replicant`)

A zip file containing:

```
manifest.json   { bundle_version, scanner_version, scanned_at,
                  volume{uuid,name,fs,total,free,...},
                  options, extractors[{name,version,available}] }
entries.jsonl   one object per file/dir (with clip_id where applicable)
clips.jsonl     one object per clip: member entries, normalized fields,
                field sources, raw output per extractor, errors
```

Direct push uses the same bundle: the CLI builds it in a temp dir and streams it to `POST /api/scans`. One code path.

## Server (`replicant-server`)

### Config (env vars)

- `REPLICANT_PASSWORD` (required) or `REPLICANT_PASSWORD_HASH` (bcrypt).
- `REPLICANT_DATA_DIR` (default `/data`).
- `REPLICANT_LISTEN` (default `:8080`).
- `REPLICANT_SESSION_SECRET` (auto-generated and persisted in the data dir if unset).

### Ingest

- `POST /api/scans` (Bearer API token) accepts a bundle. The web UI's "Upload scan" uses the same handler.
- Insert the whole scan in **one transaction**. On any error, roll back.
- Upsert the drive by volume UUID; update name, capacity, free space, last_seen.
- Compute per-directory recursive sizes and file counts at ingest, so tree browsing is instant.
- Compute the diff against the drive's previous scan and store summary counts (added, removed, changed). Compare by path; changed = different size, mtime, or fingerprint.

### Schema (initial)

```sql
drives(id, volume_uuid UNIQUE, name, label, location, notes, fs_type,
       capacity_bytes, free_bytes, first_seen, last_seen)
       -- label/location/notes are user-editable (e.g. "Shelf B, box 3")

scans(id, drive_id, scanned_at, ingested_at, scanner_version,
      options_json, extractors_json,
      file_count, clip_count, total_bytes, added, removed, changed, is_latest)

entries(id, scan_id, parent_path, path, name, ext, kind, is_dir,
        size, mtime, btime, fingerprint, full_hash, mhl_hash,
        clip_id, dir_total_size, dir_file_count)
        -- index: (scan_id, parent_path), (fingerprint), (name, size), (clip_id)

clips(id, scan_id, root_path, name, kind, file_count, total_size,
      -- normalized fields (all nullable):
      clip_name, reel, camera_index, scene, take, circled, recorded_at,
      tc_start, tc_end, duration_s, frame_count,
      container, codec, codec_detail, width, height, sensor_mode,
      fps, capture_fps, bit_depth, color_gamma, squeeze,
      camera_make, camera_model, camera_serial, firmware,
      iso, wb_kelvin, tint, shutter_angle, shutter_speed, nd,
      lens, focal_mm, t_stop, focus_distance,
      audio_channels, sample_rate, audio_bit_depth, look,
      field_sources_json, errors_json)

clip_raw(clip_id, source, raw_json)     -- verbatim output per extractor

clips_fts      -- FTS5 (trigram): clip name, reel, scene, take, camera, lens, look, path
entries_fts    -- FTS5 (trigram): file name, path

api_tokens(id, name, token_hash, created_at, last_used_at)
```

`scans.is_latest` is maintained on ingest. Search, browse, and duplicates default to latest scans only, with a toggle to include history.

### Web UI pages

1. **Login.**
2. **Drives:** name, label, location, capacity, free space, clip/file counts, last scanned, scan count. Edit label/location/notes inline.
3. **Search:** query box with live results (htmx, debounced) over clips and files. Filters: drive, kind, codec, camera model, resolution, fps, color gamma, ISO range, WB range, lens, focal length range, reel, scene/take, recorded date range, duration range, size range, timecode contains. Results show clip name, drive, path, codec, resolution, fps, duration, camera, reel, size. Sortable columns. "Include old scans" toggle.
4. **Browse:** drive → folder tree with recursive sizes. Clip folders (`.RDC`, `.ari` sequences) show as a single clip row with an expand option.
5. **Clip/file detail:** normalized metadata grouped (Clip / Format / Camera / Exposure / Lens / Audio), each field with its source shown on hover, raw output per extractor (collapsed), the drive's physical location, other copies (same fingerprint on other drives), and the history of this path across scans.
6. **Scan history:** per-drive list of scans with added/removed/changed counts. Pick any two scans to see the diff.
7. **Duplicates:** groups by fingerprint (name+size fallback) across drives, with copy count and wasted bytes.
8. **Settings:** create/revoke API tokens, upload a bundle, backup.
9. **Export:** CSV of current search results or a whole drive. Also export **ALE** for a search result, so a set of clips can go straight into Avid/Resolve.

Mobile-friendly layout, since "which drive is it on?" gets asked from a phone.

## Deployment (home server)

- Multi-stage Dockerfile; distroless or alpine final image with just the server binary. Build multi-arch (amd64 + arm64).
- `docker-compose.yml`: one service, one volume at `/data`, port 8080, `restart: unless-stopped`.
- Backups: `replicant-server backup <path>` using SQLite `VACUUM INTO`, also a Settings button.
- Remote access: via **NetBird**. The server listens only on the host's NetBird interface or LAN. No public port, and the app doesn't terminate TLS. The README should document binding to the NetBird IP and point the CLI at the server's NetBird address or name, e.g. `replicant login http://replicant.netbird.cloud:8080`; confirm the actual name format in their setup.

## Testing

- **Fixture strategy:** cinema raw files can't be generated with ffmpeg, and real samples are huge. So:
  - Store **captured tool output** (art-cmd JSON/CSV, REDline CSV, ffprobe JSON) for real clips under `testdata/golden/` and unit-test parsers and field mapping against them. Capture these from your drives during Phase 4.
  - Store real **sidecars** (ALE, `.sidecar`, Sony XML, RMD, BWF headers) — they're small.
  - Synthetic **directory structures** (`.RDC` folders, `.ari` sequences, `XDROOT` cards) with zero-byte or tiny placeholder files to test clip grouping.
  - ffmpeg-generated ProRes/H.264/MXF/WAV with timecode for the ffprobe path.
  - Optional manufacturer sample clips (ARRI, RED, and Blackmagic publish sample footage) for a slow, opt-in integration suite.
- **Indexer:** walk/skip rules, fingerprinting, clip grouping, each extractor's parser, merge priority, bundle round-trip, read-only guarantee, and a macOS integration test that mounts an `hdiutil` disk image to check UUID capture.
- **Server:** ingest + rollback, diff correctness across three scans, FTS and filter queries, ALE/CSV export, auth on every route.
- **End-to-end:** server in a container, `replicant scan` against fixtures, assert via the API.

## Build phases

Each phase ends with something runnable. Commit at the end of each.

1. **Skeleton:** Go module, layout, `replicant dump` (walk + skip rules + stat + fingerprint → JSON), FTS5 trigram check. **Done when:** `replicant dump` on a real drive prints sensible entries quickly. *User attaches real drives here; count files, note folder structures and formats present.*
2. **Clip grouping + bundle:** R3D/ARRIRAW/Sony card detection, sidecar attachment, bundle writer, `replicant scan -o --fast`. **Done when:** a real drive's bundle shows clips grouped correctly.
3. **Server core:** schema + migrations, ingest, API tokens, password login, Docker image, compose file. **Done when:** `replicant upload` lands a scan in the DB on the home server.
4. **Extractors:** extractor framework, `replicant doctor`, then ffprobe → ALE → art-cmd → REDline → Sony XML → BRAW sidecar → BWF/iXML. Capture golden outputs from real media as each one is built. **Done when:** a real drive scan fills codec, resolution, fps, timecode, camera, ISO, WB, and lens for each raw format present.
5. **Search + browse UI:** drives page, search with filters, tree browser, clip/file detail. **Done when:** a clip can be found by partial name or by "ISO 800 + 25 fps + ALEXA 35" with the drive unplugged. Decide here whether the BRAW SDK helper is needed.
6. **History + duplicates:** diffs at ingest, history page, scan diff view, duplicates page, other copies on detail.
7. **Polish:** `replicant login` + direct push, CSV and ALE export, backup command, label/location editing, mobile pass, README.

## Later

- `replicant-braw` helper on the Blackmagic RAW SDK, if sidecar + ffprobe prove too thin.
- Dynamic (per-frame) metadata such as lens focus pulls; v1 stores static/start values only.
- Optional thumbnails via vendor tools, opt-in per drive.
- A menu-bar app that auto-scans drives when they mount.
- Windows/Linux indexer builds.

## Camera coverage

The team works with many camera bodies, and the mix varies by job. So:

- Extractors are built **per format/vendor, not per camera body**. One ARRI extractor covers every ALEXA/AMIRA; one RED extractor covers every R3D camera.
- Every vendor in the per-format table is in scope for v1. Phase 4 builds them in the order the Phase 1 survey of real drives shows they're most common.
- Unrecognized formats still get ffprobe plus filesystem data, and the raw output is kept. Add a `replicant scan --report-unknown` summary listing extensions and containers that no extractor fully handled, so gaps show up as new cameras appear.
- Keep extractor field mappings data-driven (a mapping table per source), so adding a new camera's field names is a small change, not new code.

## Open questions

- Home server CPU architecture (multi-arch build covers both, but good to know for testing).
- Name.
