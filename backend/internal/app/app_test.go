package app_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"tastybites/internal/app"
	"tastybites/internal/config"
	"tastybites/internal/db"
	"tastybites/internal/realtime"
	"tastybites/internal/seed"
)

type env struct {
	t   *testing.T
	d   *sql.DB
	srv *httptest.Server
	hub *realtime.Hub
}

func setup(t *testing.T) *env {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	d.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(d, "test-password-1"); err != nil {
		t.Fatal(err)
	}
	realtime.PingEvery, realtime.Recheck = 200*time.Millisecond, 300*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	h, hub := app.New(ctx, d, config.Config{MiniAppURL: "https://example.test/mini"})
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close(); cancel() })
	return &env{t, d, srv, hub}
}

func (e *env) do(method, path, token, idem string, body any) (int, map[string]any) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (e *env) login(user string) string {
	code, out := e.do("POST", "/api/v1/auth/login", "", "", map[string]string{"username": user, "password": "test-password-1"})
	if code != 200 {
		e.t.Fatalf("login %s: %d", user, code)
	}
	return out["token"].(string)
}

func (e *env) q(query string, args ...any) (v string) {
	if err := e.d.QueryRow(query, args...).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *env) itemID(name string) int64 {
	var id int64
	e.d.QueryRow(`SELECT id FROM menu_items WHERE name=$1`, name).Scan(&id)
	return id
}

var keyN int

func (e *env) order(body map[string]any) (int, map[string]any) {
	keyN++
	return e.do("POST", "/api/v1/orders", "", fmt.Sprintf("test-key-%06d", keyN), body)
}

func (e *env) sandwich(extra map[string]any) map[string]any {
	b := map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.itemID("Chicken Sandwich"), "quantity": 1, "extraIds": []int64{}}}}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func (e *env) tableToken(label string) string {
	return e.q(`SELECT qr_token FROM dining_tables WHERE label=$1`, label)
}

// ---------- QR / table validation ----------

func TestQRResolveValidInvalidInactiveRotated(t *testing.T) {
	e := setup(t)
	tok := e.tableToken("T2")
	code, out := e.do("GET", "/api/v1/tables/resolve?t="+tok, "", "", nil)
	if code != 200 || out["table"].(map[string]any)["label"] != "T2" {
		t.Fatalf("valid token: %d %v", code, out)
	}
	if strings.Contains(fmt.Sprint(out), "id") && out["table"].(map[string]any)["id"] != nil {
		t.Fatal("resolve must not expose internal ids")
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 64), strings.Repeat("a", 65)} {
		if code, _ := e.do("GET", "/api/v1/tables/resolve?t="+bad, "", "", nil); code != 404 {
			t.Fatalf("token %q should be 404, got %d", bad, code)
		}
	}
	e.d.Exec(`UPDATE dining_tables SET active=false WHERE label='T2'`)
	if code, _ := e.do("GET", "/api/v1/tables/resolve?t="+tok, "", "", nil); code != 404 {
		t.Fatalf("inactive table must be 404, got %d", code)
	}
	e.d.Exec(`UPDATE dining_tables SET active=true WHERE label='T2'`)
	mgr := e.login("manager")
	var id int64
	e.d.QueryRow(`SELECT id FROM dining_tables WHERE label='T2'`).Scan(&id)
	if code, _ := e.do("POST", fmt.Sprintf("/api/v1/staff/admin/tables/%d/rotate-qr", id), mgr, "", nil); code != 204 {
		t.Fatalf("rotate: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/tables/resolve?t="+tok, "", "", nil); code != 404 {
		t.Fatalf("old QR must stop working after rotation, got %d", code)
	}
}

