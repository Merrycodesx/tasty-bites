// Package tables handles table QR codes: public resolution and manager administration.
package tables

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"tastybites/internal/auth"
	"tastybites/internal/httpx"
)

type Service struct {
	DB         *sql.DB
	BranchID   int64  // default branch for public resolution
	MiniAppURL string // base URL encoded into QR codes (PUBLIC_MINIAPP_URL)
}

const newToken = `replace(gen_random_uuid()::text,'-','') || replace(gen_random_uuid()::text,'-','')`

func invalid() *httpx.APIError {
	return httpx.Err(404, "table_invalid", "This table QR code is not valid or is no longer active. Please ask staff for help.")
}

// Resolve validates a QR token and returns only the table label and restaurant name.
func (s *Service) Resolve(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	if len(t) != 64 {
		httpx.Fail(w, invalid())
		return
	}
	var label, name string
	err := s.DB.QueryRow(`SELECT t.label, b.name FROM dining_tables t JOIN branches b ON b.id=t.branch_id
		WHERE t.qr_token=$1 AND t.active AND t.branch_id=$2`, t, s.BranchID).Scan(&label, &name)
	if err == sql.ErrNoRows {
		httpx.Fail(w, invalid())
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"table": map[string]string{"label": label}, "restaurant": map[string]string{"name": name}})
}

func (s *Service) qrURL(token string) string {
	if s.MiniAppURL == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(s.MiniAppURL, "?") {
		sep = "&"
	}
	return s.MiniAppURL + sep + "t=" + url.QueryEscape(token)
}

type adminTable struct {
	ID     int64  `json:"id"`
	Label  string `json:"label"`
	Seats  int    `json:"seats"`
	Active bool   `json:"active"`
	QRURL  string `json:"qrUrl"`
}

func (s *Service) AdminList(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	rows, err := s.DB.Query(`SELECT id,label,seats,active,qr_token FROM dining_tables WHERE branch_id=$1 ORDER BY id`, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []adminTable{}
	for rows.Next() {
		var t adminTable
		var tok string
		if err := rows.Scan(&t.ID, &t.Label, &t.Seats, &t.Active, &tok); err != nil {
			httpx.Fail(w, err)
			return
		}
		t.QRURL = s.qrURL(tok)
		out = append(out, t)
	}
	httpx.JSON(w, 200, map[string]any{"tables": out, "qrConfigured": s.MiniAppURL != ""})
}

func (s *Service) audit(u auth.User, action string, id int64, detail string) {
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,$2,'table',$3,$4)`, u.ID, action, id, detail)
}

func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label string `json:"label"`
		Seats int    `json:"seats"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Label = strings.TrimSpace(in.Label)
	if in.Seats == 0 {
		in.Seats = 4
	}
	if in.Label == "" || len(in.Label) > 20 || in.Seats < 1 || in.Seats > 50 {
		httpx.Fail(w, httpx.Err(400, "invalid_table", "Enter a table label (up to 20 characters) and 1-50 seats."))
		return
	}
	u := auth.From(r)
	var id int64
	err := s.DB.QueryRow(`INSERT INTO dining_tables(branch_id,label,seats) VALUES($1,$2,$3) ON CONFLICT (branch_id,label) DO NOTHING RETURNING id`, u.BranchID, in.Label, in.Seats).Scan(&id)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(409, "table_exists", "A table with that label already exists."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(u, "table.create", id, in.Label)
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Service) Patch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Active *bool `json:"active"`
		Seats  *int  `json:"seats"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Seats != nil && (*in.Seats < 1 || *in.Seats > 50) {
		httpx.Fail(w, httpx.Err(400, "invalid_table", "Seats must be 1-50."))
		return
	}
	u := auth.From(r)
	res, err := s.DB.Exec(`UPDATE dining_tables SET active=COALESCE($3,active), seats=COALESCE($4,seats) WHERE id=$1 AND branch_id=$2`, id, u.BranchID, in.Active, in.Seats)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Fail(w, httpx.Err(404, "not_found", "Table not found."))
		return
	}
	s.audit(u, "table.update", id, "active/seats changed")
	w.WriteHeader(http.StatusNoContent)
}

// Rotate replaces the QR token, instantly invalidating every printed code for that table.
func (s *Service) Rotate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := auth.From(r)
	res, err := s.DB.Exec(`UPDATE dining_tables SET qr_token=`+newToken+` WHERE id=$1 AND branch_id=$2`, id, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Fail(w, httpx.Err(404, "not_found", "Table not found."))
		return
	}
	s.audit(u, "table.rotate_qr", id, "QR token rotated")
	w.WriteHeader(http.StatusNoContent)
}
