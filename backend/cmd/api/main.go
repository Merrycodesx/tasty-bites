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
func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		
		// Dynamically allow whatever headers the client sends during preflight
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Accept, Origin")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

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
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: enableCORS(handler), ReadHeaderTimeout: 5 * time.Second}
	slog.Info("api listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
