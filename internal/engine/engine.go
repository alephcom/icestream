package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
	"github.com/darrenwiebe/icestream/internal/metadata"
	"github.com/darrenwiebe/icestream/internal/mp3"
	"github.com/darrenwiebe/icestream/internal/pacing"
	"github.com/darrenwiebe/icestream/internal/playlist"
	"github.com/darrenwiebe/icestream/internal/source"
)

const readBufferSize = 16 * 1024

var ErrTrackInterrupted = errors.New("track interrupted by disconnect")

type Streamer interface {
	Connect(ctx context.Context) error
	Reconnect(ctx context.Context) error
	BeginTrack()
	Write(p []byte) (int, error)
	SetMetadata(title string) error
	Close() error
}

type Engine struct {
	cfg        *config.Config
	tracks     []playlist.Track
	streamer   Streamer
	bitratePacer pacing.Pacer
	framePacer   *pacing.FramePacer
	logger     *slog.Logger
}

func New(cfg *config.Config, tracks []playlist.Track, streamer Streamer, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		cfg:      cfg,
		tracks:   tracks,
		streamer: streamer,
		logger:   logger,
	}
	if cfg.Audio.Format == "mp3" {
		e.framePacer = pacing.NewFramePacer()
	} else {
		e.bitratePacer = pacing.NewBitratePacer(cfg.Audio.Bitrate)
	}
	return e
}

func NewDefault(cfg *config.Config, tracks []playlist.Track, logger *slog.Logger) *Engine {
	reconnect := source.ReconnectSettings{
		Enabled:      cfg.Server.Reconnect,
		InitialDelay: cfg.ReconnectInitialDelay(),
		MaxDelay:     cfg.ReconnectMaxDelay(),
		MaxAttempts:  cfg.Server.ReconnectMaxAttempts,
	}
	meta := cfg.MetadataAdmin()
	streamer := source.NewMulti(cfg.Destinations(), cfg.Audio.Format, cfg.Audio.Bitrate, meta, reconnect, logger)
	return New(cfg, tracks, streamer, logger)
}

func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := e.streamer.Connect(runCtx); err != nil {
		return fmt.Errorf("connect to icecast: %w", err)
	}
	defer e.streamer.Close()

	dests := e.cfg.Destinations()
	if len(dests) == 1 {
		e.logger.Info("connected to stream server",
			"url", dests[0].ServerURL,
			"mount", dests[0].Mount,
		)
	} else {
		labels := make([]string, len(dests))
		for i, d := range dests {
			labels[i] = d.Label
		}
		e.logger.Info("connected to stream servers",
			"destinations", len(dests),
			"labels", labels,
		)
	}

	tracks := make(chan playlist.Track)
	trackDone := make(chan struct{}, 1)
	currentTitle := make(chan string, 1)

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(tracks)
		e.runSelector(runCtx, tracks, trackDone, errCh)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(currentTitle)
		e.runPlayer(runCtx, tracks, trackDone, currentTitle, errCh, cancel)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		e.runMetadata(runCtx, currentTitle, errCh)
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	e.logger.Info("stream stopped")
	return nil
}

func (e *Engine) runSelector(ctx context.Context, tracks chan<- playlist.Track, trackDone <-chan struct{}, errCh chan<- error) {
	it := playlist.NewIterator(e.tracks, playlist.Options{
		Paths:     e.cfg.Playlist.Paths,
		Recursive: e.cfg.Playlist.Recursive,
		Shuffle:   e.cfg.Playlist.Shuffle,
		Loop:      e.cfg.Playlist.Loop,
		Extension: e.cfg.FileExtension(),
	})

	for {
		if ctx.Err() != nil {
			return
		}

		track, ok := it.Next()
		if !ok {
			return
		}

		select {
		case tracks <- track:
			e.logger.Info("queued track", "path", track.Path)
		case <-ctx.Done():
			return
		}

		select {
		case <-trackDone:
		case <-ctx.Done():
			select {
			case <-trackDone:
			case <-time.After(24 * time.Hour):
				errCh <- fmt.Errorf("timed out waiting for current track to finish")
			}
			return
		}
	}
}

