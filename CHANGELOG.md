# Changelog

All notable changes to icestream are documented in this file.

## [0.0.4] - 2026-06-30

### Fixed

- **Icecast admin load** — Default `metadata.update_interval` to `"0"` (send Now Playing only on track change) instead of every 5 seconds. With multi-destination fanout, the old default could push hundreds of `/admin/metadata` requests per minute to Icecast.

### Changed

- **Metadata deduplication** — When `update_interval` is set, skip admin metadata updates if the title has not changed since the last successful send.

## [0.0.3] - 2026-06-30

### Fixed

- **Icecast source PUT chunked encoding** — Stream to Icecast over raw HTTP/1.0 TCP with an identity body instead of Go `net/http` chunked PUT. Icecast stores chunk size lines (e.g. `68\r\n`) verbatim in live mounts when sources use chunked transfer encoding, which broke browser and ffplay playback.

## [0.0.2] - 2026-06-29

### Fixed

- **MP3 browser playback** — Stream frame-aligned MP3 (skip ID3 tags, never split frames across writes) so browser `<audio>` decoders can sync to the live mount.
- **MP3 pacing** — Pace MP3 output from each frame's decoded duration instead of the configured bitrate alone; log a warning when detected frame bitrate differs from `audio.bitrate` by more than ~10%.
- **Metadata 401** — Use Icecast admin credentials (`metadata.admin_username` / `metadata.admin_password`) for `/admin/metadata` instead of the mount source password.
- **ID3 lead-in reader** — Fix infinite re-read of the first 10 bytes on files without an ID3v2 tag.

### Added

- `internal/mp3` package: ID3 skip, MPEG frame header parsing, and `FrameReader`.
- `FramePacer` for MP3 tracks; OGG continues to use `BitratePacer`.

## [0.0.1] - 2026-06-28

Initial release — Go replacement for IceGenerator with directory playlists, MP3/OGG streaming, multi-destination fanout, reconnect, metadata, and legacy config migration.
