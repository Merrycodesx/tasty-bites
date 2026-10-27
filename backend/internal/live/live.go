// Package live exposes the authenticated SSE endpoints for staff and for a single customer order.
package live

import (
	"database/sql"
	"net/http"

	"tastybites/internal/auth"
	"tastybites/internal/httpx"
	"tastybites/internal/realtime"
)

type Service struct {
	DB   *sql.DB
	Hub  *realtime.Hub
	Auth *auth.Service
}

// StaffEvents streams every order change in the staff member's own branch. Payloads contain no
// customer contact data. The session is re-validated periodically, so logout/expiry ends the stream.
func (s *Service) StaffEvents(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	token := auth.BearerToken(r)
	st := &realtime.Stream{
		Hub: s.Hub, DB: s.DB, BranchID: u.BranchID,
		Accept:  func(e realtime.Event) bool { return true },
		Payload: func(e realtime.Event) any { return e },
		Valid:   func() bool { _, err := s.Auth.Lookup(token); return err == nil },
	}
	st.Serve(w, r)
}

// CustomerEvents streams changes to ONE order, identified by its unguessable tracking token.
func (s *Service) CustomerEvents(w http.ResponseWriter, r *http.Request) {
	var orderID, branch int64
	err := s.DB.QueryRow(`SELECT id, branch_id FROM orders WHERE public_token=$1`, r.PathValue("token")).Scan(&orderID, &branch)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(404, "not_found", "We could not find that order."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	st := &realtime.Stream{
		Hub: s.Hub, DB: s.DB, BranchID: branch,
		Accept: func(e realtime.Event) bool { return e.OrderID == orderID },
		Payload: func(e realtime.Event) any {
			return map[string]any{"type": e.Type, "orderNumber": e.OrderNumber, "status": e.Status, "paymentStatus": e.PaymentStatus}
		},
	}
	st.Serve(w, r)
}
