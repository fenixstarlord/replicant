# ADR-004: Shared per-volume scan profiles

## Status
Accepted.

## Date
2026-10-16

## Context
Different Macs must use the same extraction choices for a drive. Tape scanning in particular
needs independent control over vendor tools, sidecar parsers and fingerprinting. The local
“already named” flag did not distinguish a configured drive from one successfully indexed.

## Decision
- Store profiles in `drive_profiles`, keyed by the exact `scan.Volume.Key()` used at ingest.
  LTFS uses its tape UUID; other volumes use their UUID or the existing name/filesystem fallback.
- Profiles exist independently of drive rows, allowing setup before a first scan without
  making an unscanned drive appear in the catalog. The indexed indicator requires a stored scan.
- Keep catalog name/client/group authoritative in `drives` after ingest. Profile reads overlay
  those fields; profile saves update them in one transaction. First ingest applies a pending
  profile's current fields in the ingest transaction. Bundle names cannot override a profile.
- An explicit extractor allowlist is applied before availability checks and ALE preparation.
  Legacy nil selects all; an empty list selects none. Record selections in bundle options.
- Use authenticated JSON profile endpoints and CLI commands; the Swift app continues using the
  bundled CLI rather than managing credentials itself. Cache by server plus volume key.
  Profile revisions reject conflicting edits from different clients.
- Resolve settings at scan start after volume identification. The app passes an expected key,
  so a replacement drive at a queued mount path is rejected. Active scans retain their options.
- Use a SwiftUI menu-bar popover for independently clickable scan and gear controls. First-time
  manual/Browse/automatic scans share one setup flow, replacing the local naming-only dialog.

## Consequences
- Both server and client need updating. Existing unconfigured drives default to all extractors
  and fingerprinting.
- Offline cached profiles can be used but cannot be edited; upload still requires the server.
- UUID-less volumes retain the existing identity limitations of name/filesystem fallback.
- Taxonomy entries use foreign keys with `ON DELETE SET NULL`, and profile/name/taxonomy updates
  are transactional. Scans never modify shared extraction settings at ingest.
