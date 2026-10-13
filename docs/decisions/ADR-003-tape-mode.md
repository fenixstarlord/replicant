# ADR-003: Tape mode for LTFS volumes

## Status
Accepted.

## Date
2026-10-12

## Context
The user indexes LTFS tapes mounted on a Mac by myLTO. On LTFS, listing is free (the index is
in memory) but every data read may move the tape, and one drive serves one read at a time.
The scan pipeline was built for disks: one hash reader per core, a hash pass and then a separate
extraction pass with up to 8 workers, both in path order, and a head-then-tail read per file.
On tape that turns into constant locating. Details and sources: `docs/design/tape-mode.md`.

## Decision
Add a tape mode, a separate code path in `internal/scanner/tape.go`, used when the scan root is on
LTFS (detected by the `ltfs.volumeUUID` virtual attribute) or when `--tape` forces it:

- One reader for everything, and a single pass: group first, then visit each clip and loose file
  once, ordered by `ltfs.partition` and `ltfs.startblock`; files without a position follow in
  path order.
- Within a clip, visit files in tape order and run each extractor when the tape reaches the file
  it reads (`extract.ClipJob`, `extract.Inputter`), then hash the file. The drive ends each file
  at its last byte rather than going back to the head, and sidecars are read where they lie.
- Read files up to 1 GiB (`--tape-stream-mb`) front to back in 4 MiB reads and compute the
  existing fingerprint from that single read. Larger files are read at head and tail.
- A 20-minute default clip timeout in tape mode, and negative timeouts meaning no limit.
- The tape's own UUID becomes the drive identity.
- Record tape statistics and per-extractor timings in the bundle manifest.

The disk path keeps its behaviour. It now runs extraction through the same `ClipJob` steps (same
order, same merged result), shares the timeout semantics, and stops starting extractors once a
scan is cancelled. Both paths send a keepalive so long reads are not marked lost.

## Alternatives Considered
- **Keep one pipeline and set the worker counts to 1 on tape.** This removes interleaving but
  keeps two passes in two different path orders and the head/tail seek per file. Rejected.
- **Import the LTFS index XML instead of reading the mount.** This gives an index-only catalog
  with no tape motion, but myLTO documents no index export, and extraction still needs the data.
  Kept as a possible later addition.
- **IBM open RAO to let the drive order reads.** RAO is LTO-9 full height only, is driven by a
  setxattr on the mounted volume (a gray area under hard rule 1), and is limited to 2,700 files.
  Rejected.
- **A new fingerprint for tape (e.g. head only).** This would break copy matching between tapes
  and disks. Rejected: the existing fingerprint can be computed from a single sequential read.

## Consequences
- Fingerprints and full hashes from tape mode equal the disk-mode values (tested at every size
  boundary), so existing catalogs and "other copies" keep working.
- In tape mode a file whose hash fails stays a clip member (disk mode drops it before grouping),
  and the clip's extractors record their own errors.
- Tape order depends on myLTO's LTFS exposing `ltfs.startblock`; without it, tape mode still
  serialises reads and makes one pass, in path order.
- Streaming can hit a bad block that disk mode (head and tail only) never reads; the entry keeps
  the read error and, when head and tail are readable, its fingerprint.
- Tapes scanned before this change were keyed by name and filesystem type (diskutil has no UUID
  for a FUSE mount); they now get a new drive keyed by the tape UUID, and old rows need a manual
  clean-up.
- Bundles gain optional manifest fields (`options.tape`, `tape`, extractor `runs`, `errors`,
  `timeouts`, `seconds`). Older servers ignore them.
