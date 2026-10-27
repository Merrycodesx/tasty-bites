// Package config loads settings from environment variables.
package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL    string
	ListenAddr     string
	AllowedOrigins []string
	MiniAppURL     string // base URL encoded into table QR codes
	SeedDevData    bool   // only for local development
	SeedPassword   string // password for seeded dev staff accounts
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		ListenAddr:   getenv("LISTEN_ADDR", ":8080"),
		SeedDevData:  os.Getenv("SEED_DEV_DATA") == "true",
		SeedPassword: os.Getenv("SEED_PASSWORD"),
		MiniAppURL:   strings.TrimSpace(os.Getenv("PUBLIC_MINIAPP_URL")),
	}
	for _, o := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.SeedDevData && len(c.SeedPassword) < 8 {
		return c, errors.New("SEED_PASSWORD (8+ chars) is required when SEED_DEV_DATA=true")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
