// Package app wires services and routes. main and the integration tests both use it, so tests exercise the real routing.
package app

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"tastybites/internal/auth"
	"tastybites/internal/config"
	"tastybites/internal/display"
	"tastybites/internal/httpx"
	"tastybites/internal/live"
	"tastybites/internal/menu"
	"tastybites/internal/orders"
	"tastybites/internal/payments"
	"tastybites/internal/realtime"
	"tastybites/internal/tables"
)

// New builds the full HTTP handler. Pruning of old events runs until ctx is cancelled.
func New(ctx context.Context, d *sql.DB, cfg config.Config) (http.Handler, *realtime.Hub) {
	var defaultBranch int64 = 1
	d.QueryRow(`SELECT COALESCE(min(id),1) FROM branches`).Scan(&defaultBranch)

	a := &auth.Service{DB: d}
	m := &menu.Service{DB: d, BranchID: defaultBranch}
	hub := realtime.NewHub()
	o := &orders.Service{DB: d, BranchID: defaultBranch, Hub: hub}
	tb := &tables.Service{DB: d, BranchID: defaultBranch, MiniAppURL: cfg.MiniAppURL}
	dp := &display.Service{DB: d, Hub: hub}
	lv := &live.Service{DB: d, Hub: hub, Auth: a}
	go func() { // keep the event log small; replay older than this triggers a client resync
		for {
			realtime.Prune(ctx, d, 24*time.Hour)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}()
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
	mux.Handle("GET /api/v1/tables/resolve", httpx.RateLimit(60, time.Minute, http.HandlerFunc(tb.Resolve)))
	mux.Handle("POST /api/v1/orders", httpx.RateLimit(20, time.Minute, http.HandlerFunc(o.CustomerCreate)))
	mux.HandleFunc("GET /api/v1/orders/track/{token}", o.Track)
	mux.HandleFunc("GET /api/v1/orders/track/{token}/events", lv.CustomerEvents)
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
	mux.HandleFunc("GET /api/v1/staff/events", staff(lv.StaffEvents))
	// Manager: tables/QR, TV displays and settings
	mux.HandleFunc("GET /api/v1/staff/admin/tables", mgr(tb.AdminList))
	mux.HandleFunc("POST /api/v1/staff/admin/tables", mgr(tb.Create))
	mux.HandleFunc("PATCH /api/v1/staff/admin/tables/{id}", mgr(tb.Patch))
	mux.HandleFunc("POST /api/v1/staff/admin/tables/{id}/rotate-qr", mgr(tb.Rotate))
	mux.HandleFunc("GET /api/v1/staff/admin/displays", mgr(dp.ListDevices))
	mux.HandleFunc("POST /api/v1/staff/admin/displays", mgr(dp.CreateDevice))
	mux.HandleFunc("POST /api/v1/staff/admin/displays/{id}/revoke", mgr(dp.RevokeDevice))
	mux.HandleFunc("GET /api/v1/staff/admin/display-settings", mgr(dp.GetSettings))
	mux.HandleFunc("PUT /api/v1/staff/admin/display-settings", mgr(dp.PutSettings))
	// Public TV (device token, read-only, minimal fields)
	mux.Handle("GET /api/v1/display/orders", httpx.RateLimit(120, time.Minute, dp.Auth(dp.Snapshot)))
	mux.HandleFunc("GET /api/v1/display/events", dp.Auth(dp.Events))
	return httpx.CORS(cfg.AllowedOrigins, mux), hub
}
