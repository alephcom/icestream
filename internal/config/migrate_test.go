package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darrenwiebe/icestream/internal/config"
)

const sampleLegacyConf = `# IceGenerator sample configuration file

IP=127.0.0.1
PORT=8000
SERVER=2
MOUNT=/stream.mp3

SOURCE=source
PASSWORD=mypassword

FORMAT=1
MP3PATH=pth:/your_mp3_path;/your_2nd_mp3_path

RECURSIVE=0
LOOP=1
SHUFFLE=1

NAME=Your radio name
GENRE=Your radio genre
DESCRIPTION=Your radio description
URL=https://example.com/radio

BITRATE=128000
PUBLIC=0
METAUPDATE=5

LOG=2
LOGPATH=/var/log/icegenerator.log
`

func TestParseLegacyConf(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(sampleLegacyConf))
	if err != nil {
		t.Fatalf("ParseLegacyConfBytes() error = %v", err)
	}
	if legacy["IP"] != "127.0.0.1" {
		t.Fatalf("IP = %q", legacy["IP"])
	}
	if legacy["MP3PATH"] != "pth:/your_mp3_path;/your_2nd_mp3_path" {
		t.Fatalf("MP3PATH = %q", legacy["MP3PATH"])
	}
}

func TestParseLegacyConfIgnoresComments(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
# comment
IP=10.0.0.1
`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy["IP"] != "10.0.0.1" {
		t.Fatalf("IP = %q", legacy["IP"])
	}
}

func TestMigrateLegacySample(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(sampleLegacyConf))
	if err != nil {
		t.Fatal(err)
	}

	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatalf("MigrateLegacy() error = %v", err)
	}

	cfg := result.Config
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 8000 {
		t.Fatalf("server = %s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	if cfg.Server.Mount != "/stream.mp3" {
		t.Fatalf("mount = %q", cfg.Server.Mount)
	}
	if cfg.Server.Password != "mypassword" {
		t.Fatalf("password = %q", cfg.Server.Password)
	}
	if cfg.Audio.Format != "mp3" || cfg.Audio.Bitrate != 128000 {
		t.Fatalf("audio = %s %d", cfg.Audio.Format, cfg.Audio.Bitrate)
	}
	if len(cfg.Playlist.Paths) != 2 {
		t.Fatalf("paths = %v", cfg.Playlist.Paths)
	}
	if cfg.Playlist.Recursive {
		t.Fatal("expected recursive=false")
	}
	if !cfg.Playlist.Shuffle || !cfg.Playlist.Loop {
		t.Fatal("expected shuffle and loop enabled")
	}
	if cfg.Metadata.UpdateInterval != "5s" {
		t.Fatalf("update_interval = %q", cfg.Metadata.UpdateInterval)
	}
	if cfg.Logging.Level != "info" || cfg.Logging.Destination != "file" {
		t.Fatalf("logging = level %q destination %q file %q", cfg.Logging.Level, cfg.Logging.Destination, cfg.Logging.File)
	}
	if cfg.Logging.File != "/var/log/icegenerator.log" {
		t.Fatalf("log file = %q", cfg.Logging.File)
	}
}

func TestMigrateLegacyOGG(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
SERVER=2
MOUNT=/stream.ogg
PASSWORD=secret
FORMAT=0
MP3PATH=pth:/music
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Audio.Format != "ogg" {
		t.Fatalf("format = %q", result.Config.Audio.Format)
	}
}

func TestMigrateLegacyBitrateKbpsHeuristic(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
MOUNT=/stream.mp3
PASSWORD=secret
MP3PATH=pth:/music
BITRATE=128
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Audio.Bitrate != 128000 {
		t.Fatalf("bitrate = %d, want 128000", result.Config.Audio.Bitrate)
	}
}

func TestMigrateLegacyMetaUpdateZero(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
MOUNT=/stream.mp3
PASSWORD=secret
MP3PATH=pth:/music
METAUPDATE=0
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Metadata.UpdateInterval != "0" {
		t.Fatalf("update_interval = %q", result.Config.Metadata.UpdateInterval)
	}
}

func TestMigrateLegacyUnsupportedServer(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
SERVER=1
MOUNT=/stream.mp3
PASSWORD=secret
MP3PATH=pth:/music
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.MigrateLegacy(legacy); err == nil {
		t.Fatal("expected error for SERVER=1")
	}
}

func TestMigrateLegacyUnsupportedMP3Path(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
MOUNT=/stream.mp3
PASSWORD=secret
MP3PATH=m3u:/playlist.m3u
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.MigrateLegacy(legacy); err == nil {
		t.Fatal("expected error for m3u playlist")
	}
}

func TestMigrateLegacyWarnings(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(`
IP=127.0.0.1
PORT=8000
MOUNT=/stream.mp3
PASSWORD=secret
MP3PATH=pth:/music
DUMPFILE=/tmp/dump.mp3
MDFPATH=/tmp/global.mdf
DATAPORT=8796
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) < 3 {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestMigrateLegacyFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "icegenerator.conf")
	if err := os.WriteFile(legacyPath, []byte(sampleLegacyConf), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.MigrateLegacyFile(legacyPath)
	if err != nil {
		t.Fatalf("MigrateLegacyFile() error = %v", err)
	}

	tomlBytes, err := result.Config.EncodeTOML()
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(dir, "icestream.toml")
	if err := os.WriteFile(outPath, tomlBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(outPath)
	if err != nil {
		t.Fatalf("Load() migrated config error = %v\n%s", err, tomlBytes)
	}
	if loaded.Server.Name != "Your radio name" {
		t.Fatalf("name = %q", loaded.Server.Name)
	}
}

func TestMarshalTOMLContainsSections(t *testing.T) {
	legacy, err := config.ParseLegacyConfBytes([]byte(sampleLegacyConf))
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.MigrateLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	out, err := result.Config.EncodeTOML()
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	for _, section := range []string{"[server]", "[audio]", "[playlist]", "[metadata]", "[logging]"} {
		if !strings.Contains(body, section) {
			t.Fatalf("output missing %s:\n%s", section, body)
		}
	}
}
