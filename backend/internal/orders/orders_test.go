package orders_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"tastybites/internal/auth"
	"tastybites/internal/db"
	"tastybites/internal/menu"
	"tastybites/internal/orders"
	"tastybites/internal/seed"
)

type env struct {
	t   *testing.T
	d   *sql.DB
	mux *http.ServeMux
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
	d.Exec(`DROP SEQUENCE IF EXISTS order_number_seq`)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(d, "test-password-1"); err != nil {
		t.Fatal(err)
	}
	a := &auth.Service{DB: d}
	m := &menu.Service{DB: d, BranchID: 1}
	o := &orders.Service{DB: d, BranchID: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", a.Login)
	mux.HandleFunc("GET /api/v1/menu", m.Public)
	mux.HandleFunc("PATCH /api/v1/staff/menu/items/{id}", a.Require("admin", "manager")(m.PatchItem))
	mux.HandleFunc("POST /api/v1/orders", o.CustomerCreate)
	mux.HandleFunc("GET /api/v1/orders/track/{token}", o.Track)
	mux.HandleFunc("POST /api/v1/staff/orders/{id}/status", a.Require()(o.SetStatus))
	mux.HandleFunc("POST /api/v1/staff/orders/{id}/payments/cash", a.Require("admin", "manager", "cashier")(o.CashPayment))
	return &env{t, d, mux}
}

func (e *env) do(method, path, token, idem string, body any) (int, map[string]any) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *env) login(user string) string {
	code, out := e.do("POST", "/api/v1/auth/login", "", "", map[string]string{"username": user, "password": "test-password-1"})
	if code != 200 {
		e.t.Fatalf("login %s: %d %v", user, code, out)
	}
	return out["token"].(string)
}

func (e *env) id(name string) int64 {
	var id int64
	if err := e.d.QueryRow(`SELECT id FROM menu_items WHERE name=$1`, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestServerSidePricingIgnoresClientPrices(t *testing.T) {
	e := setup(t)
	var extra int64
	e.d.QueryRow(`SELECT id FROM item_extras WHERE name='Extra cheese'`).Scan(&extra)
	// Client tries to smuggle a price; unknown fields are rejected.
	code, _ := e.do("POST", "/api/v1/orders", "", "key-price-0001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Cheese Burger"), "quantity": 1, "priceSantim": 1}}})
	if code != 400 {
		t.Fatalf("unknown price field should be rejected, got %d", code)
	}
	code, out := e.do("POST", "/api/v1/orders", "", "key-price-0002", map[string]any{"orderType": "takeaway", "items": []map[string]any{
		{"menuItemId": e.id("Cheese Burger"), "quantity": 2, "extraIds": []int64{extra}}}})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	// (22000 + 3000) * 2 = 50000
	if got := out["totalSantim"].(float64); got != 50000 {
		t.Fatalf("total = %v, want 50000", got)
	}
}

func TestVariantRequiredAndPriced(t *testing.T) {
	e := setup(t)
	code, _ := e.do("POST", "/api/v1/orders", "", "key-var-00001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Coffee"), "quantity": 1}}})
	if code != 400 {
		t.Fatalf("missing variant should be 400, got %d", code)
	}
	var vid int64
	e.d.QueryRow(`SELECT id FROM item_variants WHERE name='Large' AND item_id=$1`, e.id("Coffee")).Scan(&vid)
	code, out := e.do("POST", "/api/v1/orders", "", "key-var-00002", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Coffee"), "variantId": vid, "quantity": 3}}})
	if code != 201 || out["totalSantim"].(float64) != 18000 {
		t.Fatalf("variant pricing wrong: %d %v", code, out["totalSantim"])
	}
}

func TestIdempotentCreation(t *testing.T) {
	e := setup(t)
	body := map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Shiro"), "variantId": nil, "quantity": 1}}}
	var vid int64
	e.d.QueryRow(`SELECT id FROM item_variants WHERE item_id=$1 AND name='Regular'`, e.id("Shiro")).Scan(&vid)
	body["items"] = []map[string]any{{"menuItemId": e.id("Shiro"), "variantId": vid, "quantity": 1}}
	_, a := e.do("POST", "/api/v1/orders", "", "key-idem-0001", body)
	_, b := e.do("POST", "/api/v1/orders", "", "key-idem-0001", body)
	if a["id"] != b["id"] {
		t.Fatalf("retry created a second order: %v vs %v", a["id"], b["id"])
	}
	var n int
	e.d.QueryRow(`SELECT count(*) FROM orders`).Scan(&n)
	if n != 1 {
		t.Fatalf("orders = %d, want 1", n)
	}
	body["notes"] = "changed"
	code, _ := e.do("POST", "/api/v1/orders", "", "key-idem-0001", body)
	if code != 409 {
		t.Fatalf("same key with different body should be 409, got %d", code)
	}
}

