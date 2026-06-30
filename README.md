# icestream

Modern Go replacement for [IceGenerator](https://sourceforge.net/projects/icegenerator/): a lightweight daemon that streams pre-encoded MP3 or OGG files to an **Icecast 2** server without transcoding.

## Features (MVP)

- Directory-based playlists with optional recursive scan
- MP3 and OGG support (one format per config)
- Real-time stream pacing via configured bitrate (libshout-equivalent)
- Automatic Icecast reconnect with exponential backoff
- Missing files skipped at runtime with optional backoff
- Shuffle with no repeat until the playlist is exhausted, or alphabetical order
- Optional playlist loop
- Icecast 2 HTTP source (pure Go, no CGO)
- In-process fanout to multiple Icecast destinations (`[[server.destinations]]`)
- Now Playing metadata from ID3/Vorbis tags (falls back to filename)
- Graceful shutdown: finishes the current track on `SIGINT`/`SIGTERM`
- TOML configuration with startup validation

## Requirements

- Go 1.22+ (to build)
- Icecast 2 server
- Pre-encoded audio files at a fixed format/bitrate

## Build

```bash
go build -o icestream ./cmd/icestream
```

## Quick start

1. Copy and edit the example config:

```bash
cp configs/example.toml /tmp/icestream.toml
# Set server.password, playlist.paths, and server.mount
```

2. Start Icecast 2 (example with Docker):

```bash
docker run --rm -p 8000:8000 moul/icecast2
```

Default Icecast credentials in many images: source password `hackme`, admin password `hackme`.

3. Validate config and playlist:

```bash
./icestream validate-config -c /tmp/icestream.toml
```

4. Start streaming:

```bash
./icestream serve -c /tmp/icestream.toml
```

5. Listen at `http://127.0.0.1:8000/stream.mp3` (match your mount point).

## Configuration

See [configs/example.toml](configs/example.toml). Key sections:

| Section | Purpose |
|---------|---------|
| `[server]` | Icecast defaults, reconnect settings, optional `[[server.destinations]]` fanout table (or single `mount`) |
| `[audio]` | `mp3` or `ogg`, plus `bitrate` (bits/sec) for stream pacing |
| `[playlist]` | Directories, recursive scan, shuffle, loop, missing-file backoff |
| `[metadata]` | `update_interval` (e.g. `"5s"`); `"0"` sends title only on track change |
| `[logging]` | Level (`debug`–`error`); destination `stderr` (default), `file`, `syslog`, or `none`; optional `file` path for file logging |

### Source authentication

Set `server.username` and `server.password` to match the source credentials for your mount(s) in Icecast. The default username is `source` (Icecast's usual value).

### Multiple destinations (in-process fanout)

Stream the same encoded audio to several Icecast mounts or servers from **one** icestream process (one playlist, one disk read, one pacer). Define targets with `[[server.destinations]]` under `[server]`. When the table is omitted, icestream uses the legacy single-destination fields (`server.mount`, etc.).

`[server]` holds shared defaults (host, port, username, password, stream metadata) and global reconnect settings. Each `[[server.destinations]]` entry requires `mount`; other fields inherit from `[server]` when omitted. Optional `name` labels destinations in logs (defaults to `host:port/mount`).

If one destination drops mid-track, healthy destinations keep the current track. The failed destination reconnects in the background and rejoins at the **next track boundary** (it misses the rest of the current track). If **all** destinations fail, behavior matches single-destination mode: reconnect with backoff or exit when reconnect is disabled.

When every mount lives on Icecast you control, [Icecast relay](https://icecast.org/docs/icecast-trunk/relay/) is still a zero-config alternative; use in-process fanout when you need separate hosts, passwords, or mounts pushed from one playlist.

### Stream pacing

Set `audio.bitrate` to the nominal bits-per-second of your encoded files (e.g. `128000` for 128 kbps). The value is sent to Icecast as the `Ice-Bitrate` header and checked against each MP3 file (a warning is logged when they differ by more than ~10%).

For **MP3**, icestream strips ID3 tags, emits only complete MPEG frames, and paces playback from each frame's header timing (similar to legacy IceGenerator's `shout_sync()`). For **OGG**, output is throttled with the configured `audio.bitrate`.

### Metadata updates

Track titles are read from tags when available. Updates are sent via Icecast's `/admin/metadata` endpoint. Set `metadata.admin_username` (default `admin`) and `metadata.admin_password` (defaults to `server.password` when omitted). Use your Icecast `<admin-password>` for `metadata.admin_password`, not the mount source password.

### Reconnect

If **all** destinations lose their connection mid-stream, icestream reconnects with exponential backoff (`reconnect_initial_delay` → `reconnect_max_delay`). After a successful reconnect, the current track is skipped and the next playlist entry plays. Set `reconnect = false` to exit on disconnect instead.

### Missing files

If a playlist file cannot be opened at playback time (deleted after startup, permission error, etc.), icestream logs a warning and continues with the next track. Set `missing_file_backoff` (e.g. `"2s"`) to pause between consecutive missing-file skips and avoid thrashing.

### OGG mount suffix

Many players require the mount point to end in `.ogg` for OGG streams (e.g. `/stream.ogg`). MP3 mounts typically use `.mp3` (e.g. `/stream.mp3`). This matches the legacy IceGenerator behavior.

## CLI

```bash
icestream serve -c config.toml      # Start streaming (foreground)
icestream validate-config -c config.toml
icestream migrate-config -i /etc/icegenerator.conf -o /etc/icestream/config.toml
icestream version
```

### Migrating from IceGenerator

Convert a legacy `icegenerator.conf` to icestream TOML:

```bash
icestream migrate-config -i /etc/icegenerator.conf -o /etc/icestream/config.toml
```

Supported mappings: Icecast 2 (`SERVER=2`), directory playlists (`MP3PATH=pth:...`), MP3/OGG format, stream metadata, shuffle/loop/recursive flags, `METAUPDATE`, and logging (`LOG`/`LOGPATH` → `logging.destination`). Unsupported options (`DUMPFILE`, `MDFPATH`, `DATAPORT`, SQL/M3U/PLS playlists, Shoutcast/Icecast 1) produce errors or warnings. Legacy `BITRATE` is mapped to `audio.bitrate` (values under 1000 are treated as kbps).

## systemd

Install the binary to `/usr/local/bin/icestream`, config to `/etc/icestream/config.toml`, and use [deploy/icestream.service](deploy/icestream.service).

## Integration test (manual)

With Icecast running locally:

```bash
# Terminal 1
docker run --rm -p 8000:8000 moul/icecast2

# Terminal 2 — create test media and config
mkdir -p /tmp/music
# Add at least one .mp3 file to /tmp/music
cat > /tmp/test.toml <<'EOF'
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
username = "source"
password = "hackme"
name = "Test"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/tmp/music"]
recursive = false
shuffle = false
loop = true

[metadata]
update_interval = "0"

[logging]
level = "info"
EOF

go run ./cmd/icestream serve -c /tmp/test.toml
```

Verify with `curl -I http://127.0.0.1:8000/stream.mp3` or a media player.

Graceful shutdown: send `SIGTERM` and confirm the log shows the current track completes before exit.

## Architecture

```
selector goroutine  →  player goroutine  →  Icecast HTTP PUT
                    ↘  metadata goroutine →  /admin/metadata
```

Channels replace the legacy pthread/semaphore model from IceGenerator.

## Deferred to v2

- M3U/PLS/SQL playlist sources
- HTTP control API (replaces legacy telnet)
- Config hot-reload (watch config file or `SIGHUP`; apply safe changes at track boundaries without restart)
- IceMetaL `.mdf` metadata DSL
- Shoutcast ICY protocol
- Stream dump file
- Docker Compose, Prometheus metrics

## License

MIT — see [LICENSE](LICENSE).

This is a clean-room rewrite of the legacy [IceGenerator](https://sourceforge.net/projects/icegenerator/) project, which remains under GPL-2.
