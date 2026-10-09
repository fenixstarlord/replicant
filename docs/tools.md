# Third-party tools

Shelf is two static Go binaries, but metadata extraction leans on external tools for the formats
that need them. Every tool is optional: when one is missing, clips that needed it are still
catalogued with whatever the other extractors produced, plus a note of what was unavailable.
Nothing here is required to walk, fingerprint or browse a drive.

This page covers installing the tools on the Mac that runs the `shelf` CLI and on the Linux home
server that runs `shelf-server` scans of its own mounted folders.

## What each tool is for

| Tool | Used for | Source | macOS | Linux server |
|---|---|---|---|---|
| ffprobe (part of FFmpeg) | Container and stream basics for everything: ProRes, DNx, H.264/HEVC, XAVC, MXF timecode and audio, BRAW container, Canon CRM container, WAV | Open source | Homebrew | Distro package |
| `art-cmd` (ARRI Reference Tool CMD) | Camera metadata for ARRICORE, ARRIRAW (MXF, `.ari`, `.arx`) and ARRI ProRes | Free from ARRI, closed source | Download from ARRI | Download from ARRI (RHEL 8 build) |
| `REDline` | Camera metadata for R3D | Free with REDCINE-X PRO, closed source | REDCINE-X PRO installer | Standalone REDline Linux (beta) |

Built in and needing no tool: ALE, Sony clip XML, BRAW `.sidecar`, BWF/iXML. The ARRI ALE on a
card already carries most ALEXA metadata, so ARRI clips are well covered even before `art-cmd` is
installed.

## How Shelf finds a tool

1. An explicit path in `~/.config/shelf/config.toml` under `[tools]` (CLI only, see below).
2. The usual install locations listed in the extractor (`internal/extract/arri`, `internal/extract/red`).
3. `PATH`.

Run `shelf doctor` after installing anything. It lists every extractor, whether its tool was found,
and the version string:

```
EXTRACTOR     STATUS   VERSION
art-cmd       ok       art-cmd 1.0.0
redline       ok       REDline ...
ffprobe       ok       8.0.1 ...
```

The server has no `doctor`; it finds tools on the service's `PATH` only. The extractor status at
the time of each scan is stored with the scan and shown on the scan page.

Scanning is read-only. Tools get a temporary directory on the scanning machine for any output and
are never given a path under the mounted volume. Each tool call has a per-clip timeout, so a bad
file cannot hang a scan.

## macOS (the `shelf` CLI)

### ffprobe

```bash
brew install ffmpeg
```

Homebrew puts `ffprobe` in `/opt/homebrew/bin`, which is on `PATH`. Verify:

```bash
ffprobe -version
```

### ARRI Reference Tool CMD (`art-cmd`)

The ARRI Reference Tool comes in three flavours: Viewer, GUI and CMD. Shelf needs **CMD**. The GUI
app does not contain `art-cmd`, so having "ARRI Reference Tool.app" installed is not enough.

