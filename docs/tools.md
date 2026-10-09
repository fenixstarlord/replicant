# Metadata tools

Replicant reads as much as it can from media files and their sidecars by itself. For some camera
formats the only way to get camera metadata is the manufacturer's own tool. Those tools are not
part of Replicant; install them on the Mac that runs the scans and Replicant will find them.

`replicant doctor` (or the Tools section in the menu bar app's Settings) shows which are available.
A missing tool never stops a scan: the clip is still catalogued with whatever the other sources
provide, and the missing tool is noted on the clip.

| Tool | Status in `replicant doctor` | Formats | What it adds | Where to get it |
|---|---|---|---|---|
| **ffprobe** (part of FFmpeg) | `ffprobe` | MOV, MP4, MXF, MTS, BRAW, Canon CRM, WAV, and most delivery formats | Container, codec, resolution, frame rate, bit depth, duration, embedded timecode, audio channels and rate, creation date | `brew install ffmpeg`, or <https://ffmpeg.org/download.html> |
| **ARRI Reference Tool command line** (`art-cmd`) | `art-cmd` | ARRIRAW (`.ari`, `.arx`, MXF), ARRICORE, ARRI ProRes | Camera model and serial, exposure index, white balance and tint, shutter, sensor mode, lens and focus data, look file, reel, scene and take | Free from ARRI with an ARRI account: <https://www.arri.com/en/learn-help/learn-help-camera-system/tools/arri-reference-tool>. Download the **Command-Line** package (`ARRIReferenceTool_CMD_…_macos_universal_data.zip`), separate from the ART desktop app. See “Installing the ARRI command-line tool” below. |
| **REDline** (part of REDCINE-X PRO) | `redline` | R3D | Camera type and serial, ISO, colour temperature and tint, shutter, lens, focal length, aperture, REDCODE, sensor format, timecode, reel | Free from RED: <https://www.red.com/downloads>. Install REDCINE-X PRO; REDline is inside its install folder. |

Built-in, no install needed: ALE files (ARRI and Avid logs), BWF/iXML audio metadata (Sound
Devices and similar recorders), Sony clip XML (XDROOT and M4ROOT cards), and Blackmagic RAW
`.sidecar` files.

## Installing the ARRI command-line tool

ARRI ships `art-cmd` as a zip (`ARRIReferenceTool_CMD_<version>_macos_universal_data.zip`), not an
installer. Unzipped, it is a portable folder with `bin/art-cmd`, `bin/look-builder`, the `lib/`
folder the tool needs beside it, and `doc/` with the manual.

With the menu bar app:

1. Download the Command-Line package from ARRI (the Download link in Settings opens the page).
2. In Settings, next to the ARRI tool, click **Install…** and choose the download: the unzipped
   folder or the zip itself.
3. The app copies the package to `/Applications/ARRI Reference Tool CMD` (or `~/Applications` if
   that isn't writable), clears macOS's download quarantine so the tool can load its libraries,
   and rechecks. The row turns green with the tool's version.

By hand: unzip, move the folder to `/Applications/ARRI Reference Tool CMD`, then clear the
quarantine flag once with `xattr -dr com.apple.quarantine "/Applications/ARRI Reference Tool CMD"`
(the tool is signed by ARRI but not notarized, so macOS refuses it until then), and run
`replicant doctor`.

Replicant calls it as `art-cmd export --input <clip> --duration 1 --output <temp>/metadata.json`,
which writes the clip's static metadata (plus one frame of dynamic metadata) and nothing else.
ARRIRAW frame sequences are passed as their folder. The tool only ever writes to a temporary
folder on the Mac, never to the drive.

## Installing REDline

Install REDCINE-X PRO from RED's downloads page. REDline comes with it; Replicant looks for it inside
the install folder (see below).

## Where Replicant looks

| Tool | Default locations tried, in order |
|---|---|
| ffprobe | `ffprobe` on the PATH (Homebrew puts it in `/opt/homebrew/bin`) |
| art-cmd | `/Applications/ARRI Reference Tool CMD/bin/art-cmd`, `~/Applications/ARRI Reference Tool CMD/bin/art-cmd`, `/Applications/art-cmd/bin/art-cmd`, `/usr/local/bin/art-cmd`, `/opt/homebrew/bin/art-cmd`, then the PATH |
| REDline | `/Applications/REDCINE-X PRO/REDline`, `/Applications/REDCINE-X PRO.app/Contents/MacOS/REDline`, `/Applications/REDline/REDline`, `/usr/local/bin/REDline`, then the PATH |

If a tool is installed somewhere else, set its path in `~/.config/replicant/config.toml`:

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool CMD/bin/art-cmd"
redline = "/Applications/REDCINE-X PRO/REDline"
```

Then run `replicant doctor` again. The menu bar app reads the same file; use Recheck in its Settings.

## On the server

Scans that the server runs itself (Settings → Server scans) use the tools installed on the server.
A Linux server can have ffprobe (`apt install ffmpeg`); the ARRI and RED tools are macOS and
Windows applications, so ARRI and RED camera metadata comes from scans made on the Mac.

## Status of the vendor extractors

Both vendor extractors are verified against real installations and real clips (2026-10-09):
`art-cmd` 1.0.0 on ALEXA 35 ARRICORE, and REDline (REDCINE-X PRO) on a V-RAPTOR [X] R3D. The raw
tool output is stored on every clip, so a field that a newer camera or tool version names
differently can be remapped later without rescanning.
