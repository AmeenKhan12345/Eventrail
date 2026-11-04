package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AmeenKhan12345/Eventrail/internal/config"
	"github.com/AmeenKhan12345/Eventrail/internal/delivery"
	"github.com/AmeenKhan12345/Eventrail/internal/platform/postgres"
	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

func main() {
	settings, err := config.LoadWorker()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	logger := newLogger(settings.LogLevel)
	slog.SetDefault(logger)

	if err := postgres.RunMigrations(settings.DatabaseURL); err != nil {
		logger.Error("run migrations", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, settings.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	box, err := secrets.NewBox(settings.SigningSecretKey)
	if err != nil {
		logger.Error("signing secret key", "err", err)
		os.Exit(1)
	}
	sender := delivery.NewSender(delivery.Config{
		Timeout:       settings.DeliveryTimeout,
		AllowInsecure: settings.AllowInsecureURLs,
	}, delivery.DefaultPolicy)
	worker, err := delivery.NewWorker(delivery.NewQueueStore(pool), box, sender, delivery.WorkerConfig{
		Concurrency:  settings.Concurrency,
		PollInterval: settings.PollInterval,
		Lease:        settings.Lease,
	})
	if err != nil {
		logger.Error("worker config", "err", err)
		os.Exit(1)
	}

	logger.Info("worker starting", "concurrency", settings.Concurrency)
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped", "err", err)
		os.Exit(1)
	}
	logger.Info("worker stopped")
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
