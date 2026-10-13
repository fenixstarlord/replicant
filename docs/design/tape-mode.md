# Tape mode (LTFS)

Status: items 1-3 implemented 2026-10-12 (ADR-003). Items 4-8 are planned. Nothing here has been
timed on a real tape yet; see "Measurements to take".

## Why

The user mounts LTFS tapes on a Mac with myLTO (Imagine Products), which drives a vendor LTFS
stack through macFUSE. An audit on 2026-10-12 found that a default scan fights the tape:

- **Listing is free.** LTFS keeps the whole index in memory after mount, and the walk only does
  readdir/stat/lstat, so it never moves the tape.
- **Every data read can move the tape.** A locate from the beginning of the tape averages 45-65 s
  on LTO-8/9 (IBM: "average space record time from load point"), a stop and reposition costs about
  2 s, and a drive streams 300-400 MB/s. One LTO drive serves one read at a time.
- **The default scan reads in the wrong pattern.** It runs one hash reader per CPU core (8+ on any
  Apple Silicon Mac) and up to 8 extraction workers. It makes two separate passes in two different
  path orders, reads every file at its head and tail, and extractors reopen the media. ffprobe on a
  real ALEXA 35 MXF read from 258 places with 26 jumps over 1 GB, and art-cmd opens each MXF twice.
  The 60 s per-extractor timeout cannot be turned off, and ARRIRAW frames are hashed only to be
  dropped by clip grouping.
- **myLTO's own manual** says tape jobs should not run in parallel ("All at once ... not to an LTO
  tape").

## How tape mode reads a tape (implemented)

- **Detection.** `scan.VolumeInfo` reads the LTFS virtual attribute `ltfs.volumeUUID` on the
  scan root (`user.ltfs.volumeUUID` on Linux). LTFS answers from memory. When the attribute is
  there, the volume is marked `ltfs`, its UUID becomes the drive identity (stable across mounts and
  machines), and `--tape=auto` (the default) switches to tape mode. `--tape` / `--tape=on` forces it
  on any volume and `--tape=off` disables it. Server scans get the same auto-detection.
- **One reader, one pass, tape order** (`internal/scanner/tape.go`). Grouping runs first, since it
  needs names only. Every clip and every file outside a clip becomes one stop, ordered by the
  `ltfs.partition` and `ltfs.startblock` of its earliest file (the order `ltfs_ordered_copy` uses).
  Files without a position (empty files, or a volume without the attributes) go last in path
  order. Within a clip the files are visited in tape order: the extractors that read a file run
  when the tape reaches it (`extract.ClipJob`, with `Input` on the Sony XML and BRAW sidecar
  parsers; the others read the primary file), then the file is hashed. So a sidecar is read where
  it lies, before or after its media, and ffprobe, which ends near the head of a file, is followed
  by a hash that leaves the drive at the end of the file. Extractors that read no file of the clip
  (the ALE index, the first frame of an ARRIRAW sequence whose frames were dropped) run when the
  clip is reached. ARRIRAW frames that grouping drops are never read. Cancellation is checked
  before every file and between extractors.
- **Stream instead of seek** (`internal/scan/tape.go`). Files up to `--tape-stream-mb` (default
  1024) are read front to back in 4 MiB reads; larger files are read at head and tail. The
  fingerprint from a single front-to-back read is byte-identical to the disk-mode value, so catalog
  entries and copy matching are unaffected; with `--full-hash` every file is streamed and the full
  xxh64 comes from the same read. Streaming reads more of each file than disk mode does, so it can
  hit a bad block that disk mode would never touch: the entry then records the read error (damage
  on this copy) and still gets its fingerprint from head and tail when those are readable.
- **Timeouts.** The per-extractor clip timeout defaults to 20 minutes in tape mode (1 minute
  otherwise), and a negative `--clip-timeout` means no limit.
- **Numbers for later.** The manifest records `options.tape`, a `tape` block (files, positioned,
  streamed, head_tail, failed, bytes_read, backward, position/hash/extract milliseconds, stream
  limit, clip timeout, -1 for none) and per-extractor runs, errors, timeouts and seconds. The CLI
  prints a summary of them. `backward` counts reads (hash or extractor) that lie before the previous read on
  tape; it should be zero. The server keeps only `options.tape` and the extractor numbers (in
  `scans.options_json` / `extractors_json`); the `tape` block survives only in an `-o` bundle and
  in the CLI output, so measurements should use `-o`.
- **Long silences.** A tape read can go minutes without progress. The CLI and server scans send a
  keepalive once a minute so the server does not mark the scan "lost" after 5 minutes, and the
  activity reporter throttles alternating extract/hash updates instead of sending two per clip.
  A second Ctrl-C exits at once.

## Plan

1. **Tape mode switch and detection.** Done. See above.
2. **One pass in tape order.** Done. See above.
3. **Stream small files instead of seeking.** Done. The 1 GiB default comes from the reposition
   cost (~2 s plus the locate) against 300-400 MB/s streaming; measure and retune.
