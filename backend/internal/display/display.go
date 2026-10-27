// Package display serves the public TV order board: a revocable device token, a minimal
// read-only snapshot, a filtered SSE stream, and manager tools for devices and display settings.
package display

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tastybites/internal/auth"
	"tastybites/internal/httpx"
	"tastybites/internal/realtime"
)

type Service struct {
	DB  *sql.DB
	Hub *realtime.Hub
}

type ctxKey struct{}
type device struct{ ID, BranchID int64 }

func hash(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

// Settings (per branch, in the settings table) with documented defaults.
type Settings struct {
	ReadyMinutes int  `json:"readyMinutes"` // a ready order stays on the board this long
	ShowTable    bool `json:"showTable"`    // show the table label (off by default: minimum data)
	MaxOrders    int  `json:"maxOrders"`    // hard cap on orders returned
}

const staleHours = 6 // orders still received/preparing after this long are hidden as stale

func (s *Service) settings(branch int64) Settings {
	st := Settings{ReadyMinutes: 10, ShowTable: false, MaxOrders: 40}
	rows, err := s.DB.Query(`SELECT key,value FROM settings WHERE branch_id=$1 AND key LIKE 'tv\_%'`, branch)
	if err != nil {
		return st
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		switch k {
		case "tv_ready_minutes":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 60 {
				st.ReadyMinutes = n
			}
		case "tv_show_table":
			st.ShowTable = v == "true"
		case "tv_max_orders":
			if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 100 {
				st.MaxOrders = n
			}
		}
	}
	return st
}

// Auth authenticates a TV by its device token (Authorization: Bearer ...). Tokens are revocable.
func (s *Service) Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := auth.BearerToken(r)
		if len(t) != 64 {
			httpx.Fail(w, httpx.Err(401, "display_unauthorized", "This display is not paired or was revoked."))
			return
		}
		var d device
		err := s.DB.QueryRow(`UPDATE display_devices SET last_seen_at=now() WHERE token_hash=$1 AND revoked_at IS NULL RETURNING id, branch_id`, hash(t)).Scan(&d.ID, &d.BranchID)
		if err != nil {
			httpx.Fail(w, httpx.Err(401, "display_unauthorized", "This display is not paired or was revoked."))
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, d)))
	}
}

func dev(r *http.Request) device { d, _ := r.Context().Value(ctxKey{}).(device); return d }

// PublicStatus maps internal statuses to what customers see. Anything else is removed from the board.
func PublicStatus(s string) string {
	switch s {
	case "placed":
		return "received"
	case "preparing":
		return "preparing"
	case "ready":
		return "ready"
	}
	return "removed"
}

type Row struct {
	OrderNumber int64  `json:"orderNumber"`
	Status      string `json:"status"`
	TableLabel  string `json:"tableLabel,omitempty"`
}

// Snapshot is the ONLY order data a TV can read. No ids, names, phones, notes, totals or payment data.
func (s *Service) Snapshot(w http.ResponseWriter, r *http.Request) {
	d := dev(r)
	st := s.settings(d.BranchID)
	var latest int64
	s.DB.QueryRow(`SELECT COALESCE(max(id),0) FROM events WHERE branch_id=$1`, d.BranchID).Scan(&latest) // read first, so no change is missed
	rows, err := s.DB.Query(`SELECT o.order_number, o.status, CASE WHEN $4 AND o.order_type='dine_in' THEN COALESCE(t.label,'') ELSE '' END
		FROM orders o LEFT JOIN dining_tables t ON t.id=o.table_id
		WHERE o.branch_id=$1 AND (
		  (o.status IN ('placed','preparing') AND o.created_at > now() - make_interval(hours => $5))
		  OR (o.status='ready' AND COALESCE((SELECT max(h.created_at) FROM order_status_history h WHERE h.order_id=o.id AND h.to_status='ready'), o.updated_at) > now() - make_interval(mins => $2)))
		ORDER BY o.created_at LIMIT $3`, d.BranchID, st.ReadyMinutes, st.MaxOrders, st.ShowTable, staleHours)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var x Row
		var raw string
		if err := rows.Scan(&x.OrderNumber, &raw, &x.TableLabel); err != nil {
			httpx.Fail(w, err)
			return
		}
		x.Status = PublicStatus(raw)
		out = append(out, x)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 200, map[string]any{"orders": out, "lastEventId": latest, "readyMinutes": st.ReadyMinutes, "generatedAt": time.Now().UTC()})
}

