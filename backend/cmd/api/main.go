package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"tastybites/internal/app"
	"tastybites/internal/config"
	"tastybites/internal/db"
	"tastybites/internal/seed"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	d, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	if err := db.Migrate(d); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	if cfg.SeedDevData {
		if err := seed.Run(d, cfg.SeedPassword); err != nil {
			slog.Error("seed", "err", err)
			os.Exit(1)
		}
	}
	handler, _ := app.New(context.Background(), d, cfg)
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("api listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
