package config

import "testing"

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("PUBLIC_MINIAPP_URL", " https://mini.example/app ")
	t.Setenv("ALLOWED_ORIGINS", "http://a.test, http://b.test")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MiniAppURL != "https://mini.example/app" {
		t.Fatalf("MiniAppURL = %q", c.MiniAppURL)
	}
	if len(c.AllowedOrigins) != 2 || c.AllowedOrigins[1] != "http://b.test" {
		t.Fatalf("origins = %v", c.AllowedOrigins)
	}
}

func TestLoadRequiresDatabaseAndSeedPassword(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("DATABASE_URL must be required")
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("SEED_DEV_DATA", "true")
	t.Setenv("SEED_PASSWORD", "short")
	if _, err := Load(); err == nil {
		t.Fatal("seeding must require a password of 8+ characters")
	}
}