1. Download **ART CMD** for macOS from the [ARRI Reference Tool page](https://www.arri.com/en/learn-help/learn-help-camera-system/tools/arri-reference-tool).
   It is free; the download portal may ask for an ARRI account.
2. Unpack the archive somewhere permanent, for example `/Applications/ARRI Reference Tool CMD/`.
   The package contains `art-cmd`, `look-builder`, a `doc/` folder with the JSON schemas, and
   `EULA.txt`. Read the EULA; it is the licence you accept by using the tool.
3. If macOS refuses to run it because it was downloaded from the internet, clear the quarantine
   flag on the unpacked folder:

   ```bash
   xattr -dr com.apple.quarantine "/Applications/ARRI Reference Tool CMD"
   ```

4. Either put it on `PATH`:

   ```bash
   ln -s "/Applications/ARRI Reference Tool CMD/art-cmd" /usr/local/bin/art-cmd
   ```

   or set `art_cmd` in the config file (below). `/Applications/ARRI Reference Tool CMD/art-cmd` is
   one of the locations Shelf checks on its own.
5. Verify on a real clip. This is the exact call Shelf makes, with a temp directory as the output:

   ```bash
   art-cmd export --input /Volumes/CARD/A_0001C001_260930_084650_c1BY9.mxf --output /tmp/metadata.json
   ```

   `art-cmd export` writes static clip metadata and the dynamic per-frame metadata as one JSON
   document. Giving an explicit `.json` file as `--output` skips the audio tracks and look files that
   a directory output would also produce.

ART CMD 1.0.0 (January 2026) is the first release of the command-line tool. If a future version
changes the export flags, override them without rebuilding Shelf:

```toml
[tools]
art_cmd_args = ["export", "--input", "{input}", "--output", "{outdir}/metadata.json"]
```

`{input}` is the clip path and `{outdir}` is the temp directory Shelf created. The extractor reads
the first `.json` file it finds there.

ARRI does not say whether the macOS build is native on Apple Silicon. If it fails with a "bad CPU
type" error, install Rosetta once:

```bash
softwareupdate --install-rosetta --agree-to-license
```

### REDline (REDCINE-X PRO)

REDline is the command-line half of REDCINE-X PRO. There is no separate macOS download.

1. Download **REDCINE-X PRO for Mac** from [red.com](https://www.red.com/download/redcine-x-pro-mac)
   (a `.pkg`, roughly 200 MB, macOS 10.15 or later). The download page may ask you to sign in.
2. Open the `.pkg`, accept the licence and install. The app lands in
   `/Applications/REDCINE-X Professional/`. RED's installer is not notarised; if Gatekeeper blocks
   it, allow it under System Settings → Privacy & Security → "Open Anyway". A restart is sometimes
   needed before the first launch.
3. REDline lives inside the app bundle at
   `/Applications/REDCINE-X Professional/REDCINE-X PRO.app/Contents/MacOS/REDline`, which Shelf
   checks on its own. The installer also makes `REDline` callable from a terminal when `/usr/sbin`
   is on `PATH`.
4. Verify on a real clip. This is the call Shelf makes:

   ```bash
   REDline --i /Volumes/CARD/A001_C001_0101AB.RDC/A001_C001_0101AB_001.R3D --printMeta 1
   ```

   `--printMeta 1` prints every clip metadata field as `Key: Value` lines; `--printMeta 2` prints
   CSV. Neither needs a licence, a GPU or a display. REDline is case sensitive about flags and
   paths.

RED does not state whether REDCINE-X PRO 65 is native on Apple Silicon. If it turns out to be an
Intel build, macOS offers to install Rosetta on first launch and it runs under that.

### Optional: mediainfo

Not used by Shelf, but useful when checking what a file really contains. It reads the ARRICORE
and R3D containers that ffprobe cannot fully resolve:

```bash
brew install mediainfo
```

## Linux (`shelf-server` scans)

The server runs scans of folders mounted on the home server through the same extractors. It finds
tools on the `PATH` of the systemd service, which by default is
`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`. The simplest arrangement is a
real binary or a wrapper script in `/usr/local/bin`. Restart the service after installing a tool:

```bash
sudo systemctl restart shelf-server
```

Then run a scan from Settings → Server scans and check the scan page for the extractor list.

### ffprobe

Debian and Ubuntu:

```bash
sudo apt install ffmpeg
```

Fedora, Rocky and RHEL (needs the RPM Fusion repository):

```bash
sudo dnf install ffmpeg
```

Distro packages lag; if a format needs a newer FFmpeg, the static builds from
[johnvansickle.com/ffmpeg](https://johnvansickle.com/ffmpeg/) are a single tarball. Copy `ffprobe`
to `/usr/local/bin`.

### ARRI Reference Tool CMD

ARRI ships ART CMD for Linux as a 7-Zip archive built for RHEL 8. It should run on any current
glibc-based distribution; if it reports a missing library, install that library's package.

```bash
sudo apt install p7zip-full          # or: sudo dnf install p7zip
sudo mkdir -p /opt/arri/art-cmd
sudo 7z x -o/opt/arri/art-cmd "ARRI Reference Tool 1.0.0 (CMD) for Linux (RHEL8).7z"
sudo ln -s /opt/arri/art-cmd/art-cmd /usr/local/bin/art-cmd
```

Adjust the first path component if the archive unpacks into a subdirectory. Verify as the service
user, since that is the environment the scan runs in:

```bash
sudo -u shelf art-cmd export --input /mnt/media/CARD/A_0001C001_260930_084650_c1BY9.mxf --output /tmp/metadata.json
```

### REDline

RED provides a standalone **REDline Linux (beta)** download, separate from REDCINE-X PRO:
[red.com/download/redline-linux-beta](https://www.red.com/download/redline-linux-beta). It is a
64-bit Intel build (fine for the amd64 home server) and expects a distribution with Linux Standard
Base 4.0 packages. OpenCL and GPUs are only needed for debayering, not for `--printMeta`.

REDline wants to be launched from its own directory, so install it under `/opt` and put a wrapper
on `PATH` rather than a symlink:

```bash
sudo mkdir -p /opt/REDline
sudo tar -xf REDline_Linux_*.tar.gz -C /opt/REDline --strip-components=1
sudo tee /usr/local/bin/REDline >/dev/null <<'EOF'
#!/bin/sh
cd /opt/REDline && exec ./REDline "$@"
EOF
sudo chmod +x /usr/local/bin/REDline
```

If the archive is a `.zip`, use `unzip` instead. If REDline complains about missing shared
libraries, install your distribution's LSB compatibility package (`lsb-core` on older Debian and
Ubuntu, `redhat-lsb-core` on RHEL-family) or the individual libraries it names. Verify:

```bash
sudo -u shelf REDline --i /mnt/media/CARD/A001_C001_0101AB.RDC/A001_C001_0101AB_001.R3D --printMeta 1
```

## Config file reference (CLI)

`~/.config/shelf/config.toml` (or the file named by `SHELF_CONFIG`):

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool CMD/art-cmd"
art_cmd_args = ["export", "--input", "{input}", "--output", "{outdir}/metadata.json"]
redline = "/Applications/REDCINE-X Professional/REDCINE-X PRO.app/Contents/MacOS/REDline"
```

Every key is optional. A configured path that does not exist disables that extractor rather than
falling back to `PATH`, so remove the key if you uninstall a tool.

## Tools used only for development

These are not needed to run Shelf.

| Tool | Purpose | Install |
|---|---|---|
| Go 1.27 | Build both binaries | `brew install go` |
| Tailwind CSS standalone CLI and daisyUI | Rebuild `internal/web/static/app.css` after template or CSS edits | `./build-css.sh` downloads the pinned versions into `.tools/` on first run; no Node needed |
| ffmpeg | Generate test fixtures with timecode | `brew install ffmpeg` (same package as ffprobe) |
| xxhsum | Cross-check fingerprints in tests | `brew install xxhash` |
| sqlite3 | Ad-hoc inspection of `data/shelf.db` | `brew install sqlite` |
