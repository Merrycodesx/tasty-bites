// Package orders implements order creation (server-side pricing), status rules,
// kitchen updates and cash payments.
package orders

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tastybites/internal/auth"
	"tastybites/internal/httpx"
	"tastybites/internal/realtime"
)

type Service struct {
	DB       *sql.DB
	BranchID int64         // default branch for customer-created orders
	Hub      *realtime.Hub // optional; nil disables live publishing (events are still recorded)
}

func (s *Service) publish(evs ...realtime.Event) {
	if s.Hub != nil {
		s.Hub.Publish(evs...)
	}
}

type LineIn struct {
	MenuItemID int64   `json:"menuItemId"`
	VariantID  *int64  `json:"variantId"`
	ExtraIDs   []int64 `json:"extraIds"`
	Quantity   int     `json:"quantity"`
	Note       string  `json:"note"`
}
type CreateIn struct {
	OrderType       string   `json:"orderType"`
	TableID         *int64   `json:"tableId"`    // staff only; customers must use TableToken
	TableToken      string   `json:"tableToken"` // customers: secret from the table QR code
	CustomerName    string   `json:"customerName"`
	CustomerPhone   string   `json:"customerPhone"`
	DeliveryAddress string   `json:"deliveryAddress"`
	Notes           string   `json:"notes"`
	Items           []LineIn `json:"items"`
}

type Option struct {
	Name        string `json:"name"`
	PriceSantim int64  `json:"priceSantim"`
}
type OrderItem struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	VariantName   string   `json:"variantName"`
	Quantity      int      `json:"quantity"`
	UnitSantim    int64    `json:"unitPriceSantim"`
	LineSantim    int64    `json:"lineTotalSantim"`
	Note          string   `json:"note"`
	KitchenStatus string   `json:"kitchenStatus"`
	Options       []Option `json:"options"`
}
type Order struct {
	ID              int64       `json:"id"`
	OrderNumber     int64       `json:"orderNumber"`
	PublicToken     string      `json:"publicToken,omitempty"`
	Source          string      `json:"source"`
	OrderType       string      `json:"orderType"`
	TableID         *int64      `json:"tableId"`
	TableLabel      string      `json:"tableLabel"`
	CustomerName    string      `json:"customerName"`
	CustomerPhone   string      `json:"customerPhone,omitempty"`
	DeliveryAddress string      `json:"deliveryAddress,omitempty"`
	Notes           string      `json:"notes"`
	Status          string      `json:"status"`
	PaymentStatus   string      `json:"paymentStatus"`
	TotalSantim     int64       `json:"totalSantim"`
	CreatedAt       time.Time   `json:"createdAt"`
	Items           []OrderItem `json:"items"`
}

func bad(msg string) error { return httpx.Err(400, "invalid_order", msg) }

