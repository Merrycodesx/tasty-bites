// Package auth handles staff login, opaque session tokens and role checks.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"tastybites/internal/httpx"
)

type User struct {
	ID       int64  `json:"id"`
	BranchID int64  `json:"branchId"`
	Username string `json:"username"`
	FullName string `json:"fullName"`
	Role     string `json:"role"`
}

type ctxKey struct{}

type Service struct{ DB *sql.DB }

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// dummyHash lets failed logins for unknown users cost the same as real ones.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)

func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	var u User
	var hash string
	var active bool
	err := s.DB.QueryRow(`SELECT id, branch_id, username, full_name, role, password_hash, active FROM staff_users WHERE username=$1`,
		strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.BranchID, &u.Username, &u.FullName, &u.Role, &hash, &active)
	if err == sql.ErrNoRows {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		httpx.Fail(w, httpx.Err(401, "invalid_credentials", "Wrong username or password."))
		return
	} else if err != nil {
		httpx.Fail(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || !active {
		httpx.Fail(w, httpx.Err(401, "invalid_credentials", "Wrong username or password."))
		return
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	if _, err := s.DB.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`,
		hashToken(token), u.ID, time.Now().Add(12*time.Hour)); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"token": token, "user": u})
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if t := bearer(r); t != "" {
		s.DB.Exec(`DELETE FROM sessions WHERE token_hash=$1`, hashToken(t))
	}
	w.WriteHeader(http.StatusNoContent)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// Require authenticates the request and allows only the listed roles (none listed = any staff).
func (s *Service) Require(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			t := bearer(r)
			if t == "" {
				httpx.Fail(w, httpx.Err(401, "unauthenticated", "Please sign in."))
				return
			}
			u, err := s.Lookup(t)
			if err != nil {
				httpx.Fail(w, httpx.Err(401, "unauthenticated", "Your session expired. Please sign in again."))
				return
			}
			if len(roles) > 0 && !contains(roles, u.Role) {
				httpx.Fail(w, httpx.Err(403, "forbidden", "Your role cannot do this."))
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
		}
	}
}

// Lookup resolves a bearer token to an active staff user with an unexpired session.
func (s *Service) Lookup(token string) (User, error) {
	var u User
	err := s.DB.QueryRow(`SELECT u.id,u.branch_id,u.username,u.full_name,u.role FROM sessions s JOIN staff_users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active`, hashToken(token)).Scan(&u.ID, &u.BranchID, &u.Username, &u.FullName, &u.Role)
	return u, err
}

// BearerToken extracts the bearer token from a request ("" if absent).
func BearerToken(r *http.Request) string { return bearer(r) }

func From(r *http.Request) User { u, _ := r.Context().Value(ctxKey{}).(User); return u }

func (s *Service) Me(w http.ResponseWriter, r *http.Request) { httpx.JSON(w, 200, From(r)) }

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
