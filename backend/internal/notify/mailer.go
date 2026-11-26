// Package notify sends availability email through either an SMTP submission
// endpoint (compatible with a strict systemd sandbox) or the local MTA's
// sendmail command.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// SMTPConfig describes an SMTP submission endpoint.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	TLSMode  string // "auto", "starttls", "implicit", or "none"
	Helo     string // EHLO name; defaults to the sender domain
}

// Mailer sends availability and challenge-pause emails. A non-empty to address
// is a fixed recipient (the single-user mode); otherwise the verified address
// on the event's owning account is used.
type Mailer struct {
	sendmail string
	smtp     *SMTPConfig
	from     string
	to       string
	timeout  time.Duration
	now      func() time.Time
}

// New validates the addresses and sendmail command and returns a mailer for
// the local MTA. The recipient may be empty when notifications go to the
// event owner instead of a fixed address.
func New(sendmail, from, to string, timeout time.Duration) (*Mailer, error) {
	if err := validateAddress(from); err != nil {
		return nil, fmt.Errorf("sender address: %w", err)
	}
	if to != "" {
		if err := validateAddress(to); err != nil {
			return nil, fmt.Errorf("recipient address: %w", err)
		}
	}
	if !filepath.IsAbs(sendmail) {
		return nil, errors.New("sendmail path must be absolute")
	}
	info, err := os.Stat(sendmail)
	if err != nil {
		return nil, fmt.Errorf("sendmail command: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("sendmail command %q is not an executable file", sendmail)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Mailer{sendmail: sendmail, from: from, to: to, timeout: timeout, now: time.Now}, nil
}

// NewSMTP validates the addresses and SMTP settings and returns a mailer that
// submits messages over TCP/TLS without invoking privileged local helpers.
func NewSMTP(cfg SMTPConfig, from, to string, timeout time.Duration) (*Mailer, error) {
	if err := validateAddress(from); err != nil {
		return nil, fmt.Errorf("sender address: %w", err)
	}
	if to != "" {
		if err := validateAddress(to); err != nil {
			return nil, fmt.Errorf("recipient address: %w", err)
		}
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.TLSMode = strings.ToLower(strings.TrimSpace(cfg.TLSMode))
	if cfg.Host == "" {
		return nil, errors.New("SMTP host is required")
	}
	if cfg.Port == "" {
		cfg.Port = "587"
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "auto"
	}
	switch cfg.TLSMode {
	case "auto", "starttls", "implicit", "none":
	default:
		return nil, fmt.Errorf("unsupported SMTP TLS mode %q", cfg.TLSMode)
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("SMTP username and password must be set together")
	}
	if cfg.TLSMode == "none" {
		if !isLoopbackHost(cfg.Host) {
			return nil, errors.New("unencrypted SMTP is only allowed for loopback hosts")
		}
		if cfg.Username != "" {
			return nil, errors.New("SMTP credentials require TLS")
		}
	}
	cfg.Helo = strings.TrimSpace(cfg.Helo)
	if cfg.Helo == "" {
		cfg.Helo = senderDomain(from)
	}
	if err := validateHeloName(cfg.Helo); err != nil {
		return nil, fmt.Errorf("SMTP HELO name: %w", err)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	copied := cfg
	return &Mailer{smtp: &copied, from: from, to: to, timeout: timeout, now: time.Now}, nil
}

// SendEventOn sends the notification for an event that has just become
// available. The caller decides whether this transition is new; the mailer
// only formats and submits the message.
func (m *Mailer) SendEventOn(ctx context.Context, event domain.Event, uiURL string) error {
	recipient := m.recipient(event)
	if err := validateAddress(recipient); err != nil {
		return fmt.Errorf("notification recipient: %w", err)
	}
	message := m.buildMessage(event, uiURL)
	return m.deliver(ctx, recipient, message)
}

// SendEventPaused notifies the owner when a watch is automatically paused due
// to repeated anti-bot challenges or rate limiting, giving them a direct link
// to visit the site manually.
func (m *Mailer) SendEventPaused(ctx context.Context, event domain.Event, uiURL string) error {
	recipient := m.recipient(event)
	if err := validateAddress(recipient); err != nil {
		return fmt.Errorf("notification recipient: %w", err)
	}
	message := m.buildPausedMessage(event, uiURL)
	return m.deliver(ctx, recipient, message)
}

func (m *Mailer) deliver(ctx context.Context, recipient string, message []byte) error {
	if m.smtp != nil {
		return m.sendSMTP(ctx, recipient, message)
	}
	return m.sendSendmail(ctx, recipient, message)
}

func (m *Mailer) sendSendmail(ctx context.Context, recipient string, message []byte) error {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.sendmail, "-i", "-f", m.from, "--", recipient)
	cmd.Stdin = bytes.NewReader(message)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.Join(strings.Fields(stderr.String()), " ")
		if len(detail) > 300 {
			detail = detail[:300]
		}
		if detail != "" {
			return fmt.Errorf("local mail submission: %w: %s", err, detail)
		}
		return fmt.Errorf("local mail submission: %w", err)
	}
	return nil
}

func (m *Mailer) sendSMTP(ctx context.Context, recipient string, message []byte) error {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cfg := m.smtp
	mode := resolveTLSMode(*cfg)
	if mode == "none" && (!isLoopbackHost(cfg.Host) || cfg.Username != "") {
		return errors.New("refusing plaintext SMTP for non-loopback or authenticated connection")
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.Host,
	}

	dialer := &net.Dialer{Timeout: m.timeout}
	var (
		conn net.Conn
		err  error
	)
	if mode == "implicit" {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial SMTP %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Go's client says EHLO localhost by default, which a receiving MTA with
	// reject_non_fqdn_helo_hostname refuses at RCPT TO.
	if err := client.Hello(cfg.Helo); err != nil {
		return fmt.Errorf("SMTP EHLO %s: %w", cfg.Helo, err)
	}

	if mode == "starttls" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("SMTP write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("SMTP commit message: %w", err)
	}
	if err := client.Quit(); err != nil {
		// Message is already accepted after DATA close; ignore benign close races.
		return nil
	}
	return nil
}

func resolveTLSMode(cfg SMTPConfig) string {
	if cfg.TLSMode != "" && cfg.TLSMode != "auto" {
		return cfg.TLSMode
	}
	if cfg.Port == "465" {
		return "implicit"
	}
	if isLoopbackHost(cfg.Host) && cfg.Username == "" && cfg.Port != "587" {
		return "none"
	}
	return "starttls"
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.IsLoopback()
}

// senderDomain is the domain part of a validated bare address; it is a
// fully-qualified name whenever the sender is one, which makes it a usable
// EHLO name without extra configuration.
func senderDomain(from string) string {
	at := strings.LastIndex(from, "@")
	if at < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(from[at+1:]), "[]")
}

func validateHeloName(name string) error {
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return errors.New("must be a fully-qualified hostname without spaces")
	}
	if !strings.Contains(name, ".") || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return errors.New("must be a fully-qualified hostname, for example mail.example.com")
	}
	if len(name) > 253 {
		return errors.New("must be 253 bytes or fewer")
	}
	return nil
}