// build validates the request against the DB and prices it. Nothing client-supplied is trusted for money.
func (s *Service) create(r *http.Request, branch int64, source string, actor *int64, in CreateIn, idemKey string) (int64, string, error) {
	switch in.OrderType {
	case "dine_in", "takeaway", "delivery":
	default:
		return 0, "", bad("Choose dine-in, takeaway or delivery.")
	}
	if len(in.Items) == 0 || len(in.Items) > 40 {
		return 0, "", bad("Add between 1 and 40 items.")
	}
	if len(in.Notes) > 500 || len(in.CustomerName) > 80 || len(in.CustomerPhone) > 20 || len(in.DeliveryAddress) > 300 {
		return 0, "", bad("Some text is too long.")
	}
	if in.OrderType == "dine_in" && in.TableID == nil {
		return 0, "", bad("Choose a table for dine-in.")
	}
	if in.OrderType == "delivery" && (strings.TrimSpace(in.DeliveryAddress) == "" || strings.TrimSpace(in.CustomerPhone) == "") {
		return 0, "", bad("Delivery needs an address and phone number.")
	}
	if in.OrderType != "dine_in" {
		in.TableID = nil
	}

	body, _ := json.Marshal(in)
	sum := sha256.Sum256(body)
	reqHash := hex.EncodeToString(sum[:])
	scope := source + ":" + strconv.FormatInt(branch, 10)

	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()

	if idemKey != "" {
		// Serialise concurrent retries of the same key.
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, scope+idemKey); err != nil {
			return 0, "", err
		}
		var h string
		var oid sql.NullInt64
		err := tx.QueryRow(`SELECT request_hash, order_id FROM idempotency_records WHERE scope=$1 AND key=$2`, scope, idemKey).Scan(&h, &oid)
		if err == nil {
			if h != reqHash {
				return 0, "", httpx.Err(409, "idempotency_conflict", "This request key was already used with different contents.")
			}
			var tok string
			if err := tx.QueryRow(`SELECT public_token FROM orders WHERE id=$1`, oid.Int64).Scan(&tok); err != nil {
				return 0, "", err
			}
			return oid.Int64, tok, nil
		} else if err != sql.ErrNoRows {
			return 0, "", err
		}
	}

	if in.TableID != nil {
		var ok bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM dining_tables WHERE id=$1 AND branch_id=$2 AND active)`, *in.TableID, branch).Scan(&ok); err != nil {
			return 0, "", err
		}
		if !ok {
			return 0, "", bad("That table does not exist.")
		}
	}

	type priced struct {
		item    LineIn
		name    string
		variant string
		unit    int64
		opts    []Option
	}
	var lines []priced
	var subtotal int64
	for _, l := range in.Items {
		if l.Quantity < 1 || l.Quantity > 50 || len(l.Note) > 200 || len(l.ExtraIDs) > 10 {
			return 0, "", bad("Check item quantities and notes.")
		}
		var name string
		var price int64
		var avail bool
		err := tx.QueryRow(`SELECT name, price_santim, available FROM menu_items WHERE id=$1 AND branch_id=$2 AND NOT archived`, l.MenuItemID, branch).Scan(&name, &price, &avail)
		if err == sql.ErrNoRows {
			return 0, "", bad("An item on your order is no longer on the menu.")
		} else if err != nil {
			return 0, "", err
		}
		if !avail {
			return 0, "", httpx.Err(409, "item_unavailable", name+" is currently unavailable. Remove it and try again.")
		}
		p := priced{item: l, name: name, unit: price}
		if l.VariantID != nil {
			var vn string
			var vp int64
			var va bool
			err := tx.QueryRow(`SELECT name, price_santim, available FROM item_variants WHERE id=$1 AND item_id=$2`, *l.VariantID, l.MenuItemID).Scan(&vn, &vp, &va)
			if err == sql.ErrNoRows {
				return 0, "", bad("A selected size or variant is not valid.")
			} else if err != nil {
				return 0, "", err
			}
			if !va {
				return 0, "", httpx.Err(409, "item_unavailable", name+" ("+vn+") is currently unavailable.")
			}
			p.variant, p.unit = vn, vp
		} else {
			var n int
			tx.QueryRow(`SELECT count(*) FROM item_variants WHERE item_id=$1 AND available`, l.MenuItemID).Scan(&n)
			if n > 0 {
				return 0, "", bad("Choose a size for " + name + ".")
			}
		}
		seen := map[int64]bool{}
		for _, eid := range l.ExtraIDs {
			if seen[eid] {
				return 0, "", bad("An extra was selected twice.")
			}
			seen[eid] = true
			var en string
			var ep int64
			var ea bool
			err := tx.QueryRow(`SELECT name, price_santim, available FROM item_extras WHERE id=$1 AND item_id=$2`, eid, l.MenuItemID).Scan(&en, &ep, &ea)
			if err == sql.ErrNoRows {
				return 0, "", bad("A selected extra is not valid.")
			} else if err != nil {
				return 0, "", err
			}
			if !ea {
				return 0, "", httpx.Err(409, "item_unavailable", en+" is currently unavailable.")
			}
			p.opts = append(p.opts, Option{en, ep})
			p.unit += ep
		}
		subtotal += p.unit * int64(l.Quantity)
		lines = append(lines, p)
	}

	var orderID int64
	var token string
	err = tx.QueryRow(`INSERT INTO orders(branch_id,source,order_type,table_id,customer_name,customer_phone,delivery_address,notes,status,subtotal_santim,total_santim,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'placed',$9,$9,$10) RETURNING id, public_token`,
		branch, source, in.OrderType, in.TableID, strings.TrimSpace(in.CustomerName), strings.TrimSpace(in.CustomerPhone),
		strings.TrimSpace(in.DeliveryAddress), strings.TrimSpace(in.Notes), subtotal, actor).Scan(&orderID, &token)
	if err != nil {
		return 0, "", err
	}
	for _, p := range lines {
		var itemID int64
		err := tx.QueryRow(`INSERT INTO order_items(order_id,menu_item_id,name,variant_name,quantity,unit_price_santim,line_total_santim,note)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, orderID, p.item.MenuItemID, p.name, p.variant, p.item.Quantity, p.unit, p.unit*int64(p.item.Quantity), strings.TrimSpace(p.item.Note)).Scan(&itemID)
		if err != nil {
			return 0, "", err
		}
		for _, o := range p.opts {
			if _, err := tx.Exec(`INSERT INTO order_item_options(order_item_id,name,price_santim) VALUES($1,$2,$3)`, itemID, o.Name, o.PriceSantim); err != nil {
				return 0, "", err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO order_status_history(order_id,from_status,to_status,actor_id) VALUES($1,NULL,'placed',$2)`, orderID, actor); err != nil {
		return 0, "", err
	}
	ev, err := realtime.Record(tx, "order.created", orderID)
	if err != nil {
		return 0, "", err
	}
	if idemKey != "" {
		if _, err := tx.Exec(`INSERT INTO idempotency_records(key,scope,request_hash,order_id) VALUES($1,$2,$3,$4)`, idemKey, scope, reqHash, orderID); err != nil {
			return 0, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	s.publish(ev) // only after the order is durably committed
	return orderID, token, nil
}

// CustomerCreate is the unauthenticated Mini-App endpoint. Idempotency-Key is mandatory.
func (s *Service) CustomerCreate(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 100 {
		httpx.Fail(w, httpx.Err(400, "idempotency_key_required", "Missing Idempotency-Key header."))
		return
	}
	var in CreateIn
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.TableID != nil {
		httpx.Fail(w, bad("Scan the QR code on your table to order for a table."))
		return
	}
	if in.OrderType == "dine_in" {
		var tid int64
		err := s.DB.QueryRow(`SELECT id FROM dining_tables WHERE qr_token=$1 AND active AND branch_id=$2`, in.TableToken, s.BranchID).Scan(&tid)
		if err == sql.ErrNoRows || in.TableToken == "" {
			httpx.Fail(w, httpx.Err(400, "table_invalid", "This table QR code is not valid. Please scan the code on your table again or ask staff."))
			return
		} else if err != nil {
			httpx.Fail(w, err)
			return
		}
		in.TableID = &tid
	}
	in.TableToken = "" // never stored or hashed
	id, tok, err := s.create(r, s.BranchID, "customer", nil, in, key)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	o, err := s.fetch(id, true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	o.PublicToken = tok
	httpx.JSON(w, 201, o)
}

func (s *Service) StaffCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateIn
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	u := auth.From(r)
	if in.TableToken != "" {
		httpx.Fail(w, bad("Staff orders use tableId."))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	id, _, err := s.create(r, u.BranchID, "staff", &u.ID, in, key)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	o, err := s.fetchFor(id, u.Role)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 201, o)
}

func (s *Service) fetch(id int64, forCustomer bool) (*Order, error) {
	var o Order
	err := s.DB.QueryRow(`SELECT o.id,o.order_number,o.source,o.order_type,o.table_id,COALESCE(t.label,''),o.customer_name,o.customer_phone,o.delivery_address,o.notes,o.status,o.payment_status,o.total_santim,o.created_at
		FROM orders o LEFT JOIN dining_tables t ON t.id=o.table_id WHERE o.id=$1`, id).Scan(&o.ID, &o.OrderNumber, &o.Source, &o.OrderType, &o.TableID, &o.TableLabel, &o.CustomerName, &o.CustomerPhone, &o.DeliveryAddress, &o.Notes, &o.Status, &o.PaymentStatus, &o.TotalSantim, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, httpx.Err(404, "not_found", "Order not found.")
	} else if err != nil {
		return nil, err
	}
	o.Items = []OrderItem{}
	rows, err := s.DB.Query(`SELECT id,name,variant_name,quantity,unit_price_santim,line_total_santim,note,kitchen_status FROM order_items WHERE order_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.Name, &it.VariantName, &it.Quantity, &it.UnitSantim, &it.LineSantim, &it.Note, &it.KitchenStatus); err != nil {
			rows.Close()
			return nil, err
		}
		it.Options = []Option{}
		o.Items = append(o.Items, it)
	}
	rows.Close()
	for i := range o.Items {
		orows, err := s.DB.Query(`SELECT name,price_santim FROM order_item_options WHERE order_item_id=$1 ORDER BY id`, o.Items[i].ID)
		if err != nil {
			return nil, err
		}
		for orows.Next() {
			var op Option
			orows.Scan(&op.Name, &op.PriceSantim)
			o.Items[i].Options = append(o.Items[i].Options, op)
		}
		orows.Close()
	}
	if forCustomer {
		o.CustomerPhone, o.DeliveryAddress = "", ""
	}
	return &o, nil
}

