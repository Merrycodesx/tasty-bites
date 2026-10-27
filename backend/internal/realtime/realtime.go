// Package realtime implements Server-Sent Events (SSE) on top of a committed-event log.
//
// Why SSE: every real-time flow here is server -> client (kitchen/TV/customer are told that
// something changed; they act through normal REST calls). SSE needs no extra infrastructure,
// reconnects and replays via Last-Event-ID, and works through ordinary HTTP proxies.
package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Event struct {
	ID            int64     `json:"id"`
	BranchID      int64     `json:"-"`
	Type          string    `json:"type"`
	OrderID       int64     `json:"orderId"`
	OrderNumber   int64     `json:"orderNumber"`
	OrderType     string    `json:"orderType"`
	Status        string    `json:"status"`
	PaymentStatus string    `json:"paymentStatus"`
	TableLabel    string    `json:"tableLabel"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Record writes an event inside the caller's transaction, reading the order's state as just changed.
// Call Hub.Publish with the returned event only AFTER the transaction commits.
func Record(tx *sql.Tx, typ string, orderID int64) (Event, error) {
	var e Event
	e.Type, e.OrderID = typ, orderID
	err := tx.QueryRow(`INSERT INTO events(branch_id,type,order_id,order_number,order_type,status,payment_status,table_label)
		SELECT o.branch_id,$2,o.id,o.order_number,o.order_type,o.status,o.payment_status,COALESCE(t.label,'')
		FROM orders o LEFT JOIN dining_tables t ON t.id=o.table_id WHERE o.id=$1
		RETURNING id,branch_id,order_number,order_type,status,payment_status,table_label,created_at`, orderID, typ).
		Scan(&e.ID, &e.BranchID, &e.OrderNumber, &e.OrderType, &e.Status, &e.PaymentStatus, &e.TableLabel, &e.CreatedAt)
	return e, err
}

type sub struct {
	ch      chan Event
	branch  int64
	dropped bool
}

type Hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[*sub]struct{}{}} }

// Publish fans an already-committed event out to subscribers of the same branch only.
// A subscriber that cannot keep up is dropped (its stream tells the client to resync).
func (h *Hub) Publish(evs ...Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range evs {
		for s := range h.subs {
			if s.branch != e.BranchID || s.dropped {
				continue
			}
			select {
			case s.ch <- e:
			default:
				s.dropped = true
				close(s.ch)
			}
		}
	}
}

func (h *Hub) subscribe(branch int64) (*sub, func()) {
	s := &sub{ch: make(chan Event, 64), branch: branch}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s, func() {
		h.mu.Lock()
		delete(h.subs, s)
		if !s.dropped {
			s.dropped = true
			close(s.ch)
		}
		h.mu.Unlock()
	}
}

// Subscribers returns the number of live streams (for tests and diagnostics).
func (h *Hub) Subscribers() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.subs) }

// Stream describes one SSE connection.
type Stream struct {
	Hub      *Hub
	DB       *sql.DB
	BranchID int64
	// Accept decides whether this client may receive an event (audience scoping).
	Accept func(Event) bool
	// Payload turns an event into the JSON this audience may see (data filtering).
	Payload func(Event) any
	// Valid is re-checked periodically; false closes the stream (expired session, revoked display).
	Valid func() bool
}

// MaxReplay is how many missed events are replayed before the client is told to resync.
const MaxReplay = 500

// Timing knobs (variables so tests can shorten them).
var (
	PingEvery = 15 * time.Second
	Recheck   = 30 * time.Second
)

// Serve runs the stream until the client disconnects, auth becomes invalid, or the server stops.
// Protocol: on connect `ready` carries the latest event id; clients then read current state via REST.
// On reconnect with Last-Event-ID, missed events are replayed, or `resync` is sent if too many were missed.
func (st *Stream) Serve(w http.ResponseWriter, r *http.Request) {
	encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(s string) bool {
		rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprint(w, s); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	send := func(id int64, name string, data []byte) bool {
		if id > 0 {
			return write(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", id, name, data))
		}
		return write(fmt.Sprintf("event: %s\ndata: %s\n\n", name, data))
	}

	s, cancel := st.Hub.subscribe(st.BranchID)
	defer cancel()

	var last int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		last, _ = strconv.ParseInt(v, 10, 64)
	} else if v := r.URL.Query().Get("lastEventId"); v != "" {
		last, _ = strconv.ParseInt(v, 10, 64)
	}

	var latest int64
	st.DB.QueryRowContext(r.Context(), `SELECT COALESCE(max(id),0) FROM events WHERE branch_id=$1`, st.BranchID).Scan(&latest)
	if last > latest {
		last = 0 // client remembers an id from before a database reset: force a fresh start
		if !send(0, "resync", []byte(`{}`)) {
			return
		}
	}
	if last > 0 {
		var missed int
		st.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM events WHERE branch_id=$1 AND id>$2`, st.BranchID, last).Scan(&missed)
		if missed > MaxReplay {
			if !send(0, "resync", []byte(`{}`)) {
				return
			}
			last = latest
		} else if missed > 0 {
			rows, err := st.DB.QueryContext(r.Context(), `SELECT id,branch_id,type,order_id,order_number,order_type,status,payment_status,table_label,created_at
				FROM events WHERE branch_id=$1 AND id>$2 ORDER BY id`, st.BranchID, last)
			if err == nil {
				var replay []Event
				for rows.Next() {
					var e Event
					rows.Scan(&e.ID, &e.BranchID, &e.Type, &e.OrderID, &e.OrderNumber, &e.OrderType, &e.Status, &e.PaymentStatus, &e.TableLabel, &e.CreatedAt)
					replay = append(replay, e)
				}
				rows.Close()
				for _, e := range replay {
					last = e.ID
					if st.Accept(e) && !send(e.ID, "order", encode(st.Payload(e))) {
						return
					}
				}
			}
		}
	}
	if last == 0 {
		last = latest
	}
	if !send(0, "ready", []byte(fmt.Sprintf(`{"lastEventId":%d}`, last))) {
		return
	}

	ping := time.NewTicker(PingEvery)
	check := time.NewTicker(Recheck)
	defer ping.Stop()
	defer check.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-s.ch:
			if !ok {
				send(0, "resync", []byte(`{}`)) // we were too slow; client re-reads state and reconnects
				return
			}
			if e.ID <= last {
				continue // duplicate (already replayed)
			}
			last = e.ID
			if st.Accept(e) && !send(e.ID, "order", encode(st.Payload(e))) {
				return
			}
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		case <-check.C:
			if st.Valid != nil && !st.Valid() {
				send(0, "unauthorized", []byte(`{}`))
				return
			}
		}
	}
}

// Prune deletes events older than the retention window. Replay older than that triggers `resync`.
func Prune(ctx context.Context, d *sql.DB, keep time.Duration) {
	d.ExecContext(ctx, `DELETE FROM events WHERE created_at < now() - make_interval(secs => $1)`, keep.Seconds())
}
