package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

type Options struct {
	Level       string
	Destination string
	File        string
}

func New(opts Options) (*slog.Logger, func() error, error) {
	lvl, err := parseLevel(opts.Level)
	if err != nil {
		return nil, nil, err
	}

	dest := strings.ToLower(strings.TrimSpace(opts.Destination))
	if dest == "" {
		dest = "stderr"
	}

	var (
		w      io.Writer
		closer func() error
	)

	switch dest {
	case "stderr":
		w = os.Stderr
	case "none":
		w = io.Discard
	case "file":
		path := opts.File
		if path == "" {
			path = "/var/log/icestream.log"
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
		}
		w = f
		closer = f.Close
	case "syslog":
		w, closer, err = openSyslog()
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unknown logging destination %q", opts.Destination)
	}

	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	cleanup := func() error {
		if closer != nil {
			return closer()
		}
		return nil
	}
	return slog.New(handler), cleanup, nil
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q", level)
	}
}