// recipient prefers the fixed single-user address and falls back to the
// verified email on the account that owns the event.
func (m *Mailer) recipient(event domain.Event) string {
	if m.to != "" {
		return m.to
	}
	return strings.TrimSpace(event.UserEmail)
}

func (m *Mailer) buildMessage(event domain.Event, uiURL string) []byte {
	now := m.now().UTC()
	name := headerSafe(event.Name)
	to := m.recipient(event)
	subject := "DropPulse: " + name + " is on"
	status := "availability confirmed"
	if event.LastStatusCode != nil {
		status = fmt.Sprintf("HTTP %d", *event.LastStatusCode)
	}
	if detail := strings.TrimSpace(headerSafe(event.LastDetail)); detail != "" {
		status += " — " + detail
	}
	body := fmt.Sprintf(`%s is on.

URL: %s
Status: %s
Checked: %s
Open DropPulse: %s

You will get another email for this event after it goes off and then on again.
`, name, event.URL, status, now.Format(time.RFC3339), strings.TrimRight(uiURL, "/")+"/")
	return m.formatRFC5322(to, subject, now, body)
}

func (m *Mailer) buildPausedMessage(event domain.Event, uiURL string) []byte {
	now := m.now().UTC()
	name := headerSafe(event.Name)
	to := m.recipient(event)
	subject := "DropPulse: " + name + " paused (site blocked checks)"
	reason := strings.TrimSpace(headerSafe(event.PausedReason))
	if reason == "" {
		reason = "The target site repeatedly blocked or challenged automated checks."
	}
	lastErr := strings.TrimSpace(headerSafe(event.LastError))
	detailLine := ""
	if lastErr != "" {
		detailLine = "Last response: " + lastErr + "\n"
	}
	body := fmt.Sprintf(`%s has been automatically paused.

Reason: %s
%sTarget URL (open manually in your browser): %s
Checked: %s
Open DropPulse: %s

DropPulse does not bypass anti-bot walls or CAPTCHAs. You can check the page directly and re-enable the watch from DropPulse when ready.
`, name, reason, detailLine, event.URL, now.Format(time.RFC3339), strings.TrimRight(uiURL, "/")+"/")
	return m.formatRFC5322(to, subject, now, body)
}

func (m *Mailer) formatRFC5322(to, subject string, now time.Time, body string) []byte {
	body = strings.ReplaceAll(body, "\n", "\r\n")

	headers := []string{
		"From: DropPulse <" + m.from + ">",
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + now.Format(time.RFC1123Z),
		"Message-ID: " + m.messageID(now),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body)
}

func (m *Mailer) messageID(now time.Time) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("<%d@dropulse.local>", now.UnixNano())
	}
	domainPart := "dropulse.local"
	if at := strings.LastIndexByte(m.from, '@'); at >= 0 && at < len(m.from)-1 {
		domainPart = m.from[at+1:]
	}
	return "<" + hex.EncodeToString(buf) + "@" + domainPart + ">"
}

func validateAddress(raw string) error {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return fmt.Errorf("address must not be empty or padded")
	}
	address, err := mail.ParseAddress(raw)
	if err != nil || address.Name != "" || address.Address != raw {
		return fmt.Errorf("expected one bare address")
	}
	return nil
}

// headerSafe flattens characters that could end a line. Event names are
// already validated without controls; this is the second line of defence, and
// it also keeps a crafted name from faking extra lines in the message body.
func headerSafe(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}