func TestCustomerDineInUsesTableTokenOnly(t *testing.T) {
	e := setup(t)
	tok := e.tableToken("T3")
	code, out := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": tok}))
	if code != 201 || out["tableLabel"] != "T3" {
		t.Fatalf("dine-in via QR: %d %v", code, out)
	}
	// Raw table ids from customers are refused (no manual/forged selection).
	if code, _ := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableId": 1})); code != 400 {
		t.Fatalf("customer tableId must be rejected, got %d", code)
	}
	if code, out := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": strings.Repeat("0", 64)})); code != 400 || out["error"].(map[string]any)["code"] != "table_invalid" {
		t.Fatalf("unknown token: %d %v", code, out)
	}
	if code, _ := e.order(e.sandwich(map[string]any{"orderType": "dine_in"})); code != 400 {
		t.Fatalf("dine-in without token: %d", code)
	}
	e.d.Exec(`UPDATE dining_tables SET active=false WHERE label='T3'`)
	if code, out := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": tok})); code != 400 || out["error"].(map[string]any)["code"] != "table_invalid" {
		t.Fatalf("disabled table: %d %v", code, out)
	}
	if n := e.q(`SELECT count(*) FROM orders`); n != "1" {
		t.Fatalf("only the valid order should exist, got %s", n)
	}
}

func TestQRTokenFromAnotherRestaurantIsRejected(t *testing.T) {
	e := setup(t)
	e.d.Exec(`INSERT INTO branches(name) VALUES('Other Restaurant')`)
	e.d.Exec(`INSERT INTO dining_tables(branch_id,label) VALUES(2,'X1')`)
	tok := e.q(`SELECT qr_token FROM dining_tables WHERE label='X1'`)
	if code, _ := e.do("GET", "/api/v1/tables/resolve?t="+tok, "", "", nil); code != 404 {
		t.Fatalf("other restaurant's QR resolved: %d", code)
	}
	if code, _ := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": tok})); code != 400 {
		t.Fatalf("other restaurant's QR accepted for order: %d", code)
	}
}

func TestDuplicateSubmissionWithQRCreatesOneOrder(t *testing.T) {
	e := setup(t)
	body := e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": e.tableToken("T1")})
	_, a := e.do("POST", "/api/v1/orders", "", "same-key-000001", body)
	_, b := e.do("POST", "/api/v1/orders", "", "same-key-000001", body)
	if a["id"] != b["id"] || e.q(`SELECT count(*) FROM orders`) != "1" {
		t.Fatalf("duplicate submit created extra order: %v %v", a["id"], b["id"])
	}
}

func TestTableAdminRolesAndQRURL(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("GET", "/api/v1/staff/admin/tables", e.login("waiter"), "", nil); code != 403 {
		t.Fatalf("waiter must not see QR codes: %d", code)
	}
	code, out := e.do("GET", "/api/v1/staff/admin/tables", e.login("manager"), "", nil)
	first := out["tables"].([]any)[0].(map[string]any)
	if code != 200 || !strings.HasPrefix(first["qrUrl"].(string), "https://example.test/mini?t=") {
		t.Fatalf("admin list: %d %v", code, first)
	}
	if code, _ := e.do("POST", "/api/v1/staff/admin/tables", e.login("manager"), "", map[string]any{"label": "T1", "seats": 2}); code != 409 {
		t.Fatalf("duplicate label: %d", code)
	}
}

// ---------- public TV display ----------

func (e *env) displayToken() string {
	code, out := e.do("POST", "/api/v1/staff/admin/displays", e.login("manager"), "", map[string]any{"name": "Main TV"})
	if code != 201 {
		e.t.Fatalf("create display: %d", code)
	}
	return out["token"].(string)
}