// Events streams display-safe change notifications; clients re-read the snapshot when one arrives.
func (s *Service) Events(w http.ResponseWriter, r *http.Request) {
	d := dev(r)
	token := hash(auth.BearerToken(r))
	showTable := s.settings(d.BranchID).ShowTable
	st := &realtime.Stream{
		Hub: s.Hub, DB: s.DB, BranchID: d.BranchID,
		Accept: func(e realtime.Event) bool { return true },
		Payload: func(e realtime.Event) any {
			row := map[string]any{"type": "display.updated", "orderNumber": e.OrderNumber, "status": PublicStatus(e.Status)}
			if showTable && e.OrderType == "dine_in" && e.TableLabel != "" {
				row["tableLabel"] = e.TableLabel
			}
			return row
		},
		Valid: func() bool {
			var ok bool
			s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM display_devices WHERE token_hash=$1 AND revoked_at IS NULL)`, token).Scan(&ok)
			return ok
		},
	}
	st.Serve(w, r)
}

// ---- manager administration ----

func (s *Service) CreateDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		httpx.Fail(w, httpx.Err(400, "invalid_name", "Give the display a name (up to 60 characters)."))
		return
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	u := auth.From(r)
	var id int64
	if err := s.DB.QueryRow(`INSERT INTO display_devices(branch_id,name,token_hash,created_by) VALUES($1,$2,$3,$4) RETURNING id`, u.BranchID, in.Name, hash(token), u.ID).Scan(&id); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'display.create','display',$2,$3)`, u.ID, id, in.Name)
	// The token is shown exactly once; only its hash is stored.
	httpx.JSON(w, 201, map[string]any{"id": id, "name": in.Name, "token": token})
}

func (s *Service) ListDevices(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	rows, err := s.DB.Query(`SELECT id,name,created_at,last_seen_at,revoked_at FROM display_devices WHERE branch_id=$1 ORDER BY id`, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type D struct {
		ID         int64      `json:"id"`
		Name       string     `json:"name"`
		CreatedAt  time.Time  `json:"createdAt"`
		LastSeenAt *time.Time `json:"lastSeenAt"`
		RevokedAt  *time.Time `json:"revokedAt"`
	}
	out := []D{}
	for rows.Next() {
		var x D
		rows.Scan(&x.ID, &x.Name, &x.CreatedAt, &x.LastSeenAt, &x.RevokedAt)
		out = append(out, x)
	}
	httpx.JSON(w, 200, map[string]any{"displays": out})
}

func (s *Service) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := auth.From(r)
	res, err := s.DB.Exec(`UPDATE display_devices SET revoked_at=now() WHERE id=$1 AND branch_id=$2 AND revoked_at IS NULL`, id, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Fail(w, httpx.Err(404, "not_found", "Display not found or already revoked."))
		return
	}
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'display.revoke','display',$2,'')`, u.ID, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) GetSettings(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, s.settings(auth.From(r).BranchID))
}

func (s *Service) PutSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.ReadyMinutes < 1 || in.ReadyMinutes > 60 || in.MaxOrders < 10 || in.MaxOrders > 100 {
		httpx.Fail(w, httpx.Err(400, "invalid_settings", "Ready minutes must be 1-60 and max orders 10-100."))
		return
	}
	u := auth.From(r)
	for k, v := range map[string]string{"tv_ready_minutes": strconv.Itoa(in.ReadyMinutes), "tv_show_table": strconv.FormatBool(in.ShowTable), "tv_max_orders": strconv.Itoa(in.MaxOrders)} {
		if _, err := s.DB.Exec(`INSERT INTO settings(branch_id,key,value) VALUES($1,$2,$3) ON CONFLICT (branch_id,key) DO UPDATE SET value=EXCLUDED.value`, u.BranchID, k, v); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,detail) VALUES($1,'settings.tv','settings','tv display settings changed')`, u.ID)
	httpx.JSON(w, 200, in)
}
