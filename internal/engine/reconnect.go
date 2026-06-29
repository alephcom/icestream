package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
)

func reconnectWithBackoff(ctx context.Context, s Streamer, cfg *config.Config, logger *slog.Logger) error {
	if !cfg.Server.Reconnect {
		return fmt.Errorf("reconnect disabled")
	}

	initial := cfg.ReconnectInitialDelay()
	maxDelay := cfg.ReconnectMaxDelay()
	delay := initial
	attempt := 0

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempt++
		if cfg.Server.ReconnectMaxAttempts > 0 && attempt > cfg.Server.ReconnectMaxAttempts {
			return fmt.Errorf("reconnect failed after %d attempts", cfg.Server.ReconnectMaxAttempts)
		}

		logger.Warn("reconnecting to stream servers", "attempt", attempt, "destinations", len(cfg.Destinations()))
		if err := s.Reconnect(ctx); err != nil {
			logger.Warn("reconnect attempt failed", "attempt", attempt, "error", err)
		} else {
			logger.Info("reconnected to stream servers", "attempt", attempt)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}