func TestDisplayAuthAndRevocation(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("GET", "/api/v1/display/orders", "", "", nil); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/display/orders", e.login("manager"), "", nil); code != 401 {
		t.Fatalf("a staff token must NOT work as a display token: %d", code)
	}
	tok := e.displayToken()
	if code, _ := e.do("GET", "/api/v1/display/orders", tok, "", nil); code != 200 {
		t.Fatalf("valid display token: %d", code)
	}
	if n := e.q(`SELECT count(*) FROM display_devices WHERE token_hash=$1`, tok); n != "0" {
		t.Fatal("raw display token must not be stored")
	}
	if code, _ := e.do("POST", "/api/v1/staff/admin/displays/1/revoke", e.login("waiter"), "", nil); code != 403 {
		t.Fatalf("waiter revoke: %d", code)
	}
	e.do("POST", "/api/v1/staff/admin/displays/1/revoke", e.login("manager"), "", nil)
	if code, _ := e.do("GET", "/api/v1/display/orders", tok, "", nil); code != 401 {
		t.Fatalf("revoked token must stop working: %d", code)
	}
}

func TestDisplayShowsOnlyApprovedFields(t *testing.T) {
	e := setup(t)
	tok := e.displayToken()
	e.order(e.sandwich(map[string]any{"orderType": "delivery", "customerName": "Secret Name", "customerPhone": "0911999999", "deliveryAddress": "Private Street", "notes": "private note"}))
	_, snap := e.do("GET", "/api/v1/display/orders", tok, "", nil)
	raw, _ := json.Marshal(snap)
	for _, leak := range []string{"Secret Name", "0911999999", "Private Street", "private note", "publicToken", "total", "payment", `"id"`, "customer"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("display snapshot leaks %q: %s", leak, raw)
		}
	}
	orders := snap["orders"].([]any)
	if len(orders) != 1 {
		t.Fatalf("orders: %v", orders)
	}
	row := orders[0].(map[string]any)
	if len(row) != 2 || row["status"] != "received" {
		t.Fatalf("row should be only orderNumber+status: %v", row)
	}
}

func TestDisplayLifecycleRulesAndTableSetting(t *testing.T) {
	e := setup(t)
	tok := e.displayToken()
	_, o := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": e.tableToken("T4")}))
	id := int64(o["id"].(float64))
	count := func() (int, map[string]any) {
		_, s := e.do("GET", "/api/v1/display/orders", tok, "", nil)
		l := s["orders"].([]any)
		if len(l) > 0 {
			return len(l), l[0].(map[string]any)
		}
		return 0, nil
	}
	if _, r := count(); r["tableLabel"] != nil {
		t.Fatal("table must be hidden by default")
	}
	mgr := e.login("manager")
	e.do("PUT", "/api/v1/staff/admin/display-settings", mgr, "", map[string]any{"readyMinutes": 10, "showTable": true, "maxOrders": 40})
	if _, r := count(); r["tableLabel"] != "T4" {
		t.Fatalf("table should show after enabling: %v", r)
	}
	status := func(to string) {
		if code, out := e.do("POST", fmt.Sprintf("/api/v1/staff/orders/%d/status", id), mgr, "", map[string]any{"status": to}); code != 200 {
			t.Fatalf("-> %s: %d %v", to, code, out)
		}
	}
	status("preparing")
	if _, r := count(); r["status"] != "preparing" {
		t.Fatalf("preparing: %v", r)
	}
	status("ready")
	if _, r := count(); r["status"] != "ready" {
		t.Fatalf("ready: %v", r)
	}
	// A ready order disappears after the configured window.
	e.d.Exec(`UPDATE order_status_history SET created_at = now() - interval '11 minutes' WHERE order_id=$1 AND to_status='ready'`, id)
	if n, _ := count(); n != 0 {
		t.Fatal("ready order older than the window must leave the board")
	}
	e.d.Exec(`UPDATE order_status_history SET created_at = now() WHERE order_id=$1 AND to_status='ready'`, id)
	status("served")
	if n, _ := count(); n != 0 {
		t.Fatal("served (collected) orders must leave the board")
	}
	// Cancelled and stale orders are never shown.
	_, o2 := e.order(e.sandwich(nil))
	id2 := int64(o2["id"].(float64))
	e.do("POST", fmt.Sprintf("/api/v1/staff/orders/%d/status", id2), mgr, "", map[string]any{"status": "cancelled", "reason": "test"})
	if n, _ := count(); n != 0 {
		t.Fatal("cancelled order shown")
	}
	_, o3 := e.order(e.sandwich(nil))
	e.d.Exec(`UPDATE orders SET created_at = now() - interval '7 hours' WHERE id=$1`, int64(o3["id"].(float64)))
	if n, _ := count(); n != 0 {
		t.Fatal("stale order shown")
	}
	// Cap on the number of orders.
	e.do("PUT", "/api/v1/staff/admin/display-settings", mgr, "", map[string]any{"readyMinutes": 10, "showTable": false, "maxOrders": 10})
	for i := 0; i < 12; i++ {
		e.d.Exec(`UPDATE orders SET created_at = now() WHERE id=$1`, int64(o3["id"].(float64)))
		e.order(e.sandwich(nil))
	}
	_, s := e.do("GET", "/api/v1/display/orders", tok, "", nil)
	if len(s["orders"].([]any)) != 10 {
		t.Fatalf("cap not applied: %d", len(s["orders"].([]any)))
	}
}

