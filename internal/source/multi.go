package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
)

type destState int

const (
	destActive destState = iota
	destDisconnected
	destWaitingTrack
)

type ReconnectSettings struct {
	Enabled      bool
	InitialDelay time.Duration
	MaxDelay     time.Duration
	MaxAttempts  int
}

type destination struct {
	label  string
	client *Client
	state  destState

	mu           sync.Mutex
	reconnecting bool
}

// Multi fans out encoded audio to multiple Icecast destinations.
type Multi struct {
	destinations []*destination
	reconnect    ReconnectSettings
	logger       *slog.Logger

	mu     sync.Mutex
	closed bool

	reconnectCtx    context.Context
	reconnectCancel context.CancelFunc
	reconnectWG     sync.WaitGroup
}

func NewMulti(dests []config.Destination, audioFormat string, bitrate int, reconnect ReconnectSettings, logger *slog.Logger) *Multi {
	if logger == nil {
		logger = slog.Default()
	}

	contentType := "audio/mpeg"
	if audioFormat == "ogg" {
		contentType = "application/ogg"
	}

	m := &Multi{
		reconnect: reconnect,
		logger:    logger,
	}
	m.reconnectCtx, m.reconnectCancel = context.WithCancel(context.Background())

	for _, d := range dests {
		m.destinations = append(m.destinations, &destination{
			label: d.Label,
			client: New(Config{
				ServerURL:   d.ServerURL,
				Mount:       d.Mount,
				Username:    d.Username,
				Password:    d.Password,
				Name:        d.Name,
				Genre:       d.Genre,
				Description: d.Description,
				URL:         d.URL,
				Public:      d.Public,
				ContentType: contentType,
				Bitrate:     bitrate,
			}),
			state: destActive,
		})
	}
	return m
}

func (m *Multi) Connect(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("multi streamer closed")
	}
	m.mu.Unlock()

	var connected []*destination
	for _, d := range m.destinations {
		if err := d.client.Connect(ctx); err != nil {
			for _, c := range connected {
				_ = c.client.Close()
			}
			return fmt.Errorf("connect %s: %w", d.label, err)
		}
		d.mu.Lock()
		d.state = destActive
		d.reconnecting = false
		d.mu.Unlock()
		connected = append(connected, d)
	}
	return nil
}

func (m *Multi) Reconnect(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("multi streamer closed")
	}
	m.mu.Unlock()

	var firstErr error
	for _, d := range m.destinations {
		d.mu.Lock()
		active := d.state == destActive
		d.mu.Unlock()
		if active {
			continue
		}
		if err := d.client.Reconnect(ctx); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconnect %s: %w", d.label, err)
			}
			continue
		}
		d.mu.Lock()
		d.state = destWaitingTrack
		d.reconnecting = false
		d.mu.Unlock()
		m.logger.Info("reconnected to stream server", "destination", d.label)
	}
	return firstErr
}

func (m *Multi) BeginTrack() {
	for _, d := range m.destinations {
		d.mu.Lock()
		if d.state == destWaitingTrack {
			d.state = destActive
			m.logger.Info("destination resumed at track boundary", "destination", d.label)
		}
		d.mu.Unlock()
	}
}

func (m *Multi) Write(p []byte) (int, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, ErrNotConnected
	}
	m.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}

	activeCount := 0
	wroteAny := false

	for _, d := range m.destinations {
		d.mu.Lock()
		state := d.state
		d.mu.Unlock()
		if state != destActive {
			continue
		}
		activeCount++

		if _, err := d.client.Write(p); err != nil {
			if !IsDisconnectError(err) {
				return 0, err
			}
			m.markDisconnected(d, err)
			activeCount--
			continue
		}
		wroteAny = true
	}

	if wroteAny {
		return len(p), nil
	}
	if activeCount == 0 {
		return 0, ErrNotConnected
	}
	return len(p), nil
}

func (m *Multi) markDisconnected(d *destination, err error) {
	d.mu.Lock()
	if d.state != destActive {
		d.mu.Unlock()
		return
	}
	d.state = destDisconnected
	alreadyReconnecting := d.reconnecting
	if !alreadyReconnecting {
		d.reconnecting = true
	}
	d.mu.Unlock()

	m.logger.Warn("destination write failed", "destination", d.label, "error", err)

	if !alreadyReconnecting && m.reconnect.Enabled {
		m.reconnectWG.Add(1)
		go func() {
			defer m.reconnectWG.Done()
			m.reconnectDestination(d)
		}()
	}
}

func (m *Multi) reconnectDestination(d *destination) {
	delay := m.reconnect.InitialDelay
	if delay <= 0 {
		delay = time.Second
	}
	maxDelay := m.reconnect.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 60 * time.Second
	}

	attempt := 0
	for {
		if m.reconnectCtx.Err() != nil {
			return
		}

		attempt++
		if m.reconnect.MaxAttempts > 0 && attempt > m.reconnect.MaxAttempts {
			m.logger.Warn("destination reconnect exhausted", "destination", d.label, "attempts", attempt-1)
			d.mu.Lock()
			d.reconnecting = false
			d.mu.Unlock()
			return
		}

		m.logger.Warn("reconnecting destination", "destination", d.label, "attempt", attempt)
		if err := d.client.Reconnect(m.reconnectCtx); err != nil {
			m.logger.Warn("destination reconnect failed", "destination", d.label, "attempt", attempt, "error", err)
		} else {
			d.mu.Lock()
			d.state = destWaitingTrack
			d.reconnecting = false
			d.mu.Unlock()
			m.logger.Info("destination reconnected, waiting for next track", "destination", d.label, "attempt", attempt)
			return
		}

		select {
		case <-m.reconnectCtx.Done():
			return
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (m *Multi) SetMetadata(title string) error {
	var errs []error
	for _, d := range m.destinations {
		d.mu.Lock()
		state := d.state
		d.mu.Unlock()
		if state == destDisconnected {
			continue
		}
		if err := d.client.SetMetadata(title); err != nil {
			m.logger.Warn("metadata update failed", "destination", d.label, "error", err, "title", title)
			errs = append(errs, fmt.Errorf("%s: %w", d.label, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Multi) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	m.reconnectCancel()
	m.reconnectWG.Wait()

	var firstErr error
	for _, d := range m.destinations {
		if err := d.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
