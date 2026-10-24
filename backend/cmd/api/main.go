package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"tastybites/internal/auth"
	"tastybites/internal/config"
	"tastybites/internal/db"
	"tastybites/internal/httpx"
	"tastybites/internal/menu"
	"tastybites/internal/orders"
	"tastybites/internal/payments"
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
	var defaultBranch int64 = 1
	d.QueryRow(`SELECT COALESCE(min(id),1) FROM branches`).Scan(&defaultBranch)

	a := &auth.Service{DB: d}
	m := &menu.Service{DB: d, BranchID: defaultBranch}
	o := &orders.Service{DB: d, BranchID: defaultBranch}
	var _ payments.Provider = payments.Disabled{}

	staff := a.Require("admin", "manager", "waiter", "cashier", "kitchen")
	mgr := a.Require("admin", "manager")
	front := a.Require("admin", "manager", "waiter", "cashier")
	cook := a.Require("admin", "manager", "kitchen")
	money := a.Require("admin", "manager", "cashier")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Ping(); err != nil {
			httpx.JSON(w, 503, map[string]string{"status": "db_down"})
			return
		}
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
	})
	// Auth
	mux.Handle("POST /api/v1/auth/login", httpx.RateLimit(10, time.Minute, http.HandlerFunc(a.Login)))
	mux.HandleFunc("POST /api/v1/auth/logout", a.Logout)
	mux.HandleFunc("GET /api/v1/auth/me", staff(a.Me))
	// Customer (public)
	mux.HandleFunc("GET /api/v1/menu", m.Public)
	mux.HandleFunc("GET /api/v1/tables", o.PublicTables)
	mux.Handle("POST /api/v1/orders", httpx.RateLimit(20, time.Minute, http.HandlerFunc(o.CustomerCreate)))
	mux.HandleFunc("GET /api/v1/orders/track/{token}", o.Track)
	// Staff
	mux.HandleFunc("GET /api/v1/staff/menu", staff(m.Staff))
	mux.HandleFunc("POST /api/v1/staff/menu/items", mgr(m.CreateItem))
	mux.HandleFunc("PATCH /api/v1/staff/menu/items/{id}", mgr(m.PatchItem))
	mux.HandleFunc("GET /api/v1/staff/tables", staff(o.Tables))
	mux.HandleFunc("GET /api/v1/staff/orders", staff(o.List))
	mux.HandleFunc("POST /api/v1/staff/orders", front(o.StaffCreate))
	mux.HandleFunc("GET /api/v1/staff/orders/{id}", staff(o.Get))
	mux.HandleFunc("POST /api/v1/staff/orders/{id}/status", staff(o.SetStatus))
	mux.HandleFunc("POST /api/v1/staff/orders/{id}/items/{itemId}/kitchen-status", cook(o.SetKitchenStatus))
	mux.HandleFunc("POST /api/v1/staff/orders/{id}/payments/cash", money(o.CashPayment))
	mux.HandleFunc("POST /api/v1/orders/{id}/payments/telebirr", func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, httpx.Err(501, "payment_not_configured", "Telebirr payment is not enabled yet. It needs approved credentials and the official integration."))
	})
	mux.HandleFunc("GET /api/v1/staff/reports/summary", mgr(o.Summary))

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: httpx.CORS(cfg.AllowedOrigins, mux), ReadHeaderTimeout: 5 * time.Second}
	slog.Info("api listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
