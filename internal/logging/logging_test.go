package logging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darrenwiebe/icestream/internal/logging"
)

func TestNewStderr(t *testing.T) {
	logger, cleanup, err := logging.New(logging.Options{
		Level:       "info",
		Destination: "stderr",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if logger == nil {
		t.Fatal("expected logger")
	}
}

func TestNewNone(t *testing.T) {
	logger, cleanup, err := logging.New(logging.Options{
		Level:       "debug",
		Destination: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	logger.Debug("discarded")
}

func TestNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icestream.log")
	logger, cleanup, err := logging.New(logging.Options{
		Level:       "info",
		Destination: "file",
		File:        path,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	logger.Info("hello", "component", "test")
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("log file = %q, want hello", data)
	}
}

func TestNewInvalidDestination(t *testing.T) {
	_, _, err := logging.New(logging.Options{
		Level:       "info",
		Destination: "journald",
	})
	if err == nil {
		t.Fatal("expected error for invalid destination")
	}
}

func TestNewInvalidLevel(t *testing.T) {
	_, _, err := logging.New(logging.Options{
		Level:       "trace",
		Destination: "stderr",
	})
	if err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestNewSyslog(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("syslog may be unavailable in CI")
	}
	logger, cleanup, err := logging.New(logging.Options{
		Level:       "info",
		Destination: "syslog",
	})
	if err != nil {
		t.Skipf("syslog unavailable: %v", err)
	}
	defer cleanup()

	logger.Info("syslog test")
}
