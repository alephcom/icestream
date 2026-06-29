package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
password = "secret"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 8000 {
		t.Fatalf("port = %d, want 8000", cfg.Server.Port)
	}
	if cfg.Audio.Bitrate != 128000 {
		t.Fatalf("bitrate = %d, want 128000", cfg.Audio.Bitrate)
	}
	if cfg.ContentType() != "audio/mpeg" {
		t.Fatalf("content type = %q", cfg.ContentType())
	}
	if cfg.Server.Username != "source" {
		t.Fatalf("username = %q, want source", cfg.Server.Username)
	}
}

func TestLoadCustomSourceUsername(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/jazz.mp3"
username = "jazz_source"
password = "jazz-secret"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Username != "jazz_source" {
		t.Fatalf("username = %q, want jazz_source", cfg.Server.Username)
	}
	if cfg.Server.Mount != "/jazz.mp3" {
		t.Fatalf("mount = %q", cfg.Server.Mount)
	}
}

func TestLoadInvalidBitrate(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
password = "secret"

[audio]
format = "mp3"
bitrate = 0

[playlist]
paths = ["/music"]
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for invalid bitrate")
	}
}

func TestLoadReconnectSettings(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
password = "secret"
reconnect = false
reconnect_initial_delay = "2s"
reconnect_max_delay = "30s"
reconnect_max_attempts = 5

[audio]
format = "mp3"
bitrate = 64000

[playlist]
paths = ["/music"]
missing_file_backoff = "1s"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Reconnect {
		t.Fatal("expected reconnect disabled")
	}
	if cfg.ReconnectInitialDelay() != 2*time.Second {
		t.Fatalf("initial delay = %v", cfg.ReconnectInitialDelay())
	}
	if cfg.MissingFileBackoff() != time.Second {
		t.Fatalf("missing file backoff = %v", cfg.MissingFileBackoff())
	}
}

func TestLoadInvalidFormat(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream"
password = "secret"

[audio]
format = "flac"

[playlist]
paths = ["/music"]
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestLoadInvalidUpdateInterval(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream"
password = "secret"

[audio]
format = "mp3"

[playlist]
paths = ["/music"]

[metadata]
update_interval = "not-a-duration"
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for invalid update interval")
	}
}

func TestLoadLoggingDestination(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
password = "secret"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]

[logging]
level = "warn"
destination = "file"
file = "/var/log/icestream.log"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Logging.Level != "warn" {
		t.Fatalf("level = %q, want warn", cfg.Logging.Level)
	}
	if cfg.Logging.Destination != "file" {
		t.Fatalf("destination = %q, want file", cfg.Logging.Destination)
	}
	if cfg.Logging.File != "/var/log/icestream.log" {
		t.Fatalf("file = %q", cfg.Logging.File)
	}
}

func TestLoadInvalidLoggingDestination(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
mount = "/stream.mp3"
password = "secret"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]

[logging]
destination = "journald"
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for invalid logging destination")
	}
}

func TestLoadMissingMount(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
password = "secret"

[audio]
format = "mp3"

[playlist]
paths = ["/music"]
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for missing mount")
	}
}

func TestLoadMultiDestinations(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
password = "shared-secret"
name = "Default Name"

[[server.destinations]]
mount = "/primary.mp3"
name = "primary"

[[server.destinations]]
host = "backup.example.com"
port = 9000
mount = "/mirror.mp3"
password = "backup-secret"
name = "backup"

[playlist]
paths = ["/music"]
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	dests := cfg.Destinations()
	if len(dests) != 2 {
		t.Fatalf("destinations = %d, want 2", len(dests))
	}
	if dests[0].Label != "primary" {
		t.Fatalf("label[0] = %q", dests[0].Label)
	}
	if dests[0].ServerURL != "http://127.0.0.1:8000" {
		t.Fatalf("url[0] = %q", dests[0].ServerURL)
	}
	if dests[0].Password != "shared-secret" {
		t.Fatalf("password[0] = %q", dests[0].Password)
	}
	if dests[0].Name != "primary" {
		t.Fatalf("name[0] = %q", dests[0].Name)
	}
	if dests[1].ServerURL != "http://backup.example.com:9000" {
		t.Fatalf("url[1] = %q", dests[1].ServerURL)
	}
	if dests[1].Password != "backup-secret" {
		t.Fatalf("password[1] = %q", dests[1].Password)
	}
}

func TestLoadDestinationMissingMount(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000
password = "secret"

[[server.destinations]]
name = "bad"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error for destination missing mount")
	}
}

func TestLoadDestinationMissingPassword(t *testing.T) {
	path := writeConfig(t, `
[server]
host = "127.0.0.1"
port = 8000

[[server.destinations]]
mount = "/stream.mp3"

[audio]
format = "mp3"
bitrate = 128000

[playlist]
paths = ["/music"]
`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("expected error when no password available")
	}
}
