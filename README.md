# Shelf

A self-hosted catalog of offline drives full of camera media.

- **`shelf`** (macOS CLI) indexes a mounted drive read-only: walks the filesystem, fingerprints
  files, groups camera card structures into clips (RED `.RDC`, ARRIRAW sequences, Sony cards,
  BRAW + sidecar), and extracts technical and camera metadata with ffprobe, ALE files, BWF/iXML,
  Sony XML, BRAW sidecars, REDline, and ARRI's `art-cmd`.
- **`shelf-server`** stores every scan in SQLite and serves a web UI to search and browse any
  drive while it is unplugged, see what changed between scans, and find duplicates.

The UI emulates the look of the Dirtywave M8 tracker. See `docs/design/m8-theme.md`.

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

Tool paths can be set in the config file:

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool.app/Contents/MacOS/art-cmd"
redline = "/Applications/REDCINE-X PRO/REDline"
```

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
shelf-server token create <name>  mint an API token for the CLI (shown once)
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

The UI uses daisyUI on Tailwind CSS with a custom M8 theme. The compiled stylesheet is committed
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

## Credits

Font: stealth57 by Trash80 (Timothy Lamb),
<https://fontstruct.com/fontstructions/show/413734/stealth57>, licensed CC BY-SA 3.0.
The Dirtywave M8 is a product of Dirtywave; this project is not affiliated with Dirtywave.
