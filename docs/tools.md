# Metadata tools

Shelf reads as much as it can from media files and their sidecars by itself. For some camera
formats the only way to get camera metadata is the manufacturer's own tool. Those tools are not
part of Shelf; install them on the Mac that runs the scans and Shelf will find them.

`shelf doctor` (or the Tools section in the menu bar app's Settings) shows which are available.
A missing tool never stops a scan: the clip is still catalogued with whatever the other sources
provide, and the missing tool is noted on the clip.

| Tool | Status in `shelf doctor` | Formats | What it adds | Where to get it |
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
folder the tool needs beside it, and `doc/` with the manual. Keep the folder together.

1. Unzip it and move the folder to `/Applications/ARRI Reference Tool CMD` (or
   `~/Applications/ARRI Reference Tool CMD`). Shelf looks there by default.
2. macOS marks everything a browser downloads with a quarantine flag. The tool is signed by ARRI
   but not notarized, so macOS refuses to load its libraries until the flag is cleared; running
   it shows “libArriImageSdk… Not Opened”. The menu bar app handles this: open Settings, and
   next to the ARRI tool click **Allow it to run**. (From a terminal the equivalent is
   `xattr -dr com.apple.quarantine "/Applications/ARRI Reference Tool CMD"`.)

3. Check with `shelf doctor` (or Recheck in the menu bar app). `art-cmd` should show its version.

Shelf calls it as `art-cmd export --input <clip> --duration 1 --output <temp>/metadata.json`,
which writes the clip's static metadata (plus one frame of dynamic metadata) and nothing else.
ARRIRAW frame sequences are passed as their folder. The tool only ever writes to a temporary
folder on the Mac, never to the drive.

## Installing REDline

Install REDCINE-X PRO from RED's downloads page. REDline comes with it; Shelf looks for it inside
the install folder (see below).

## Where Shelf looks

| Tool | Default locations tried, in order |
|---|---|
| ffprobe | `ffprobe` on the PATH (Homebrew puts it in `/opt/homebrew/bin`) |
| art-cmd | `/Applications/ARRI Reference Tool CMD/bin/art-cmd`, `~/Applications/ARRI Reference Tool CMD/bin/art-cmd`, `/Applications/art-cmd/bin/art-cmd`, `/usr/local/bin/art-cmd`, `/opt/homebrew/bin/art-cmd`, then the PATH |
| REDline | `/Applications/REDCINE-X PRO/REDline`, `/Applications/REDCINE-X PRO.app/Contents/MacOS/REDline`, `/Applications/REDline/REDline`, `/usr/local/bin/REDline`, then the PATH |

If a tool is installed somewhere else, set its path in `~/.config/shelf/config.toml`:

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool CMD/bin/art-cmd"
redline = "/Applications/REDCINE-X PRO/REDline"
```

Then run `shelf doctor` again. The menu bar app reads the same file; use Recheck in its Settings.

## On the server

Scans that the server runs itself (Settings → Server scans) use the tools installed on the server.
A Linux server can have ffprobe (`apt install ffmpeg`); the ARRI and RED tools are macOS and
Windows applications, so ARRI and RED camera metadata comes from scans made on the Mac.

## Status of the vendor extractors

The `art-cmd` and `redline` extractors are written against the tools' documented output but have
not yet been verified against a real installation. The first scan with either tool installed will
show whether their output maps correctly; the raw output is stored on every clip either way, so
nothing is lost if a field needs remapping later.
