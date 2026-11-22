package config

import (
	"strings"
	"testing"
	"time"
)

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://droppulse:secret@localhost:5432/droppulse?sslmode=disable",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(envLookup(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q", cfg.Port)
	}
	if cfg.BindAddr != "127.0.0.1" {
		t.Fatalf("BindAddr = %q, want loopback by default", cfg.BindAddr)
	}
	if cfg.CookieSecure {
		t.Fatal("local http development must not set Secure cookies")
	}
	if cfg.SessionIdleTTL != 30*24*time.Hour {
		t.Fatalf("SessionIdleTTL = %v", cfg.SessionIdleTTL)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("AllowedOrigins = %v", cfg.AllowedOrigins)
	}
	if !cfg.OriginAllowed("http://localhost:8000") || !cfg.OriginAllowed("http://localhost:8080") {
		t.Fatalf("local origins missing: %v", cfg.AllowedOrigins)
	}
	if cfg.OriginAllowed("http://evil.example") {
		t.Fatal("unexpected origin allowed")
	}
}

func TestLoadSecureOrigins(t *testing.T) {
	env := baseEnv()
	env["PUBLIC_API_URL"] = "https://api.example.com"
	env["WEB_APP_URL"] = "https://app.example.com"
	env["GOOGLE_CLIENT_ID"] = "google-client"
	env["GOOGLE_CLIENT_SECRET"] = "google-secret"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure {
		t.Fatal("https deployment must set Secure cookies")
	}
	if _, ok := cfg.Provider("google"); !ok {
		t.Fatal("google provider was not configured")
	}
	if !cfg.OriginAllowed("https://app.example.com") {
		t.Fatal("web origin missing")
	}
}

func TestLoadRejectsInvalidBindAddr(t *testing.T) {
	env := baseEnv()
	env["BIND_ADDR"] = "localhost"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("BIND_ADDR must be an IP address")
	}
}

func TestLoadRejectsWildcardOrigin(t *testing.T) {
	env := baseEnv()
	env["ALLOWED_ORIGINS"] = "*"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("wildcard origin must be rejected")
	}
}

func TestLoadRejectsInsecureRemoteAPI(t *testing.T) {
	env := baseEnv()
	env["PUBLIC_API_URL"] = "http://api.example.com"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("non-local http API URL must be rejected")
	}
}

func TestLoadRejectsInsecureAllowedOriginWhenSecure(t *testing.T) {
	env := baseEnv()
	env["PUBLIC_API_URL"] = "https://api.example.com"
	env["WEB_APP_URL"] = "https://app.example.com"
	env["ALLOWED_ORIGINS"] = "https://app.example.com,http://old.example.com"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("insecure origin must be rejected when the API serves https")
	}
}

func TestLoadRejectsPartialProvider(t *testing.T) {
	env := baseEnv()
	env["GOOGLE_CLIENT_ID"] = "only-id"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("partial provider credentials must be rejected")
	}
}

func TestLoadRejectsBadSessionWindow(t *testing.T) {
	env := baseEnv()
	env["SESSION_IDLE_TTL"] = "100h"
	env["SESSION_ABSOLUTE_TTL"] = "10h"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("absolute session lifetime shorter than idle lifetime must be rejected")
	}
}

func TestLoadRejectsMalformedAdminEmail(t *testing.T) {
	env := baseEnv()
	env["ADMIN_EMAILS"] = "not-an-email"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("malformed admin email must be rejected")
	}
}

func TestProviderNamesStable(t *testing.T) {
	env := baseEnv()
	env["FACEBOOK_CLIENT_ID"] = "fb-id"
	env["FACEBOOK_CLIENT_SECRET"] = "fb-secret"
	env["GOOGLE_CLIENT_ID"] = "google-id"
	env["GOOGLE_CLIENT_SECRET"] = "google-secret"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ProviderNames(), ","); got != "google,facebook" {
		t.Fatalf("ProviderNames = %q", got)
	}
}
