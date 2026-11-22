package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

func TestBuildMessage(t *testing.T) {
	fixed := time.Date(2026, 11, 22, 15, 4, 5, 0, time.UTC)
	m := &Mailer{
		from: "agent@example.com",
		to:   "alerts@example.com",
		now:  func() time.Time { return fixed },
	}
	code := 200
	message := string(m.buildMessage(domain.Event{
		Name:           "Saturday show",
		URL:            "https://example.com/tickets",
		LastStatusCode: &code,
	}, "http://localhost:8090"))

	for _, want := range []string{
		"From: DropPulse <agent@example.com>",
		"To: alerts@example.com",
		"Subject: DropPulse: Saturday show is on",
		"Status: HTTP 200",
		"Checked: 2026-11-22T15:04:05Z",
		"Open DropPulse: http://localhost:8090/",
		"You will get another email",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message is missing %q:\n%s", want, message)
		}
	}
	if strings.Contains(strings.ReplaceAll(message, "\r\n", ""), "\n") {
		t.Fatal("message must not contain bare LF line endings")
	}
}

func TestBuildMessageSanitisesHeaderInjection(t *testing.T) {
	m := &Mailer{
		from: "agent@example.com",
		to:   "alerts@example.com",
		now:  time.Now,
	}
	message := string(m.buildMessage(domain.Event{
		Name: "Show\r\nBcc: victim@example.com",
		URL:  "https://example.com/",
	}, "http://localhost:8090"))
	if strings.Contains(message, "\r\nBcc:") {
		t.Fatalf("header injection survived:\n%s", message)
	}
}

func TestValidateAddress(t *testing.T) {
	if err := validateAddress("agent@example.com"); err != nil {
		t.Fatalf("bare address rejected: %v", err)
	}
	for _, raw := range []string{"", " agent@example.com", "Name <agent@example.com>", "not-an-address"} {
		if err := validateAddress(raw); err == nil {
			t.Errorf("address %q must be rejected", raw)
		}
	}
}

func TestNewRejectsDisplayNameSender(t *testing.T) {
	if _, err := New("/nonexistent/sendmail", "DropPulse <agent@example.com>", "alerts@example.com", 0); err == nil {
		t.Fatal("sender with a display name must be rejected")
	}
}

func TestNewRejectsMissingSendmail(t *testing.T) {
	if _, err := New("/nonexistent/sendmail", "agent@example.com", "alerts@example.com", 0); err == nil {
		t.Fatal("a missing sendmail command must be rejected")
	}
}

func TestSendEventOnInvokesLocalSendmail(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OUT/args\"\ncat > \"$OUT/message\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OUT", dir)

	m, err := New(script, "agent@example.com", "alerts@example.com", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	if err := m.SendEventOn(context.Background(), domain.Event{
		Name:           "Saturday show",
		URL:            "https://example.com/tickets",
		LastStatusCode: &code,
	}, "http://localhost:8090"); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := "-i\n-f\nagent@example.com\n--\nalerts@example.com\n"
	if string(args) != wantArgs {
		t.Fatalf("sendmail args = %q, want %q", args, wantArgs)
	}
	message, err := os.ReadFile(filepath.Join(dir, "message"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(message), "Subject: DropPulse: Saturday show is on") {
		t.Fatalf("message was not passed to sendmail:\n%s", message)
	}
}
