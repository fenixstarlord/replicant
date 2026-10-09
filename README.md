# Replicant

A self-hosted catalog of offline drives full of camera media.

- **`replicant`** (macOS CLI) indexes a mounted drive read-only: walks the filesystem, fingerprints
  files, groups camera card structures into clips (RED `.RDC`, ARRIRAW sequences, Sony cards,
  BRAW + sidecar), and extracts technical and camera metadata with ffprobe, ALE files, BWF/iXML,
  Sony XML, BRAW sidecars, REDline, and ARRI's `art-cmd`.
- **`replicant-server`** stores every scan in SQLite and serves a web UI to search and browse any
  drive while it is unplugged and see what changed between scans. It can also scan folders
  mounted on the server itself, on demand or on a schedule (Settings → Server scans).

The web UI is a clean daisyUI/Tailwind interface with automatic dark mode. See `docs/design/ui.md`.

## Quick start (development)

```bash
brew install go ffmpeg          # Go 1.27+, ffprobe
./start.sh                      # server on :8080, data in ./data, password "replicant"
```

In another terminal:

```bash
make build
REPLICANT_DATA_DIR=./data ./bin/replicant-server token create laptop   # prints a key once (or use the API keys page)
./bin/replicant login 'replicant://replicant_...@127.0.0.1:8080'           # paste the key; it carries the server address
./bin/replicant doctor                                             # which extractors are available
./bin/replicant scan /Volumes/MyDrive                              # scan and push
```

Open <http://127.0.0.1:8080>, log in with the password, and the drive appears under DRIVES.

## CLI

```
replicant login <key>                      save server + key to ~/.config/replicant/config.toml
replicant login <server-url> [--token T]   same, with the key given separately (prompted if omitted)
replicant doctor                           show extractors, tool availability, versions
replicant scan /Volumes/X                  scan and push to the server
replicant scan /Volumes/X -o x.replicant       scan to a bundle file (no network)
replicant scan /Volumes/X --fast           filesystem only, no metadata extraction
replicant upload x.replicant                   push a bundle later
replicant drives                           list drives known to the server
replicant dump /Volumes/X [--clips]        print what would be indexed as JSON lines
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

`make app` builds `dist/Replicant.app`, a menu bar app with the `replicant` CLI bundled inside. Drag it to
Applications and open it. From the drop-down, click a drive to scan it and push it to the server;
Browse… scans a folder or network share instead. Settings… takes the connection key from the
server's API keys page (one string with the address and the key, stored in the same
`~/.config/replicant/config.toml` the CLI uses), drives to ignore, automatic scans (a global "scan any
external drive when it is mounted" switch, plus per-drive rules for on-mount and/or interval
rescans), and launch at login. Scans run one at a time; a notification reports each result.

## Server

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `REPLICANT_PASSWORD` | required | UI password (or `REPLICANT_PASSWORD_HASH`, bcrypt) |
| `REPLICANT_DATA_DIR` | `/data` | where `replicant.db`, uploads and backups live |
| `REPLICANT_LISTEN` | `:8080` | listen address |
| `REPLICANT_SESSION_SECRET` | auto | cookie signing key; generated and persisted if unset |
| `REPLICANT_PUBLIC_URL` | auto | address baked into API keys, e.g. `http://100.64.0.5:8080`; see Remote access |
| `REPLICANT_AUTH` | | `open` turns authentication off; only allowed with a loopback `REPLICANT_LISTEN` (used by the standalone app) |

Commands:

```
replicant-server                      serve (default)
replicant-server token create <name>  mint an API key (or use the API keys page in the web UI)
replicant-server token list | revoke <id>
replicant-server ingest <file.replicant>  import a bundle without HTTP
replicant-server backup <path|dir>    consistent copy of the database (VACUUM INTO)
```

API (bearer token): `POST /api/scans` (bundle body), `GET /api/drives`, `GET /api/me`.

### Remote access