4. **Stop ffprobe walking MXF files.** Skip ffprobe on MXF when an ALE row or Sony XML already
   supplied duration (art-cmd's export has none). Otherwise pass `-seekable 0 -nofind_stream_info`,
   which read one contiguous 27 MB at the head of the ALEXA 35 file with timecode and audio intact
   but a wrong duration, so drop DurationS/FrameCount from that run. Longer term, a small Go MXF
   reader that reads the RIP, footer and header partitions once. Also load ALEs in tape order
   before the pass (today the first clip's `Matches` reads them all, a one-time detour), and look at
   art-cmd opening each MXF twice.
5. **Hash after grouping in disk mode too,** so dropped ARRIRAW frames are never read. Tape mode
   already does this. Note the behaviour change: today a file whose hash fails is left out of clip
   grouping.
6. **Reuse the previous scan.** Fetch the latest scan of the same (drive, root) from the server,
   and skip hashing and extraction for files and clips whose size and mtime match, carrying the
   fingerprint and the verbatim raw output over (hard rule 3). An appended tape then reads only
   new files. Do not key this on `ltfs.indexGeneration`: with myLTO's default "write index on
   unmount" it does not change until unmount. Offer `--rehash`.
7. **Import MHL / ASC MHL hashes** into FullHash without reading media. ASC MHL `xxh64` is plain
   XXH64 seed 0 in canonical hex, the same string as FullHash. myLTO's legacy MHL may write
   `<xxhash64>` byte-swapped (Pomfort mhl-tool prints host order); `<xxhash64be>` matches. myLTO
   writes MHL only when MHL output is turned on. LTFS `ltfs.hash.*` attributes (md5/sha only) are a
   second source if present.
8. **App and CLI.**
   - Keep the bundle when an upload fails (today `defer os.Remove` discards hours of tape reading).
   - Show progress during long reads. The non-TTY printer reports hashing only every 5,000 files, so
     the menu bar app shows almost nothing on tape.
   - Do not auto-scan or interval-rescan LTFS volumes from the menu bar app, or pass tape flags. A
     failed or cancelled tape scan currently becomes an interval retry.
   - Fix the `--fast` help text, which says "filesystem only" although fingerprinting still reads
     data.
   - Let the menu bar app pass scan options.

## Measurements to take

On the Mac where myLTO mounts the tape, with myLTO idle and no Finder window open on the tape:

1. **Attributes.** `xattr -p ltfs.volumeUUID <mount>`, `xattr -p ltfs.partition <file>`,
   `xattr -p ltfs.startblock <file>`. Also `ltfs.softwareProduct` and `ltfs.softwareVersion` on the
   mount, and the deck's LTO generation and full/half height. Confirm `xattr -l <mount>` and
   `xattr -l <file>` list no `ltfs.*` names (virtual attributes are never listed). Never read
   `ltfs.driveCaptureDump` or the `ltfs.media*` counters: they drive the tape.
2. **Detection without moving the tape.** `replicant dump --summary --fast --no-fingerprint <mount>`
   should print `fs=ltfs` and the tape's UUID.
3. **Tape layout.** For one card folder, list partition and start block per file and sort. Does
   tape order follow path order? Does anything sit on partition `a`?
4. **Streaming speed.** `time dd if=<file of 10+ GB on tape> of=/dev/null bs=4m`. Compare with the
   drive's native rate and its speed-matching floor (~112-177 MB/s).
5. **Caching.** Read a ~200 MB file twice with `time cat <file> > /dev/null`. Is the second read
   instant?
6. **Seek against stream.** For a 2-4 GB file, time a head read plus a tail read against reading
   the whole file.
7. **Tape-mode scans of one card folder**, with `-o` to a local file, at `--tape-stream-mb` 0,
   1024 and 4096. Keep the console output and `unzip -p <bundle> manifest.json`.
8. **Baseline.** The same folder with `--tape=off`, stopped after a fixed time if needed, to
   measure the improvement.
9. **Extractor cost.** Per-extractor times and timeouts from step 7, and
   `time ffprobe -v error -show_format -show_streams <clip.mxf>` with and without
   `-seekable 0 -nofind_stream_info`.
10. **Identity.** Scan the same tape after a remount and a second tape. They should be one stable
    drive and two separate drives, with fs `ltfs`. Tapes scanned before tape mode were keyed
    `name:<name>|<fstype>` (diskutil has no UUID for a FUSE mount), possibly all in one row; check
    with `sqlite3 <data dir>/replicant.db "select id, volume_uuid, name from drives where volume_uuid like 'name:%'"`
    and clean up by hand. Record what `diskutil info -plist <mount>` returns.
11. **MHL survey.** `find <mount> -name '*.mhl' -o -name ascmhl` (index only), then which hash
    element they use.
12. **Full tape run.** Scan a whole tape in tape mode, record wall time, and compare with the
    `hash_ms` and `extract_ms` totals.

## Open questions

- Does myLTO's bundled LTFS expose `ltfs.startblock` and `ltfs.partition`? The LTFS spec reserves
  them and the reference implementation provides them, but myLTO's docs are silent. myLTOdna uses
  StorageDNA's stack and may differ.
- Does macOS cache reads through myLTO's mount? This decides whether extractors re-reading a file
  just hashed costs tape motion.
- IBM's open RAO (`ltfs.vendor.IBM.rao`, LTO-9 full height) can order reads optimally, but it is a
  setxattr on the mounted volume, a gray area under hard rule 1. Not planned.