func TestDisplayIsScopedToItsRestaurant(t *testing.T) {
	e := setup(t)
	tok := e.displayToken()
	e.d.Exec(`INSERT INTO branches(name) VALUES('Other')`)
	e.d.Exec(`INSERT INTO orders(branch_id,source,order_type,status,subtotal_santim,total_santim) VALUES(2,'customer','takeaway','placed',100,100)`)
	_, s := e.do("GET", "/api/v1/display/orders", tok, "", nil)
	if len(s["orders"].([]any)) != 0 {
		t.Fatal("display of restaurant 1 saw an order from restaurant 2")
	}
}

func TestKitchenDoesNotSeeCustomerContactDetails(t *testing.T) {
	e := setup(t)
	e.order(e.sandwich(map[string]any{"orderType": "delivery", "customerName": "Abebe", "customerPhone": "0911000000", "deliveryAddress": "Bole"}))
	_, out := e.do("GET", "/api/v1/staff/orders", e.login("kitchen"), "", nil)
	raw, _ := json.Marshal(out)
	for _, leak := range []string{"0911000000", "Bole", "Abebe"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("kitchen sees %q", leak)
		}
	}
	_, out = e.do("GET", "/api/v1/staff/orders", e.login("cashier"), "", nil)
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), "0911000000") {
		t.Fatal("cashier/waiter should still see delivery contact details")
	}
}

// ---------- SSE ----------

type sse struct {
	t      *testing.T
	events chan sseEvent
	cancel context.CancelFunc
	status int
}
type sseEvent struct{ ID, Name, Data string }

func (e *env) stream(path, token string, lastID string) *sse {
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	s := &sse{t: e.t, events: make(chan sseEvent, 256), cancel: cancel, status: res.StatusCode}
	if res.StatusCode != 200 {
		res.Body.Close()
		close(s.events)
		return s
	}
	go func() {
		defer res.Body.Close()
		defer close(s.events)
		sc := bufio.NewScanner(res.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Name = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.Data = line[6:]
			case line == "":
				if ev.Name != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			}
		}
	}()
	e.t.Cleanup(cancel)
	return s
}

func (s *sse) next(name string, within time.Duration) (sseEvent, bool) {
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				return sseEvent{}, false
			}
			if ev.Name == name {
				return ev, true
			}
		case <-deadline:
			return sseEvent{}, false
		}
	}
}

func (s *sse) mustReady() string {
	ev, ok := s.next("ready", 3*time.Second)
	if !ok {
		s.t.Fatal("no ready event")
	}
	return ev.Data
}

