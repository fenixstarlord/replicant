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
SHELF_DATA_DIR=./data ./bin/shelf-server token create laptop   # prints a key once (or use the API keys page)
./bin/shelf login 'shelf://shelf_...@127.0.0.1:8080'           # paste the key; it carries the server address
./bin/shelf doctor                                             # which extractors are available
./bin/shelf scan /Volumes/MyDrive                              # scan and push
```

Open <http://127.0.0.1:8080>, log in with the password, and the drive appears under DRIVES.

## CLI

```
shelf login <key>                      save server + key to ~/.config/shelf/config.toml
shelf login <server-url> [--token T]   same, with the key given separately (prompted if omitted)
shelf doctor                           show extractors, tool availability, versions
shelf scan /Volumes/X                  scan and push to the server
shelf scan /Volumes/X -o x.shelf       scan to a bundle file (no network)
shelf scan /Volumes/X --fast           filesystem only, no metadata extraction
shelf upload x.shelf                   push a bundle later
shelf drives                           list drives known to the server
shelf dump /Volumes/X [--clips]        print what would be indexed as JSON lines
```

Scans never write to the scanned volume. Vendor tools get a temp directory on the Mac.
Which tools are needed for which formats, and where to get them: `docs/tools.md`.

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
Browse… scans a folder or network share instead. Settings… takes the connection key from the
server's API keys page (one string with the address and the key, stored in the same
`~/.config/shelf/config.toml` the CLI uses), drives to ignore, automatic scans (a global "scan any
external drive when it is mounted" switch, plus per-drive rules for on-mount and/or interval
rescans), and launch at login. Scans run one at a time; a notification reports each result.

## Server

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `SHELF_PASSWORD` | required | UI password (or `SHELF_PASSWORD_HASH`, bcrypt) |
| `SHELF_DATA_DIR` | `/data` | where `shelf.db`, uploads and backups live |
| `SHELF_LISTEN` | `:8080` | listen address |
| `SHELF_SESSION_SECRET` | auto | cookie signing key; generated and persisted if unset |
| `SHELF_PUBLIC_URL` | auto | address baked into API keys, e.g. `http://100.64.0.5:8080`; see Remote access |
| `SHELF_AUTH` | | `open` turns authentication off; only allowed with a loopback `SHELF_LISTEN` (used by the standalone app) |

Commands:

```
shelf-server                      serve (default)
shelf-server token create <name>  mint an API key (or use the API keys page in the web UI)
shelf-server token list | revoke <id>
shelf-server ingest <file.shelf>  import a bundle without HTTP
shelf-server backup <path|dir>    consistent copy of the database (VACUUM INTO)
```

API (bearer token): `POST /api/scans` (bundle body), `GET /api/drives`, `GET /api/me`.

### Remote access

Run the server on the home server and reach it over NetBird. An API key is a connection string
such as `shelf://shelf_abc@100.64.0.5:8080`, so the Mac needs only one paste. The address in it
is resolved in this order:

1. `SHELF_PUBLIC_URL`, for Docker, where the container cannot see the host's interfaces.
2. Settings → Server address in the web UI (the detected interfaces are offered as buttons).
3. A host in `SHELF_LISTEN`, e.g. `SHELF_LISTEN=100.64.0.5:8080`, which also binds only there.
4. The server's NetBird interface (`wt0`, or any 100.64.0.0/10 address), detected automatically
   when the server runs directly on the host.
5. The address the browser used to open the API keys page.

The server does not terminate TLS; keep it off the public internet. For HTTPS behind a proxy, set
`SHELF_PUBLIC_URL=https://…` and keys become `shelfs://…`.

### Docker

The image is a static `shelf-server` on Debian slim with ffmpeg (for ffprobe), published to
`ghcr.io/fenixstarlord/indexserver` for amd64 and arm64 by the GitHub workflow on every push to
`main` (`latest`) and on `v*` tags. `make docker` builds it locally.

```bash
cp .env.example .env     # set SHELF_PASSWORD and SHELF_PUBLIC_URL
docker compose up -d     # http://<host>:8080
```

`docker-compose.yml` mounts `./data` at `/data` (database, uploads, backups) and `./media` at
`/media` read-only for server-side scans. Vendor tools are not in the image; Linux builds of
`art-cmd` or `REDline` can be bind-mounted at `/opt/shelf-tools`, which is on `PATH`.

**TrueNAS SCALE:** Apps → Discover Apps → Custom App (or "Install via YAML" and paste the compose
file). Image `ghcr.io/fenixstarlord/indexserver:latest`, port 8080, environment `SHELF_PASSWORD`
and `SHELF_PUBLIC_URL` (the NetBird address of the NAS, e.g. `http://100.64.0.5:8080`), host-path
storage: a dataset such as `/mnt/pool/apps/shelf` at `/data`, and your media datasets at `/media`
read-only. The container runs as UID/GID 1000; give the data dataset that owner or set the app's
user accordingly. The container cannot see the host's NetBird interface, hence `SHELF_PUBLIC_URL`.

### Standalone app (no server)

`make standalone` builds `dist/Shelf Standalone.app`: the menu bar app with `shelf-server`
bundled. It runs the catalog on this Mac at `http://127.0.0.1:8787` with authentication off
(`SHELF_AUTH=open`, loopback only), keeps its data in `~/Library/Application Support/Shelf`, and
connects the scanner to it automatically. There is no password and no API key: click a drive to
scan it, "Open catalog" to browse. Its config file lives in the data folder, so it does not touch
a `~/.config/shelf/config.toml` set up for a remote server.

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