func TestUnavailableItemRejectedAndHiddenFromMenu(t *testing.T) {
	e := setup(t)
	mgr := e.login("manager")
	id := e.id("Fresh Juice")
	if code, _ := e.do("PATCH", "/api/v1/staff/menu/items/"+itoa(id), mgr, "", map[string]any{"available": false}); code != 204 {
		t.Fatalf("patch: %d", code)
	}
	code, _ := e.do("POST", "/api/v1/orders", "", "key-unav-0001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": id, "quantity": 1}}})
	if code != 409 {
		t.Fatalf("want 409 for unavailable, got %d", code)
	}
	_, menuOut := e.do("GET", "/api/v1/menu", "", "", nil)
	if bytes.Contains(mustJSON(menuOut), []byte("Fresh Juice")) {
		t.Fatal("unavailable item leaked to public menu")
	}
}

func TestDineInNeedsTableAndDeliveryNeedsAddress(t *testing.T) {
	e := setup(t)
	items := []map[string]any{{"menuItemId": e.id("Chicken Sandwich"), "quantity": 1}}
	if code, _ := e.do("POST", "/api/v1/orders", "", "key-dine-0001", map[string]any{"orderType": "dine_in", "items": items}); code != 400 {
		t.Fatalf("dine-in without table: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/orders", "", "key-del-00001", map[string]any{"orderType": "delivery", "items": items}); code != 400 {
		t.Fatalf("delivery without address: %d", code)
	}
}

func TestStateMachineAndRoles(t *testing.T) {
	e := setup(t)
	_, o := e.do("POST", "/api/v1/orders", "", "key-flow-0001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Chicken Sandwich"), "quantity": 1}}})
	path := "/api/v1/staff/orders/" + itoa(int64(o["id"].(float64)))
	waiter, kitchen, cashier := e.login("waiter"), e.login("kitchen"), e.login("cashier")

	if code, _ := e.do("POST", path+"/status", waiter, "", map[string]any{"status": "preparing"}); code != 409 {
		t.Fatalf("waiter must not start cooking: %d", code)
	}
	if code, _ := e.do("POST", path+"/status", kitchen, "", map[string]any{"status": "preparing"}); code != 200 {
		t.Fatalf("kitchen preparing: %d", code)
	}
	if code, _ := e.do("POST", path+"/status", kitchen, "", map[string]any{"status": "completed"}); code != 409 {
		t.Fatalf("cannot skip ready: %d", code)
	}
	e.do("POST", path+"/status", kitchen, "", map[string]any{"status": "ready"})
	if code, _ := e.do("POST", path+"/status", cashier, "", map[string]any{"status": "completed"}); code != 200 {
		t.Fatalf("cashier complete: %d", code)
	}
	if code, _ := e.do("POST", path+"/status", e.login("manager"), "", map[string]any{"status": "preparing"}); code != 409 {
		t.Fatalf("completed order must be terminal: %d", code)
	}
	if code, _ := e.do("POST", path+"/status", "", "", map[string]any{"status": "ready"}); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
}

func TestCashPaymentUsesServerTotalAndCannotRepeat(t *testing.T) {
	e := setup(t)
	_, o := e.do("POST", "/api/v1/orders", "", "key-cash-0001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Chicken Sandwich"), "quantity": 2}}})
	path := "/api/v1/staff/orders/" + itoa(int64(o["id"].(float64)))
	if code, _ := e.do("POST", path+"/payments/cash", e.login("waiter"), "", nil); code != 403 {
		t.Fatalf("waiter cannot take payment: %d", code)
	}
	cashier := e.login("cashier")
	code, out := e.do("POST", path+"/payments/cash", cashier, "", nil)
	if code != 200 || out["paymentStatus"] != "paid" {
		t.Fatalf("cash: %d %v", code, out)
	}
	var amt int64
	e.d.QueryRow(`SELECT amount_santim FROM payments`).Scan(&amt)
	if amt != 32000 {
		t.Fatalf("payment amount %d", amt)
	}
	if code, _ := e.do("POST", path+"/payments/cash", cashier, "", nil); code != 409 {
		t.Fatalf("double payment must be rejected: %d", code)
	}
}

func TestCancelRequiresReasonAndIsAudited(t *testing.T) {
	e := setup(t)
	_, o := e.do("POST", "/api/v1/orders", "", "key-canc-0001", map[string]any{"orderType": "takeaway", "items": []map[string]any{{"menuItemId": e.id("Chicken Sandwich"), "quantity": 1}}})
	path := "/api/v1/staff/orders/" + itoa(int64(o["id"].(float64)))
	mgr := e.login("manager")
	if code, _ := e.do("POST", path+"/status", mgr, "", map[string]any{"status": "cancelled"}); code != 400 {
		t.Fatalf("reason required: %d", code)
	}
	if code, _ := e.do("POST", path+"/status", mgr, "", map[string]any{"status": "cancelled", "reason": "customer left"}); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	var n int
	e.d.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='order.cancel'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	e := setup(t)
	code, _ := e.do("POST", "/api/v1/auth/login", "", "", map[string]string{"username": "manager", "password": "wrong"})
	if code != 401 {
		t.Fatalf("got %d", code)
	}
}

func itoa(n int64) string   { b, _ := json.Marshal(n); return string(b) }
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
