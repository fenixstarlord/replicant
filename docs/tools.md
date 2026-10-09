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
| **ARRI Reference Tool command line** (`art-cmd`) | `art-cmd` | ARRIRAW (`.ari`, `.arx`, MXF), ARRICORE, ARRI ProRes | Camera model and serial, exposure index, white balance and tint, shutter, sensor mode, lens and focus data, look file, reel, scene and take | Free from ARRI with an ARRI account: <https://www.arri.com/en/learn-help/learn-help-camera-system/tools/arri-reference-tool>. Install the ARRI Reference Tool package, which includes the command-line tool. |
| **REDline** (part of REDCINE-X PRO) | `redline` | R3D | Camera type and serial, ISO, colour temperature and tint, shutter, lens, focal length, aperture, REDCODE, sensor format, timecode, reel | Free from RED: <https://www.red.com/downloads>. Install REDCINE-X PRO; REDline is inside its install folder. |

Built-in, no install needed: ALE files (ARRI and Avid logs), BWF/iXML audio metadata (Sound
Devices and similar recorders), Sony clip XML (XDROOT and M4ROOT cards), and Blackmagic RAW
`.sidecar` files.

## Where Shelf looks

| Tool | Default locations tried, in order |
|---|---|
| ffprobe | `ffprobe` on the PATH (Homebrew puts it in `/opt/homebrew/bin`) |
| art-cmd | `/Applications/ARRI Reference Tool.app/Contents/MacOS/art-cmd`, `/Applications/ARRI/ARRI Reference Tool/art-cmd`, `/usr/local/bin/art-cmd`, `/opt/homebrew/bin/art-cmd`, then the PATH |
| REDline | `/Applications/REDCINE-X PRO/REDline`, `/Applications/REDCINE-X PRO.app/Contents/MacOS/REDline`, `/Applications/REDline/REDline`, `/usr/local/bin/REDline`, then the PATH |

If a tool is installed somewhere else, set its path in `~/.config/shelf/config.toml`:

```toml
[tools]
ffprobe = "/opt/homebrew/bin/ffprobe"
art_cmd = "/Applications/ARRI Reference Tool.app/Contents/MacOS/art-cmd"
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
