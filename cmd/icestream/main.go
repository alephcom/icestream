package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/darrenwiebe/icestream/internal/config"
	"github.com/darrenwiebe/icestream/internal/engine"
	"github.com/darrenwiebe/icestream/internal/logging"
	"github.com/darrenwiebe/icestream/internal/playlist"
	"github.com/spf13/cobra"
)

const version = "0.0.3"

var (
	configPath   string
	legacyPath   string
	migrateOut   string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "icestream",
	Short: "Stream pre-encoded audio to Icecast 2",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start streaming to Icecast",
	RunE:  runServe,
}

var validateCmd = &cobra.Command{
	Use:   "validate-config",
	Short: "Validate configuration and playlist without connecting",
	RunE:  runValidate,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("icestream %s\n", version)
	},
}

var migrateCmd = &cobra.Command{
	Use:   "migrate-config",
	Short: "Convert icegenerator.conf to icestream TOML",
	RunE:  runMigrate,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "configs/example.toml", "path to TOML config file")
	migrateCmd.Flags().StringVarP(&legacyPath, "input", "i", "", "path to icegenerator.conf (required)")
	migrateCmd.Flags().StringVarP(&migrateOut, "output", "o", "", "path for TOML output (default: stdout)")
	_ = migrateCmd.MarkFlagRequired("input")
	rootCmd.AddCommand(serveCmd, validateCmd, migrateCmd, versionCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	result, err := config.MigrateLegacyFile(legacyPath)
	if err != nil {
		return err
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	out, err := result.Config.EncodeTOML()
	if err != nil {
		return err
	}

	if migrateOut == "" {
		_, err = os.Stdout.Write(out)
		return err
	}

	return os.WriteFile(migrateOut, out, 0o644)
}

func runValidate(cmd *cobra.Command, args []string) error {
	cfg, tracks, logger, cleanup, err := load(configPath)
	if err != nil {
		return err
	}
	defer cleanup()
	dests := cfg.Destinations()
	fields := []any{
		"tracks", len(tracks),
		"format", cfg.Audio.Format,
		"destinations", len(dests),
	}
	for _, d := range dests {
		fields = append(fields, "destination", d.Label, "url", d.ServerURL+d.Mount)
	}
	logger.Info("configuration valid", fields...)
	return nil
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, tracks, logger, cleanup, err := load(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eng := engine.NewDefault(cfg, tracks, logger)
	if err := eng.Run(ctx); err != nil {
		return err
	}
	return nil
}

func load(path string) (*config.Config, []playlist.Track, *slog.Logger, func() error, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, nil, func() error { return nil }, err
	}

	logger, cleanup, err := logging.New(logging.Options{
		Level:       cfg.Logging.Level,
		Destination: cfg.Logging.Destination,
		File:        cfg.Logging.File,
	})
	if err != nil {
		return nil, nil, nil, func() error { return nil }, err
	}

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     cfg.Playlist.Paths,
		Recursive: cfg.Playlist.Recursive,
		Extension: cfg.FileExtension(),
	})
	if err != nil {
		_ = cleanup()
		return nil, nil, nil, func() error { return nil }, err
	}

	return cfg, tracks, logger, cleanup, nil
}
