// Package menu serves the public menu and staff menu management.
package menu

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"tastybites/internal/auth"
	"tastybites/internal/httpx"
)

type Service struct {
	DB       *sql.DB
	BranchID int64 // default branch for public, unauthenticated reads
}

type Option struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	PriceSantim int64  `json:"priceSantim"`
	Available   bool   `json:"available"`
}
type Item struct {
	ID          int64    `json:"id"`
	CategoryID  int64    `json:"categoryId"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PriceSantim int64    `json:"priceSantim"`
	Available   bool     `json:"available"`
	Variants    []Option `json:"variants"`
	Extras      []Option `json:"extras"`
}
type Category struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Items []Item `json:"items"`
}

func (s *Service) load(branch int64, includeUnavailable bool) ([]Category, error) {
	cats := []Category{}
	rows, err := s.DB.Query(`SELECT id,name FROM menu_categories WHERE branch_id=$1 AND active ORDER BY sort_order,id`, branch)
	if err != nil {
		return nil, err
	}
	idx := map[int64]int{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		c.Items = []Item{}
		idx[c.ID] = len(cats)
		cats = append(cats, c)
	}
	rows.Close()

	irows, err := s.DB.Query(`SELECT id,category_id,name,description,price_santim,available FROM menu_items
		WHERE branch_id=$1 AND NOT archived AND ($2 OR available) ORDER BY id`, branch, includeUnavailable)
	if err != nil {
		return nil, err
	}
	pos := map[int64][2]int{}
	for irows.Next() {
		var it Item
		if err := irows.Scan(&it.ID, &it.CategoryID, &it.Name, &it.Description, &it.PriceSantim, &it.Available); err != nil {
			return nil, err
		}
		it.Variants, it.Extras = []Option{}, []Option{}
		if ci, ok := idx[it.CategoryID]; ok {
			pos[it.ID] = [2]int{ci, len(cats[ci].Items)}
			cats[ci].Items = append(cats[ci].Items, it)
		}
	}
	irows.Close()

	for _, kind := range []string{"item_variants", "item_extras"} {
		orows, err := s.DB.Query(`SELECT o.item_id,o.id,o.name,o.price_santim,o.available FROM ` + kind + ` o ORDER BY o.id`)
		if err != nil {
			return nil, err
		}
		for orows.Next() {
			var itemID int64
			var o Option
			if err := orows.Scan(&itemID, &o.ID, &o.Name, &o.PriceSantim, &o.Available); err != nil {
				return nil, err
			}
			p, ok := pos[itemID]
			if !ok || (!includeUnavailable && !o.Available) {
				continue
			}
			it := &cats[p[0]].Items[p[1]]
			if kind == "item_variants" {
				it.Variants = append(it.Variants, o)
			} else {
				it.Extras = append(it.Extras, o)
			}
		}
		orows.Close()
	}
	return cats, nil
}

// Public returns only available items for the customer app.
func (s *Service) Public(w http.ResponseWriter, r *http.Request) {
	cats, err := s.load(s.BranchID, false)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"categories": cats})
}

// Staff returns everything including unavailable items.
func (s *Service) Staff(w http.ResponseWriter, r *http.Request) {
	cats, err := s.load(auth.From(r).BranchID, true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"categories": cats})
}

func (s *Service) CreateItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CategoryID  int64  `json:"categoryId"`
		Name        string `json:"name"`
		Description string `json:"description"`
		PriceSantim int64  `json:"priceSantim"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 || in.PriceSantim < 0 || in.PriceSantim > 100_000_00 {
		httpx.Fail(w, httpx.Err(400, "invalid_item", "Enter a name and a valid price."))
		return
	}
	u := auth.From(r)
	var id int64
	err := s.DB.QueryRow(`INSERT INTO menu_items(branch_id,category_id,name,description,price_santim)
		SELECT $1,c.id,$3,$4,$5 FROM menu_categories c WHERE c.id=$2 AND c.branch_id=$1 RETURNING id`,
		u.BranchID, in.CategoryID, in.Name, in.Description, in.PriceSantim).Scan(&id)
	if err == sql.ErrNoRows {
		httpx.Fail(w, httpx.Err(400, "invalid_category", "That category does not exist."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'menu.create','menu_item',$2,$3)`, u.ID, id, in.Name)
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Service) PatchItem(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		PriceSantim *int64 `json:"priceSantim"`
		Available   *bool  `json:"available"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.PriceSantim != nil && (*in.PriceSantim < 0 || *in.PriceSantim > 100_000_00) {
		httpx.Fail(w, httpx.Err(400, "invalid_price", "Enter a valid price."))
		return
	}
	u := auth.From(r)
	res, err := s.DB.Exec(`UPDATE menu_items SET price_santim=COALESCE($3,price_santim), available=COALESCE($4,available)
		WHERE id=$1 AND branch_id=$2`, id, u.BranchID, in.PriceSantim, in.Available)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Fail(w, httpx.Err(404, "not_found", "Menu item not found."))
		return
	}
	s.DB.Exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,detail) VALUES($1,'menu.update','menu_item',$2,$3)`, u.ID, id, "price/availability changed")
	w.WriteHeader(http.StatusNoContent)
}