// fetchFor returns an order for a staff role. The kitchen does not need customer contact details.
func (s *Service) fetchFor(id int64, role string) (*Order, error) {
	o, err := s.fetch(id, false)
	if err == nil && role == "kitchen" {
		o.CustomerPhone, o.DeliveryAddress, o.CustomerName = "", "", ""
	}
	return o, err
}

// Track lets a customer follow an order using the unguessable token returned at creation.
func (s *Service) Track(w http.ResponseWriter, r *http.Request) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM orders WHERE public_token=$1`, r.PathValue("token")).Scan(&id)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(404, "not_found", "We could not find that order."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	o, err := s.fetch(id, true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, o)
}

func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	filter := `o.status NOT IN ('completed','cancelled')`
	if r.URL.Query().Get("scope") == "all" {
		filter = `o.created_at > now() - interval '2 days'`
	}
	rows, err := s.DB.Query(`SELECT o.id FROM orders o WHERE o.branch_id=$1 AND `+filter+` ORDER BY o.created_at`, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*Order{}
	for _, id := range ids {
		o, err := s.fetchFor(id, u.Role)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		out = append(out, o)
	}
	httpx.JSON(w, 200, map[string]any{"orders": out})
}

func (s *Service) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	o, err := s.owned(id, auth.From(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, o)
}

func (s *Service) owned(id int64, u auth.User) (*Order, error) {
	var b int64
	if err := s.DB.QueryRow(`SELECT branch_id FROM orders WHERE id=$1`, id).Scan(&b); err != nil || b != u.BranchID {
		return nil, httpx.Err(404, "not_found", "Order not found.")
	}
	return s.fetchFor(id, u.Role)
}

// allowedTransition encodes the order state machine and who may drive each step.
func allowedTransition(orderType, from, to, role string) bool {
	type key struct{ from, to string }
	roles := map[key][]string{
		{"placed", "preparing"}:    {"kitchen", "manager", "admin"},
		{"preparing", "ready"}:     {"kitchen", "manager", "admin"},
		{"ready", "served"}:        {"waiter", "manager", "admin"},
		{"ready", "completed"}:     {"waiter", "cashier", "manager", "admin"},
		{"served", "completed"}:    {"waiter", "cashier", "manager", "admin"},
		{"placed", "cancelled"}:    {"waiter", "manager", "admin"},
		{"preparing", "cancelled"}: {"manager", "admin"},
		{"ready", "cancelled"}:     {"manager", "admin"},
	}
	rs, ok := roles[key{from, to}]
	if !ok {
		return false
	}
	if to == "served" && orderType != "dine_in" {
		return false
	}
	for _, r := range rs {
		if r == role {
			return true
		}
	}
	return false
}

func (s *Service) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	u := auth.From(r)
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback()
	var from, otype, pay string
	err = tx.QueryRow(`SELECT status, order_type, payment_status FROM orders WHERE id=$1 AND branch_id=$2 FOR UPDATE`, id, u.BranchID).Scan(&from, &otype, &pay)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(404, "not_found", "Order not found."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !allowedTransition(otype, from, in.Status, u.Role) {
		httpx.Fail(w, httpx.Err(409, "invalid_transition", "That status change is not allowed for this order or your role."))
		return
	}
	if in.Status == "cancelled" {
		if strings.TrimSpace(in.Reason) == "" {
			httpx.Fail(w, httpx.Err(400, "reason_required", "Give a reason for cancelling."))
			return
		}
		if pay == "paid" {
			httpx.Fail(w, httpx.Err(409, "paid_order", "A paid order cannot be cancelled here. Refunds are not available yet."))
			return
		}
	}
	if _, err := tx.Exec(`UPDATE orders SET status=$2, updated_at=now() WHERE id=$1`, id, in.Status); err != nil {
		httpx.Fail(w, err)
		return
	}
	if _, err := tx.Exec(`INSERT INTO order_status_history(order_id,from_status,to_status,actor_id,reason) VALUES($1,$2,$3,$4,$5)`, id, from, in.Status, u.ID, strings.TrimSpace(in.Reason)); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Status == "cancelled" {
		tx.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'order.cancel','order',$2,$3)`, u.ID, id, in.Reason)
	}
	ev, err := realtime.Record(tx, "order.updated", id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.publish(ev)
	o, _ := s.fetchFor(id, u.Role)
	httpx.JSON(w, 200, o)
}

