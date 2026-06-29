package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
	"github.com/darrenwiebe/icestream/internal/engine"
	"github.com/darrenwiebe/icestream/internal/playlist"
	"github.com/darrenwiebe/icestream/internal/source"
)

type mockStreamer struct {
	mu            sync.Mutex
	writes        int
	metadata      []string
	connected     bool
	failWriteOnce bool
	failWrite     bool
	reconnectErr  error
	reconnects    int
}

func (m *mockStreamer) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
	return nil
}

func (m *mockStreamer) BeginTrack() {}

func (m *mockStreamer) Reconnect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnects++
	if m.reconnectErr != nil {
		return m.reconnectErr
	}
	m.connected = true
	m.failWriteOnce = false
	m.failWrite = false
	return nil
}

func (m *mockStreamer) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWrite || m.failWriteOnce {
		if m.failWriteOnce {
			m.failWriteOnce = false
		}
		return 0, errors.New("broken pipe")
	}
	m.writes++
	return len(p), nil
}

func (m *mockStreamer) SetMetadata(title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metadata = append(m.metadata, title)
	return nil
}

func (m *mockStreamer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	return nil
}

func testConfig(dir string, loop bool) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:                  "127.0.0.1",
			Port:                  8000,
			Mount:                 "/stream.mp3",
			Password:              "secret",
			Reconnect:             true,
			ReconnectInitialDelay: "10ms",
			ReconnectMaxDelay:     "50ms",
			ReconnectMaxAttempts:  3,
		},
		Audio: config.AudioConfig{Format: "mp3", Bitrate: 100_000_000},
		Playlist: config.PlaylistConfig{
			Paths:              []string{dir},
			Recursive:          false,
			Shuffle:            false,
			Loop:               loop,
			MissingFileBackoff: "0",
		},
		Metadata: config.MetadataConfig{UpdateInterval: "0"},
		Logging:  config.LoggingConfig{Level: "error"},
	}
}

func TestEnginePlaysTracksWithoutLoop(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1234567890"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := testConfig(dir, false)

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mock := &mockStreamer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- eng.Run(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("engine did not finish in time")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.writes < 2 {
		t.Fatalf("writes = %d, want at least 2", mock.writes)
	}
}

func TestEngineGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.mp3")
	data := make([]byte, 256*1024)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(dir, true)

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mock := &mockStreamer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- eng.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("engine did not shut down gracefully")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.writes == 0 {
		t.Fatal("expected at least one write before shutdown")
	}
}

func TestEngineSkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.mp3", "two.mp3", "three.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := testConfig(dir, false)

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	missingPath := filepath.Join(dir, "two.mp3")
	if err := os.Remove(missingPath); err != nil {
		t.Fatal(err)
	}

	mock := &mockStreamer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- eng.Run(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("engine did not finish in time")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.writes < 2 {
		t.Fatalf("writes = %d, want at least 2 (skipped missing file)", mock.writes)
	}
}

func TestEngineReconnectSkipsTrack(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1234567890"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := testConfig(dir, false)

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mock := &mockStreamer{failWriteOnce: true}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- eng.Run(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("engine did not finish in time")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", mock.reconnects)
	}
	if mock.writes < 1 {
		t.Fatalf("writes = %d, want at least 1", mock.writes)
	}
}

func TestEngineReconnectFailureExits(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.mp3"), []byte("1234567890"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(dir, false)
	cfg.Server.ReconnectMaxAttempts = 2

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mock := &mockStreamer{
		failWrite:    true,
		reconnectErr: errors.New("connection refused"),
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = eng.Run(ctx)
	if err == nil {
		t.Fatal("expected error when reconnect fails")
	}
}

func TestEngineMissingFileBackoff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.mp3"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(dir, false)
	cfg.Playlist.MissingFileBackoff = "200ms"

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add phantom tracks to iterator by appending paths that don't exist
	tracks = append(tracks,
		playlist.Track{Path: filepath.Join(dir, "missing1.mp3")},
		playlist.Track{Path: filepath.Join(dir, "missing2.mp3")},
		playlist.Track{Path: filepath.Join(dir, "one.mp3")},
	)

	mock := &mockStreamer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, mock, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- eng.Run(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("engine did not finish in time")
	}

	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("elapsed = %v, expected at least 200ms backoff", elapsed)
	}
}

type flakySubStreamer struct {
	mu       sync.Mutex
	writes   int
	suspended bool
}

func (f *flakySubStreamer) Connect(ctx context.Context) error { return nil }
func (f *flakySubStreamer) Reconnect(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspended = false
	return nil
}
func (f *flakySubStreamer) BeginTrack() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspended = false
}
func (f *flakySubStreamer) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.suspended {
		return 0, errors.New("broken pipe")
	}
	f.writes++
	f.suspended = true
	return len(p), nil
}
func (f *flakySubStreamer) SetMetadata(string) error { return nil }
func (f *flakySubStreamer) Close() error             { return nil }

type dualStreamer struct {
	healthy *mockStreamer
	flaky   *flakySubStreamer
}

func (d *dualStreamer) Connect(ctx context.Context) error {
	if err := d.healthy.Connect(ctx); err != nil {
		return err
	}
	return d.flaky.Connect(ctx)
}
func (d *dualStreamer) Reconnect(ctx context.Context) error {
	_ = d.healthy.Reconnect(ctx)
	return d.flaky.Reconnect(ctx)
}
func (d *dualStreamer) BeginTrack() {
	d.healthy.BeginTrack()
	d.flaky.BeginTrack()
}
func (d *dualStreamer) Write(p []byte) (int, error) {
	if _, err := d.healthy.Write(p); err != nil {
		return 0, err
	}
	if _, err := d.flaky.Write(p); err != nil {
		if source.IsDisconnectError(err) {
			return len(p), nil
		}
		return 0, err
	}
	return len(p), nil
}
func (d *dualStreamer) SetMetadata(title string) error {
	_ = d.healthy.SetMetadata(title)
	return d.flaky.SetMetadata(title)
}
func (d *dualStreamer) Close() error {
	_ = d.healthy.Close()
	return d.flaky.Close()
}

func TestEnginePartialDestinationFailureCompletesTrack(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1234567890"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := testConfig(dir, false)

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		t.Fatal(err)
	}

	streamer := &dualStreamer{healthy: &mockStreamer{}, flaky: &flakySubStreamer{}}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	eng := engine.New(cfg, tracks, streamer, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := eng.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	streamer.healthy.mu.Lock()
	healthyWrites := streamer.healthy.writes
	streamer.healthy.mu.Unlock()
	streamer.flaky.mu.Lock()
	flakyWrites := streamer.flaky.writes
	streamer.flaky.mu.Unlock()

	if healthyWrites < 2 {
		t.Fatalf("healthy writes = %d, want at least 2", healthyWrites)
	}
	if flakyWrites < 2 {
		t.Fatalf("flaky writes = %d, want at least 2 (resumed after BeginTrack)", flakyWrites)
	}
}

