package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/trixsec-dev/trix-agent/internal/agent"
)

// Version is set at build time via ldflags
var Version = "0.2.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := agent.LoadConfig()
	if err != nil {
		return err
	}
	cfg.Version = Version

	logger := setupLogger(cfg.LogFormat, cfg.LogLevel)

	srv, err := agent.New(cfg, logger)
	if err != nil {
		return err
	}

	logger.Info("trix-agent starting",
		"version", Version,
		"poll_interval", cfg.PollInterval,
		"namespaces", cfg.Namespaces,
		"saas_enabled", cfg.SaasEndpoint != "",
	)

	return srv.Run(context.Background())
}

func setupLogger(format, level string) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{}
	switch level {
	case "debug":
		opts.Level = slog.LevelDebug
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}

	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
