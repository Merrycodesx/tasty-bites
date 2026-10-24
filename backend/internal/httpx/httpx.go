// Package httpx holds shared HTTP helpers: JSON responses, errors, CORS, rate limiting.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func Err(status int, code, msg string) *APIError { return &APIError{status, code, msg} }

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Fail writes an APIError, or a generic 500 that never leaks internals.
func Fail(w http.ResponseWriter, err error) {
	if ae, ok := err.(*APIError); ok {
		JSON(w, ae.Status, map[string]any{"error": map[string]string{"code": ae.Code, "message": ae.Message}})
		return
	}
	slog.Error("internal error", "err", err)
	JSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "Something went wrong. Please try again."}})
}

// Decode reads a JSON body (max 64 KB) rejecting unknown fields.
func Decode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return Err(400, "bad_request", "Invalid request body.")
	}
	return nil
}

func CORS(allowed []string, next http.Handler) http.Handler {
	set := map[string]bool{}
	for _, o := range allowed {
		set[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && set[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimit is a simple per-IP fixed-window limiter for sensitive endpoints.
func RateLimit(max int, window time.Duration, next http.Handler) http.Handler {
	var mu sync.Mutex
	hits := map[string][]time.Time{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		now := time.Now()
		mu.Lock()
		kept := hits[ip][:0]
		for _, t := range hits[ip] {
			if now.Sub(t) < window {
				kept = append(kept, t)
			}
		}
		if len(kept) >= max {
			hits[ip] = kept
			mu.Unlock()
			Fail(w, Err(429, "rate_limited", "Too many requests. Please wait a moment."))
			return
		}
		hits[ip] = append(kept, now)
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