func TestStreamsRequireAuthentication(t *testing.T) {
	e := setup(t)
	if s := e.stream("/api/v1/staff/events", "", ""); s.status != 401 {
		t.Fatalf("staff stream without token: %d", s.status)
	}
	if s := e.stream("/api/v1/display/events", "", ""); s.status != 401 {
		t.Fatalf("display stream without token: %d", s.status)
	}
	if s := e.stream("/api/v1/display/events", e.login("manager"), ""); s.status != 401 {
		t.Fatalf("staff token on display stream: %d", s.status)
	}
	if s := e.stream("/api/v1/orders/track/nope/events", "", ""); s.status != 404 {
		t.Fatalf("unknown order token: %d", s.status)
	}
}

func TestOrderFlowsToKitchenTVAndCustomerInRealTime(t *testing.T) {
	e := setup(t)
	kitchen := e.login("kitchen")
	kStream := e.stream("/api/v1/staff/events", kitchen, "")
	kStream.mustReady()
	tvStream := e.stream("/api/v1/display/events", e.displayToken(), "")
	tvStream.mustReady()

	code, o := e.order(e.sandwich(map[string]any{"orderType": "dine_in", "tableToken": e.tableToken("T5")}))
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	cStream := e.stream("/api/v1/orders/track/"+o["publicToken"].(string)+"/events", "", "")
	cStream.mustReady()
	id := int64(o["id"].(float64))

	ev, ok := kStream.next("order", 3*time.Second)
	if !ok {
		t.Fatal("kitchen did not receive order.created")
	}
	var kp map[string]any
	json.Unmarshal([]byte(ev.Data), &kp)
	if kp["type"] != "order.created" || kp["status"] != "placed" {
		t.Fatalf("kitchen payload %v", kp)
	}
	if ev, ok := tvStream.next("order", 3*time.Second); !ok || strings.Contains(ev.Data, "orderId") || !strings.Contains(ev.Data, `"received"`) {
		t.Fatalf("TV event wrong or leaks internals: %v %s", ok, ev.Data)
	}

	// Kitchen starts and finishes the item: customer and TV see it.
	var itemID int64
	e.d.QueryRow(`SELECT id FROM order_items WHERE order_id=$1`, id).Scan(&itemID)
	e.do("POST", fmt.Sprintf("/api/v1/staff/orders/%d/items/%d/kitchen-status", id, itemID), kitchen, "", map[string]any{"status": "preparing"})
	e.do("POST", fmt.Sprintf("/api/v1/staff/orders/%d/items/%d/kitchen-status", id, itemID), kitchen, "", map[string]any{"status": "ready"})
	var last map[string]any
	for i := 0; i < 2; i++ {
		ev, ok := cStream.next("order", 3*time.Second)
		if !ok {
			t.Fatal("customer missed a status event")
		}
		json.Unmarshal([]byte(ev.Data), &last)
	}
	if last["status"] != "ready" {
		t.Fatalf("customer final status: %v", last)
	}
	sawReady := false
	for i := 0; i < 3 && !sawReady; i++ {
		if ev, ok := tvStream.next("order", 3*time.Second); ok && strings.Contains(ev.Data, `"ready"`) {
			sawReady = true
		}
	}
	if !sawReady {
		t.Fatal("TV never saw ready")
	}
}

func TestEventsAreScopedByRestaurantAndByOrder(t *testing.T) {
	e := setup(t)
	e.d.Exec(`INSERT INTO branches(name) VALUES('Other')`)
	e.d.Exec(`INSERT INTO staff_users(branch_id,username,full_name,password_hash,role) SELECT 2,'other_kitchen','Other',password_hash,'kitchen' FROM staff_users WHERE username='kitchen'`)
	other := e.stream("/api/v1/staff/events", e.login("other_kitchen"), "")
	other.mustReady()
	mine := e.stream("/api/v1/staff/events", e.login("kitchen"), "")
	mine.mustReady()

	_, a := e.order(e.sandwich(nil))
	_, b := e.order(e.sandwich(nil))
	custA := e.stream("/api/v1/orders/track/"+a["publicToken"].(string)+"/events", "", "")
	custA.mustReady()
	mgr := e.login("manager")
	e.do("POST", fmt.Sprintf("/api/v1/staff/orders/%d/status", int64(b["id"].(float64))), mgr, "", map[string]any{"status": "preparing"}) // change to order B
	if _, ok := custA.next("order", 700*time.Millisecond); ok {
		t.Fatal("customer A received an event about order B")
	}
	if _, ok := mine.next("order", 2*time.Second); !ok {
		t.Fatal("same-restaurant staff should receive events")
	}
	if ev, ok := other.next("order", 700*time.Millisecond); ok {
		t.Fatalf("another restaurant's staff received %s", ev.Data)
	}
}

