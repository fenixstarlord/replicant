# Shelf

A self-hosted catalog of offline drives full of camera media.

- **`shelf`** (macOS CLI) indexes a mounted drive read-only: walks the filesystem, fingerprints
  files, groups camera card structures into clips (RED `.RDC`, ARRIRAW sequences, Sony cards,
  BRAW + sidecar), and extracts technical and camera metadata with ffprobe, ALE files, BWF/iXML,
  Sony XML, BRAW sidecars, REDline, and ARRI's `art-cmd`.
- **`shelf-server`** stores every scan in SQLite and serves a web UI to search and browse any
  drive while it is unplugged and see what changed between scans. It can also scan folders
  mounted on the server itself, on demand or on a schedule (Settings → Server scans).

The web UI is a clean daisyUI/Tailwind interface with automatic dark mode. See `docs/design/ui.md`.

## Quick start (development)

```bash
brew install go ffmpeg          # Go 1.27+, ffprobe
./start.sh                      # server on :8080, data in ./data, password "shelf"
```

In another terminal:

```bash
make build
SHELF_DATA_DIR=./data ./bin/shelf-server token create laptop   # prints a token once
./bin/shelf login http://127.0.0.1:8080 --token shelf_...
./bin/shelf doctor                                             # which extractors are available
./bin/shelf scan /Volumes/MyDrive                              # scan and push
```

Open <http://127.0.0.1:8080>, log in with the password, and the drive appears under DRIVES.

## CLI

```
shelf login <server-url> [--token T]   save server + token to ~/.config/shelf/config.toml
shelf doctor                           show extractors, tool availability, versions
shelf scan /Volumes/X                  scan and push to the server
shelf scan /Volumes/X -o x.shelf       scan to a bundle file (no network)
shelf scan /Volumes/X --fast           filesystem only, no metadata extraction
shelf upload x.shelf                   push a bundle later
shelf drives                           list drives known to the server
shelf dump /Volumes/X [--clips]        print what would be indexed as JSON lines
```

Scans never write to the scanned volume. Vendor tools get a temp directory on the Mac.

Metadata extraction uses external tools where a format needs one: ffprobe, ARRI's `art-cmd` and
RED's `REDline`. All are optional; a missing tool is noted on the affected clips. Installation
on the Mac and on the Linux server is covered in [docs/tools.md](docs/tools.md). Tools are found
on `PATH` or in their usual install locations; explicit paths can be set in the config file:

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool CMD/art-cmd"
redline = "/Applications/REDCINE-X Professional/REDCINE-X PRO.app/Contents/MacOS/REDline"
```

## Menu bar app (macOS)

`make app` builds `dist/Shelf.app`, a menu bar app with the `shelf` CLI bundled inside. Drag it to
Applications and open it. From the drop-down, click a drive to scan it and push it to the server;
Browse… scans a folder or network share instead. Settings… holds the server URL and API key
(stored in the same `~/.config/shelf/config.toml` the CLI uses), drives to ignore, and launch at
login. Scans run one at a time; a notification reports each result.

## Server

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `SHELF_PASSWORD` | required | UI password (or `SHELF_PASSWORD_HASH`, bcrypt) |
| `SHELF_DATA_DIR` | `/data` | where `shelf.db`, uploads and backups live |
| `SHELF_LISTEN` | `:8080` | listen address |
| `SHELF_SESSION_SECRET` | auto | cookie signing key; generated and persisted if unset |

Commands:

```
shelf-server                      serve (default)
shelf-server token create <name>  mint an API key for the CLI (or use the API keys page in the web UI)
shelf-server token list | revoke <id>
shelf-server ingest <file.shelf>  import a bundle without HTTP
shelf-server backup <path|dir>    consistent copy of the database (VACUUM INTO)
```

API (bearer token): `POST /api/scans` (bundle body), `GET /api/drives`, `GET /api/me`.

### Remote access

Run the server on the home server and reach it over NetBird. Bind to the NetBird or LAN
interface only, for example `SHELF_LISTEN=100.64.0.5:8080`, and point the CLI at it:
`shelf login http://shelf.netbird.cloud:8080`. The server does not terminate TLS; keep it off the
public internet.

### Docker

Deferred until the server has been proven with `./start.sh`. The server is a single static
binary, so the image will be a multi-stage build with a distroless final stage.

## Building the stylesheet

The UI uses daisyUI on Tailwind CSS. The compiled stylesheet is committed
and embedded; rebuild it after editing `internal/web/static/src/app.css` or the templates:

```bash
./build-css.sh    # downloads the pinned standalone Tailwind CLI and daisyUI on first run
```

## Layout

```
cmd/shelf, cmd/shelf-server
internal/scan      filesystem walk, skip rules, fingerprinting, volume identity
internal/clips     multi-file clip detection
internal/extract   extractor framework + ffprobe, ale, bwf, sony, braw, red, arri
internal/meta      normalized metadata model and merge
internal/bundle    .shelf bundle format (zip of manifest + jsonl)
internal/store     SQLite schema, migrations, ingest, queries, export
internal/web       HTTP API, auth, templates, static assets
docs/              design spec and decision records
testdata/          golden tool output, sidecars, carved BWF headers
```
