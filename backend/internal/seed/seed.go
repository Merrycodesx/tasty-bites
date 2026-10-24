// Package seed inserts clearly labelled DEVELOPMENT fixtures. Never enable in production.
package seed

import (
	"database/sql"
	"log/slog"

	"tastybites/internal/auth"
)

func Run(d *sql.DB, password string) error {
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM branches`).Scan(&n); err != nil || n > 0 {
		return err
	}
	slog.Warn("seeding DEVELOPMENT fixture data (sample menu and staff accounts)")
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var branch int64
	if err := tx.QueryRow(`INSERT INTO branches(name) VALUES('Tasty Bites') RETURNING id`).Scan(&branch); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	for _, u := range [][3]string{{"admin", "Dev Admin", "admin"}, {"manager", "Dev Manager", "manager"}, {"waiter", "Dev Waiter", "waiter"}, {"cashier", "Dev Cashier", "cashier"}, {"kitchen", "Dev Kitchen", "kitchen"}} {
		if _, err := tx.Exec(`INSERT INTO staff_users(branch_id,username,full_name,password_hash,role) VALUES($1,$2,$3,$4,$5)`, branch, u[0], u[1], hash, u[2]); err != nil {
			return err
		}
	}
	for i := 1; i <= 8; i++ {
		tx.Exec(`INSERT INTO dining_tables(branch_id,label,seats) VALUES($1,$2,4)`, branch, "T"+string(rune('0'+i)))
	}
	type item struct {
		name, desc string
		price      int64 // santim
		variants   [][2]any
		extras     [][2]any
	}
	menu := []struct {
		cat   string
		items []item
	}{
		{"Ethiopian", []item{
			{"Injera with Beef Stew", "Injera with spicy beef stew. (sample item)", 18000, nil, [][2]any{{"Extra injera", int64(2000)}, {"Boiled egg", int64(2500)}}},
			{"Doro Wot", "Chicken stew with berbere and egg. (sample item)", 25000, nil, [][2]any{{"Extra injera", int64(2000)}}},
			{"Shiro", "Chickpea stew. (sample item)", 10000, [][2]any{{"Regular", int64(10000)}, {"Large", int64(14000)}}, nil},
		}},
		{"Fast food", []item{
			{"Cheese Burger", "Burger with cheese and vegetables. (sample item)", 22000, nil, [][2]any{{"Extra cheese", int64(3000)}, {"Fries", int64(6000)}}},
			{"Chicken Sandwich", "Grilled chicken sandwich. (sample item)", 16000, nil, nil},
		}},
		{"Drinks", []item{
			{"Coffee", "Ethiopian coffee. (sample item)", 4000, [][2]any{{"Small", int64(4000)}, {"Large", int64(6000)}}, nil},
			{"Fresh Juice", "Seasonal fruit juice. (sample item)", 7000, nil, nil},
		}},
	}
	for ci, c := range menu {
		var cid int64
		if err := tx.QueryRow(`INSERT INTO menu_categories(branch_id,name,sort_order) VALUES($1,$2,$3) RETURNING id`, branch, c.cat, ci).Scan(&cid); err != nil {
			return err
		}
		for _, it := range c.items {
			var iid int64
			if err := tx.QueryRow(`INSERT INTO menu_items(branch_id,category_id,name,description,price_santim) VALUES($1,$2,$3,$4,$5) RETURNING id`, branch, cid, it.name, it.desc, it.price).Scan(&iid); err != nil {
				return err
			}
			for _, v := range it.variants {
				tx.Exec(`INSERT INTO item_variants(item_id,name,price_santim) VALUES($1,$2,$3)`, iid, v[0], v[1])
			}
			for _, e := range it.extras {
				tx.Exec(`INSERT INTO item_extras(item_id,name,price_santim) VALUES($1,$2,$3)`, iid, e[0], e[1])
			}
		}
	}
	return tx.Commit()
}
