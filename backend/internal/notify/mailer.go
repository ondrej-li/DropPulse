// Package notify sends availability email through the local MTA.
//
// Mail is submitted with the MTA's sendmail command, the privileged local
// injection path. The SMTP listener on this host deliberately refuses relay to
// external recipients from loopback, so using it would require widening the
// relay policy; local submission does not. The message still passes through
// rspamd for DKIM signing because Postfix applies non_smtpd_milters to locally
// submitted mail. There is no remote SMTP, no authentication and no credential
// configuration here.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// Mailer sends one email per off->on transition.
type Mailer struct {
	sendmail string
	from     string
	to       string
	timeout  time.Duration
	now      func() time.Time
}

// New validates the addresses and sendmail command and returns a mailer for
// the local MTA.
func New(sendmail, from, to string, timeout time.Duration) (*Mailer, error) {
	if err := validateAddress(from); err != nil {
		return nil, fmt.Errorf("sender address: %w", err)
	}
	if err := validateAddress(to); err != nil {
		return nil, fmt.Errorf("recipient address: %w", err)
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

// SendEventOn sends the notification for an event that has just become
// available. The caller decides whether this transition is new; the mailer
// only formats and submits the message.
func (m *Mailer) SendEventOn(ctx context.Context, event domain.Event, uiURL string) error {
	message := m.buildMessage(event, uiURL)
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.sendmail, "-i", "-f", m.from, "--", m.to)
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

func (m *Mailer) buildMessage(event domain.Event, uiURL string) []byte {
	now := m.now().UTC()
	name := headerSafe(event.Name)
	subject := "DropPulse: " + name + " is on"
	status := "no HTTP response"
	if event.LastStatusCode != nil {
		status = fmt.Sprintf("HTTP %d", *event.LastStatusCode)
	}
	body := fmt.Sprintf(`%s is on.

URL: %s
Status: %s
Checked: %s
Open DropPulse: %s

You will get another email for this event after it goes off and then on again.
`, name, event.URL, status, now.Format(time.RFC3339), strings.TrimRight(uiURL, "/")+"/")
	body = strings.ReplaceAll(body, "\n", "\r\n")

	headers := []string{
		"From: DropPulse <" + m.from + ">",
		"To: " + m.to,
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