// SetKitchenStatus updates one line. When every line is ready the order moves to ready automatically.
func (s *Service) SetKitchenStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	itemID, _ := strconv.ParseInt(r.PathValue("itemId"), 10, 64)
	var in struct {
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Status != "preparing" && in.Status != "ready" {
		httpx.Fail(w, httpx.Err(400, "invalid_status", "Use preparing or ready."))
		return
	}
	u := auth.From(r)
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback()
	var ostatus string
	err = tx.QueryRow(`SELECT status FROM orders WHERE id=$1 AND branch_id=$2 FOR UPDATE`, id, u.BranchID).Scan(&ostatus)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(404, "not_found", "Order not found."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	if ostatus != "placed" && ostatus != "preparing" {
		httpx.Fail(w, httpx.Err(409, "invalid_transition", "This order is no longer in the kitchen."))
		return
	}
	res, err := tx.Exec(`UPDATE order_items SET kitchen_status=$3 WHERE id=$1 AND order_id=$2`, itemID, id, in.Status)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Fail(w, httpx.Err(404, "not_found", "Item not found on this order."))
		return
	}
	next := ostatus
	if ostatus == "placed" {
		next = "preparing"
	}
	var notReady int
	tx.QueryRow(`SELECT count(*) FROM order_items WHERE order_id=$1 AND kitchen_status<>'ready'`, id).Scan(&notReady)
	if notReady == 0 {
		next = "ready"
	}
	if next != ostatus {
		tx.Exec(`UPDATE orders SET status=$2, updated_at=now() WHERE id=$1`, id, next)
		tx.Exec(`INSERT INTO order_status_history(order_id,from_status,to_status,actor_id,reason) VALUES($1,$2,$3,$4,'kitchen item update')`, id, ostatus, next, u.ID)
	}
	ev, err := realtime.Record(tx, "order.updated", id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.publish(ev)
	o, _ := s.fetchFor(id, u.Role)
	httpx.JSON(w, 200, o)
}

// CashPayment records a cash payment for the full order total. The amount is never taken from the client.
func (s *Service) CashPayment(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := auth.From(r)
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer tx.Rollback()
	var total int64
	var status, pay string
	err = tx.QueryRow(`SELECT total_santim,status,payment_status FROM orders WHERE id=$1 AND branch_id=$2 FOR UPDATE`, id, u.BranchID).Scan(&total, &status, &pay)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(404, "not_found", "Order not found."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	if status == "cancelled" {
		httpx.Fail(w, httpx.Err(409, "cancelled", "This order was cancelled."))
		return
	}
	if pay == "paid" {
		httpx.Fail(w, httpx.Err(409, "already_paid", "This order is already paid."))
		return
	}
	if _, err := tx.Exec(`INSERT INTO payments(order_id,provider,amount_santim,status,recorded_by,paid_at) VALUES($1,'cash',$2,'paid',$3,now())`, id, total, u.ID); err != nil {
		httpx.Fail(w, err)
		return
	}
	tx.Exec(`UPDATE orders SET payment_status='paid', updated_at=now() WHERE id=$1`, id)
	tx.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'payment.cash','order',$2,$3)`, u.ID, id, strconv.FormatInt(total, 10))
	ev, err := realtime.Record(tx, "payment.updated", id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.publish(ev)
	o, _ := s.fetchFor(id, u.Role)
	httpx.JSON(w, 200, o)
}

func (s *Service) Tables(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	rows, err := s.DB.Query(`SELECT t.id,t.label,t.seats,
		EXISTS(SELECT 1 FROM orders o WHERE o.table_id=t.id AND o.status NOT IN ('completed','cancelled')) FROM dining_tables t WHERE t.branch_id=$1 AND t.active ORDER BY t.id`, u.BranchID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()
	type T struct {
		ID       int64  `json:"id"`
		Label    string `json:"label"`
		Seats    int    `json:"seats"`
		Occupied bool   `json:"occupied"`
	}
	out := []T{}
	for rows.Next() {
		var t T
		rows.Scan(&t.ID, &t.Label, &t.Seats, &t.Occupied)
		out = append(out, t)
	}
	httpx.JSON(w, 200, map[string]any{"tables": out})
}

// Summary powers the manager dashboard from real paid-order data.
func (s *Service) Summary(w http.ResponseWriter, r *http.Request) {
	u := auth.From(r)
	period := r.URL.Query().Get("period")
	if period != "month" && period != "all" { period = "today" }
	var orders, paidOrders int
	var paidTotal, avg int64
	// Report periods follow the restaurant's local calendar. Revenue is grouped by actual settlement time.
	orderPeriod, paymentPeriod := `TRUE`, `TRUE`
	bucket := `to_char(timezone(b.timezone,p.paid_at), 'YYYY-MM')`
	if period == "today" {
		orderPeriod = `o.created_at >= (date_trunc('day', timezone(b.timezone, now())) AT TIME ZONE b.timezone) AND o.created_at < now()`
		paymentPeriod = `p.paid_at >= (date_trunc('day', timezone(b.timezone, now())) AT TIME ZONE b.timezone) AND p.paid_at < now()`
		bucket = `to_char(timezone(b.timezone,p.paid_at), 'HH24:00')`
	} else if period == "month" {
		orderPeriod = `o.created_at >= (date_trunc('month', timezone(b.timezone, now())) AT TIME ZONE b.timezone) AND o.created_at < now()`
		paymentPeriod = `p.paid_at >= (date_trunc('month', timezone(b.timezone, now())) AT TIME ZONE b.timezone) AND p.paid_at < now()`
		bucket = `to_char(timezone(b.timezone,p.paid_at), 'YYYY-MM-DD')`
	}
	s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM orders o JOIN branches b ON b.id=o.branch_id WHERE o.branch_id=$1 AND o.status<>'cancelled' AND `+orderPeriod+`),
		(SELECT count(DISTINCT o.id) FROM orders o JOIN payments p ON p.order_id=o.id JOIN branches b ON b.id=o.branch_id WHERE o.branch_id=$1 AND o.status<>'cancelled' AND p.status='paid' AND `+paymentPeriod+`),
		(SELECT COALESCE(sum(p.amount_santim),0) FROM payments p JOIN orders o ON o.id=p.order_id JOIN branches b ON b.id=o.branch_id WHERE o.branch_id=$1 AND o.status<>'cancelled' AND p.status='paid' AND `+paymentPeriod+`)`, u.BranchID).Scan(&orders, &paidOrders, &paidTotal)
	if paidOrders > 0 {
		avg = paidTotal / int64(paidOrders)
	}
	type Top struct {
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		Santim   int64  `json:"revenueSantim"`
	}
	top := []Top{}
	rows, err := s.DB.Query(`SELECT oi.name, sum(oi.quantity), sum(oi.line_total_santim) FROM order_items oi JOIN orders o ON o.id=oi.order_id JOIN branches b ON b.id=o.branch_id
		WHERE o.branch_id=$1 AND o.status<>'cancelled' AND EXISTS (SELECT 1 FROM payments p JOIN branches b ON b.id=o.branch_id WHERE p.order_id=o.id AND p.status='paid' AND `+paymentPeriod+`) GROUP BY oi.name ORDER BY 3 DESC LIMIT 5`, u.BranchID)
	if err == nil {
		for rows.Next() {
			var t Top
			rows.Scan(&t.Name, &t.Quantity, &t.Santim)
			top = append(top, t)
		}
		rows.Close()
	}
	type TableSales struct {
		Label       string `json:"label"`
		OrderCount  int    `json:"orderCount"`
		SalesSantim int64  `json:"salesSantim"`
	}
	tables := []TableSales{}
	tableRows, err := s.DB.Query(`SELECT t.label, count(DISTINCT o.id), COALESCE(sum(p.amount_santim),0)
		FROM orders o JOIN branches b ON b.id=o.branch_id JOIN dining_tables t ON t.id=o.table_id JOIN payments p ON p.order_id=o.id
		WHERE o.branch_id=$1 AND o.status<>'cancelled' AND p.status='paid' AND `+paymentPeriod+`
		GROUP BY t.id,t.label ORDER BY 3 DESC LIMIT 5`, u.BranchID)
	if err == nil {
		for tableRows.Next() {
			var t TableSales
			if tableRows.Scan(&t.Label, &t.OrderCount, &t.SalesSantim) == nil {
				tables = append(tables, t)
			}
		}
		tableRows.Close()
	}
	type SalesBucket struct {
		Label       string `json:"label"`
		SalesSantim int64  `json:"salesSantim"`
	}
	timeline := []SalesBucket{}
	dailyRows, err := s.DB.Query(`SELECT `+bucket+`, COALESCE(sum(p.amount_santim),0)
		FROM payments p JOIN orders o ON o.id=p.order_id JOIN branches b ON b.id=o.branch_id
		WHERE o.branch_id=$1 AND o.status<>'cancelled' AND p.status='paid' AND `+paymentPeriod+`
		GROUP BY 1 ORDER BY 1`, u.BranchID)
	if err == nil {
		for dailyRows.Next() {
			var b SalesBucket
			if dailyRows.Scan(&b.Label, &b.SalesSantim) == nil {
				timeline = append(timeline, b)
			}
		}
		dailyRows.Close()
	}
	httpx.JSON(w, 200, map[string]any{"period": period, "orderCount": orders, "paidOrderCount": paidOrders,
		"paidSalesSantim": paidTotal, "averagePaidOrderSantim": avg, "topItems": top, "topTables": tables, "salesTimeline": timeline})
}