func (e *Engine) runPlayer(ctx context.Context, tracks <-chan playlist.Track, trackDone chan<- struct{}, currentTitle chan<- string, errCh chan<- error, cancel context.CancelFunc) {
	consecutiveMissing := 0

	for {
		select {
		case <-ctx.Done():
			return
		case track, ok := <-tracks:
			if !ok {
				return
			}

			title := metadata.TitleForPath(track.Path)
			select {
			case currentTitle <- title:
			case <-ctx.Done():
				return
			}

			e.logger.Info("now playing", "title", title, "path", track.Path)

			e.streamer.BeginTrack()
			err := e.streamTrack(ctx, track.Path)
			if err != nil {
				if errors.Is(err, ErrTrackInterrupted) {
					e.logger.Warn("track interrupted by disconnect, skipping", "path", track.Path)
					consecutiveMissing = 0
					trackDone <- struct{}{}
					continue
				}
				if isOpenError(err) {
					e.logger.Warn("file unavailable, skipping", "path", track.Path, "error", err)
					consecutiveMissing++
					if backoff := e.cfg.MissingFileBackoff(); backoff > 0 && consecutiveMissing > 1 {
						select {
						case <-ctx.Done():
							return
						case <-time.After(backoff):
						}
					}
					trackDone <- struct{}{}
					continue
				}
				errCh <- fmt.Errorf("stream %q: %w", track.Path, err)
				cancel()
				select {
				case trackDone <- struct{}{}:
				default:
				}
				return
			}

			consecutiveMissing = 0
			trackDone <- struct{}{}
		}
	}
}

func (e *Engine) runMetadata(ctx context.Context, currentTitle <-chan string, errCh chan<- error) {
	interval := e.cfg.MetadataUpdateInterval()
	var title string
	var ticker *time.Ticker
	var tickCh <-chan time.Time

	sendMetadata := func() {
		if title == "" {
			return
		}
		if err := e.streamer.SetMetadata(title); err != nil {
			e.logger.Warn("metadata update failed", "error", err, "title", title)
		}
	}

	for {
		select {
		case <-ctx.Done():
			if ticker != nil {
				ticker.Stop()
			}
			return
		case t, ok := <-currentTitle:
			if !ok {
				return
			}
			title = t
			if interval > 0 {
				if ticker != nil {
					ticker.Stop()
				}
				ticker = time.NewTicker(interval)
				tickCh = ticker.C
			} else {
				tickCh = nil
			}
			sendMetadata()
		case <-tickCh:
			sendMetadata()
		}
	}
}

func (e *Engine) streamTrack(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if e.cfg.Audio.Format == "mp3" {
		return e.streamMP3Track(ctx, path, f)
	}
	return e.streamRawTrack(ctx, path, f)
}

func (e *Engine) streamRawTrack(ctx context.Context, path string, f *os.File) error {
	e.bitratePacer.Reset()

	buf := make([]byte, readBufferSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if werr := e.writeChunk(ctx, path, buf[:n]); werr != nil {
				return werr
			}
			e.bitratePacer.WaitAfterWrite(n)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (e *Engine) streamMP3Track(ctx context.Context, path string, f *os.File) error {
	fr, err := mp3.NewFrameReader(f)
	if err != nil {
		return err
	}

	e.framePacer.Reset()
	var warnedBitrate bool

	for {
		frame, info, err := fr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if !warnedBitrate && e.cfg.Audio.Bitrate > 0 && info.Bitrate > 0 {
			warnedBitrate = true
			if diff := abs(info.Bitrate - e.cfg.Audio.Bitrate); diff*10 > e.cfg.Audio.Bitrate {
				e.logger.Warn("audio bitrate mismatch",
					"path", path,
					"configured_bps", e.cfg.Audio.Bitrate,
					"detected_bps", info.Bitrate,
				)
			}
		}

		if werr := e.writeChunk(ctx, path, frame); werr != nil {
			return werr
		}
		e.framePacer.WaitAfterFrame(info.Duration)
	}
}

func (e *Engine) writeChunk(ctx context.Context, path string, p []byte) error {
	if _, werr := e.streamer.Write(p); werr != nil {
		if source.IsDisconnectError(werr) && e.cfg.Server.Reconnect {
			e.logger.Warn("stream write failed, reconnecting", "path", path, "error", werr)
			if rerr := reconnectWithBackoff(ctx, e.streamer, e.cfg, e.logger); rerr != nil {
				return fmt.Errorf("reconnect: %w", rerr)
			}
			return ErrTrackInterrupted
		}
		return werr
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func isOpenError(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return true
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission)
}