func TestReconnectReplaysMissedEventsAndResyncs(t *testing.T) {
	e := setup(t)
	k := e.login("kitchen")
	s1 := e.stream("/api/v1/staff/events", k, "")
	s1.mustReady()
	e.order(e.sandwich(nil))
	ev, ok := s1.next("order", 3*time.Second)
	if !ok {
		t.Fatal("no event")
	}
	s1.cancel() // network drops
	e.order(e.sandwich(nil))
	e.order(e.sandwich(nil)) // two events happen while disconnected

	s2 := e.stream("/api/v1/staff/events", k, ev.ID)
	got := 0
	for got < 2 {
		r, ok := s2.next("order", 3*time.Second)
		if !ok {
			t.Fatalf("replayed only %d of 2 missed events", got)
		}
		if r.ID <= ev.ID {
			t.Fatalf("replay repeated an old event %s", r.ID)
		}
		got++
	}
	// Too many missed events -> resync instead of a huge replay.
	s2.cancel()
	e.d.Exec(`INSERT INTO events(branch_id,type,order_id,order_number,order_type,status,payment_status)
		SELECT 1,'order.updated',o.id,o.order_number,'takeaway','placed','unpaid' FROM orders o, generate_series(1,$1) LIMIT $1`, realtime.MaxReplay+5)
	s3 := e.stream("/api/v1/staff/events", k, ev.ID)
	if _, ok := s3.next("resync", 3*time.Second); !ok {
		t.Fatal("expected resync after a large gap")
	}
	// A client holding an id from a wiped database is told to resync, not left waiting.
	s4 := e.stream("/api/v1/staff/events", k, "99999999")
	if _, ok := s4.next("resync", 3*time.Second); !ok {
		t.Fatal("expected resync for unknown future id")
	}
}

func TestStreamEndsAfterLogoutAndRevocation(t *testing.T) {
	e := setup(t)
	w := e.login("waiter")
	ws := e.stream("/api/v1/staff/events", w, "")
	ws.mustReady()
	e.do("POST", "/api/v1/auth/logout", w, "", nil)
	if _, ok := ws.next("unauthorized", 3*time.Second); !ok {
		t.Fatal("staff stream should end with unauthorized after logout")
	}
	tok := e.displayToken()
	ds := e.stream("/api/v1/display/events", tok, "")
	ds.mustReady()
	e.do("POST", "/api/v1/staff/admin/displays/1/revoke", e.login("manager"), "", nil)
	if _, ok := ds.next("unauthorized", 3*time.Second); !ok {
		t.Fatal("display stream should end after revocation")
	}
}

func TestNoEventForRolledBackOrderAndStreamsCleanUp(t *testing.T) {
	e := setup(t)
	before := e.q(`SELECT count(*) FROM events`)
	e.order(map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": 999999, "quantity": 1}}}) // fails validation
	if e.q(`SELECT count(*) FROM events`) != before {
		t.Fatal("failed order must not record an event")
	}
	s := e.stream("/api/v1/staff/events", e.login("kitchen"), "")
	s.mustReady()
	if e.hub.Subscribers() != 1 {
		t.Fatalf("subscribers = %d", e.hub.Subscribers())
	}
	s.cancel()
	deadline := time.Now().Add(3 * time.Second)
	for e.hub.Subscribers() != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if e.hub.Subscribers() != 0 {
		t.Fatal("subscriber leaked after disconnect")
	}
}
