package notify

import (
	"bufio"
	"context"
	"net"
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

func TestBuildMessageForProviderAvailability(t *testing.T) {
	m := &Mailer{
		from: "agent@example.com",
		to:   "alerts@example.com",
		now:  time.Now,
	}
	message := string(m.buildMessage(domain.Event{
		Name: "Dune at Flora IMAX",
		URL:  "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
	}, "http://localhost:8090"))
	if !strings.Contains(message, "Status: availability confirmed") {
		t.Fatalf("provider message did not describe availability:\n%s", message)
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

func TestSendEventOnUsesVerifiedAccountRecipient(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OUT/args\"\ncat > \"$OUT/message\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OUT", dir)

	m, err := New(script, "agent@example.com", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	if err := m.SendEventOn(context.Background(), domain.Event{
		Name:           "Saturday show",
		URL:            "https://example.com/tickets",
		LastStatusCode: &code,
		UserEmail:      "viewer@example.com",
	}, "http://localhost:8090"); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := "-i\n-f\nagent@example.com\n--\nviewer@example.com\n"
	if string(args) != wantArgs {
		t.Fatalf("sendmail args = %q, want %q", args, wantArgs)
	}
	message, err := os.ReadFile(filepath.Join(dir, "message"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(message), "To: viewer@example.com") {
		t.Fatalf("account recipient missing from message:\n%s", message)
	}
}

func TestSendEventOnRejectsMissingRecipient(t *testing.T) {
	m := &Mailer{from: "agent@example.com", now: time.Now}
	if err := m.SendEventOn(context.Background(), domain.Event{Name: "Show"}, "http://localhost:8090"); err == nil {
		t.Fatal("an event with no fixed or account recipient must be rejected")
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

func TestBuildMessageIncludesDetailAndPausedFormat(t *testing.T) {
	m := &Mailer{
		from: "alerts@example.com",
		to:   "viewer@example.com",
		now:  func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) },
	}
	onMsg := string(m.buildMessage(domain.Event{
		Name:       "Flora IMAX",
		URL:        "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
		LastDetail: "IMAX VOLVO · 2026-12-18 19:30 · ~5% open",
	}, "https://dp.example/app/"))
	for _, want := range []string{
		"Status: availability confirmed — IMAX VOLVO · 2026-12-18 19:30 · ~5% open",
		"Open DropPulse: https://dp.example/app/",
	} {
		if !strings.Contains(onMsg, want) {
			t.Errorf("availability message missing %q:\n%s", want, onMsg)
		}
	}

	pausedMsg := string(m.buildPausedMessage(domain.Event{
		Name:         "Flora IMAX",
		URL:          "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
		PausedReason: "Paused after repeated blocks",
		LastError:    "Cinema City rate-limited requests (HTTP 429)",
	}, "https://dp.example/app/"))
	for _, want := range []string{
		"Subject: DropPulse: Flora IMAX paused (site blocked checks)",
		"Reason: Paused after repeated blocks",
		"Last response: Cinema City rate-limited requests (HTTP 429)",
		"Target URL (open manually in your browser):",
	} {
		if !strings.Contains(pausedMsg, want) {
			t.Errorf("paused message missing %q:\n%s", want, pausedMsg)
		}
	}
}

func TestSendSMTPLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	received := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		write := func(line string) {
			_, _ = w.WriteString(line + "\r\n")
			_ = w.Flush()
		}
		write("220 localhost ESMTP test")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					write("250 OK queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			upper := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				write("250 localhost")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				write("250 OK")
			case strings.HasPrefix(upper, "RCPT TO:"):
				write("250 OK")
			case upper == "DATA":
				inData = true
				write("354 Go ahead")
			case upper == "QUIT":
				write("221 Bye")
				received <- data.String()
				return
			default:
				write("250 OK")
			}
		}
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewSMTP(SMTPConfig{Host: host, Port: port, TLSMode: "none"}, "alerts@example.com", "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SendEventOn(context.Background(), domain.Event{
		Name:      "Flora IMAX",
		URL:       "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
		UserEmail: "viewer@example.com",
	}, "https://dp.example/app/"); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-received:
		if !strings.Contains(msg, "Subject: DropPulse: Flora IMAX is on") || !strings.Contains(msg, "To: viewer@example.com") {
			t.Fatalf("unexpected SMTP payload:\n%s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SMTP message")
	}
}