Run the server on the home server and reach it over NetBird. An API key is a connection string
such as `replicant://replicant_abc@100.64.0.5:8080`, so the Mac needs only one paste. The address in it
is resolved in this order:

1. `REPLICANT_PUBLIC_URL`, for Docker, where the container cannot see the host's interfaces.
2. Settings → Server address in the web UI (the detected interfaces are offered as buttons).
3. A host in `REPLICANT_LISTEN`, e.g. `REPLICANT_LISTEN=100.64.0.5:8080`, which also binds only there.
4. The server's NetBird interface (`wt0`, or any 100.64.0.0/10 address), detected automatically
   when the server runs directly on the host.
5. The address the browser used to open the API keys page.

The server does not terminate TLS; keep it off the public internet. For HTTPS behind a proxy, set
`REPLICANT_PUBLIC_URL=https://…` and keys become `replicants://…`.

### Docker

The image is a static `replicant-server` on Debian slim with ffmpeg (for ffprobe), published to
`ghcr.io/fenixstarlord/indexserver` for amd64 and arm64 by the GitHub workflow on every push to
`main` (`latest`) and on `v*` tags. `make docker` builds it locally.

Edit `docker-compose.yml` (password, `REPLICANT_PUBLIC_URL`, and the paths), then:

```bash
docker compose up -d     # http://<host>:8080
```

Volumes are plain `host:container` lines. `/data` holds the catalog. Each drive the server
should scan itself gets its own line under `/media`, read-only:

```yaml
    volumes:
      - /mnt/rock/apps/replicant:/data
      - /mnt/rock/zfsshare/dataset1:/media/dataset1:ro
      - /mnt/rock/zfsshare/dataset2:/media/dataset2:ro
```

Those show up under Settings → Server scans. Vendor tools are not in the image; Linux builds of
`art-cmd` or `REDline` can be mounted at `/opt/replicant-tools`, which is on `PATH`.

**TrueNAS SCALE:** Apps → Discover Apps → Custom App (or "Install via YAML" and paste the compose
file). Image `ghcr.io/fenixstarlord/indexserver:latest`, port 8080, environment `REPLICANT_PASSWORD`
and `REPLICANT_PUBLIC_URL` (the NetBird address of the NAS, e.g. `http://100.64.0.5:8080`), host-path
storage: a dataset such as `/mnt/rock/apps/replicant` at `/data`, and each media dataset at its own
path under `/media`, read-only. The container runs as UID/GID 1000; give the data dataset that owner or set the app's
user accordingly. The container cannot see the host's NetBird interface, hence `REPLICANT_PUBLIC_URL`.

### Standalone app (no server)

`make standalone` builds `dist/Replicant Standalone.app`: the menu bar app with `replicant-server`
bundled. It runs the catalog on this Mac at `http://127.0.0.1:8787` with authentication off
(`REPLICANT_AUTH=open`, loopback only), keeps its data in `~/Library/Application Support/Replicant`, and
connects the scanner to it automatically. There is no password and no API key: click a drive to
scan it, "Open catalog" to browse. Its config file lives in the data folder, so it does not touch
a `~/.config/replicant/config.toml` set up for a remote server.

## Building the stylesheet

The UI uses daisyUI on Tailwind CSS. The compiled stylesheet is committed
and embedded; rebuild it after editing `internal/web/static/src/app.css` or the templates:

```bash
./build-css.sh    # downloads the pinned standalone Tailwind CLI and daisyUI on first run
```

## Layout

```
cmd/replicant, cmd/replicant-server
internal/scan      filesystem walk, skip rules, fingerprinting, volume identity
internal/clips     multi-file clip detection
internal/extract   extractor framework + ffprobe, ale, bwf, sony, braw, red, arri
internal/meta      normalized metadata model and merge
internal/bundle    .replicant bundle format (zip of manifest + jsonl)
internal/store     SQLite schema, migrations, ingest, queries, export
internal/web       HTTP API, auth, templates, static assets
docs/              design spec and decision records
testdata/          golden tool output, sidecars, carved BWF headers
```
