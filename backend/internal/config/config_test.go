package config

import (
	"os"
	"path/filepath"
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

func TestLoadAllowedEmails(t *testing.T) {
	env := baseEnv()
	env["ALLOWED_EMAILS"] = " First@Example.com , second@example.com ,, "
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedEmails) != 2 {
		t.Fatalf("AllowedEmails = %v", cfg.AllowedEmails)
	}
	if !cfg.EmailAllowed("first@example.com", true) {
		t.Fatal("allowlisted verified address was refused")
	}
	if cfg.EmailAllowed("first@example.com", false) {
		t.Fatal("unverified address must not pass a non-empty allowlist")
	}
	if cfg.EmailAllowed("third@example.com", true) {
		t.Fatal("address outside the allowlist was admitted")
	}
}

func TestEmailAllowedEmptyListIsOpen(t *testing.T) {
	cfg := Config{}
	if !cfg.EmailAllowed("anyone@example.com", false) {
		t.Fatal("an empty allowlist must keep the development default open")
	}
}

func TestLoadRejectsMalformedAllowedEmail(t *testing.T) {
	env := baseEnv()
	env["ALLOWED_EMAILS"] = "Name <person@example.com>"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("a display name in ALLOWED_EMAILS must be rejected")
	}
}

func TestLoadProductionRequiresAllowedEmails(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["PUBLIC_API_URL"] = "https://dp.example.com"
	env["WEB_APP_URL"] = "https://dp.example.com"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("production multi-user mode must refuse an empty allowlist")
	}
}

func TestLoadProductionAcceptsAllowedEmails(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "Production"
	env["PUBLIC_API_URL"] = "https://dp.example.com"
	env["WEB_APP_URL"] = "https://dp.example.com"
	env["ALLOWED_EMAILS"] = "owner@example.com"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EmailAllowed("owner@example.com", true) {
		t.Fatal("allowlisted address was refused")
	}
}

func TestLoadSingleUserDefaults(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["PORT"] = "8090"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SingleUserName != "admin" {
		t.Fatalf("SingleUserName = %q", cfg.SingleUserName)
	}
	if got := cfg.WebAppURL.String(); got != "http://localhost:8090" {
		t.Fatalf("WebAppURL = %q, want the API origin", got)
	}
	for _, origin := range []string{"http://localhost:8090", "http://127.0.0.1:8090", "http://[::1]:8090"} {
		if !cfg.OriginAllowed(origin) {
			t.Errorf("loopback origin %q is not allowed", origin)
		}
	}
	if cfg.OriginAllowed("http://evil.example") {
		t.Fatal("non-loopback origin was allowed")
	}
}

func TestLoadFrontendDefaultsWebAppToAPIOrigin(t *testing.T) {
	env := baseEnv()
	env["FRONTEND_FILE"] = "../frontend/index.html"
	env["PORT"] = "8090"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.WebAppURL.String(); got != "http://localhost:8090" {
		t.Fatalf("WebAppURL = %q, want the API origin", got)
	}
	if !cfg.OriginAllowed("http://localhost:8090") {
		t.Fatal("the served frontend origin must be allowed")
	}
}

func TestLoadAllowsVerifiedAccountNotifications(t *testing.T) {
	env := baseEnv()
	env["SMTP_FROM"] = "agent@example.com"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPFrom != "agent@example.com" {
		t.Fatalf("SMTPFrom = %q", cfg.SMTPFrom)
	}
	if cfg.NotificationEmail != "" {
		t.Fatalf("NotificationEmail = %q, want no fixed recipient outside single-user mode", cfg.NotificationEmail)
	}
}

func TestLoadSingleUserRequiresLoopback(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["BIND_ADDR"] = "0.0.0.0"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("single-user mode on a public listener must be rejected")
	}
}

func TestLoadRejectsBadSingleUserName(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["SINGLE_USER_NAME"] = "not a user"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("invalid single-user name must be rejected")
	}
}

func TestLoadNotificationConfig(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["NOTIFICATION_EMAIL"] = "alerts@example.com"
	env["SMTP_FROM"] = "agent@example.com"
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NotificationEmail != "alerts@example.com" || cfg.SMTPFrom != "agent@example.com" {
		t.Fatalf("notification config = %q / %q", cfg.NotificationEmail, cfg.SMTPFrom)
	}
	if cfg.SendmailPath != "/usr/sbin/sendmail" {
		t.Fatalf("SendmailPath = %q", cfg.SendmailPath)
	}
}

func TestLoadRejectsNotificationsOutsideSingleUser(t *testing.T) {
	env := baseEnv()
	env["NOTIFICATION_EMAIL"] = "alerts@example.com"
	env["SMTP_FROM"] = "agent@example.com"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("notifications outside single-user mode must be rejected")
	}
}

func TestLoadRejectsNotificationWithoutSender(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["NOTIFICATION_EMAIL"] = "alerts@example.com"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("notification recipient without a sender must be rejected")
	}
}

func TestLoadRejectsRelativeSendmailPath(t *testing.T) {
	env := baseEnv()
	env["SINGLE_USER_MODE"] = "true"
	env["SENDMAIL_PATH"] = "sendmail"
	if _, err := LoadFrom(envLookup(env)); err == nil {
		t.Fatal("a relative sendmail path must be rejected")
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

func TestLoadSMTPConfig(t *testing.T) {
	dir := t.TempDir()
	pwFile := filepath.Join(dir, "smtp.pass")
	if err := os.WriteFile(pwFile, []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["SMTP_FROM"] = "alerts@dp.example"
	env["SMTP_HOST"] = "smtp.example.com"
	env["SMTP_USERNAME"] = "mailer"
	env["SMTP_PASSWORD_FILE"] = pwFile
	cfg, err := LoadFrom(envLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPHost != "smtp.example.com" || cfg.SMTPPort != "587" || cfg.SMTPUsername != "mailer" || cfg.SMTPPassword != "s3cr3t" || cfg.SMTPTLS != "auto" {
		t.Fatalf("SMTP config = %+v", cfg)
	}
}

func TestLoadSMTPValidation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "host without from",
			env:  map[string]string{"SMTP_HOST": "smtp.example.com"},
		},
		{
			name: "username without host",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p"},
		},
		{
			name: "username without password",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_HOST": "smtp.example.com", "SMTP_USERNAME": "u"},
		},
		{
			name: "plaintext remote host",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_HOST": "smtp.example.com", "SMTP_TLS": "none"},
		},
		{
			name: "plaintext with credentials on loopback",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_HOST": "127.0.0.1", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p", "SMTP_TLS": "none"},
		},
		{
			name: "host with port suffix",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_HOST": "smtp.example.com:587"},
		},
		{
			name: "invalid tls mode",
			env:  map[string]string{"SMTP_FROM": "a@example.com", "SMTP_HOST": "smtp.example.com", "SMTP_TLS": "bogus"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			for k, v := range tc.env {
				env[k] = v
			}
			if _, err := LoadFrom(envLookup(env)); err == nil {
				t.Fatalf("case %q must be rejected", tc.name)
			}
		})
	}

	env := baseEnv()
	env["SMTP_FROM"] = "a@example.com"
	env["SMTP_HOST"] = "127.0.0.1"
	env["SMTP_PORT"] = "25"
	env["SMTP_TLS"] = "none"
	if _, err := LoadFrom(envLookup(env)); err != nil {
		t.Fatalf("unauthenticated loopback SMTP should be allowed: %v", err)
	}
}
