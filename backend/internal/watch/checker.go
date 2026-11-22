// Package watch checks whether an HTTPS page is available.
//
// The checker is deliberately narrow: one GET, no redirects, no cookies, no
// proxy, and a dialer that refuses every non-public destination. These rules
// are what keep a user-supplied URL from becoming a request forgery primitive.
package watch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// Result is one check. StatusCode is nil when the request did not produce an
// HTTP response at all.
type Result struct {
	StatusCode *int
	Err        error
	CheckedAt  time.Time
}

type lookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Checker performs the HTTPS status check. It is safe for concurrent use.
type Checker struct {
	client *http.Client
	lookup lookupFunc
	dial   dialFunc
	now    func() time.Time
}

// NewChecker builds a checker with a hard per-request timeout.
func NewChecker(timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	c := &Checker{now: time.Now}
	c.lookup = net.DefaultResolver.LookupNetIP
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	c.dial = dialer.DialContext
	c.client = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// A proxy would receive the request and bypass the dial guard.
			Proxy:                  nil,
			DialContext:            c.dialPublic,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  5 * time.Second,
			IdleConnTimeout:        30 * time.Second,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 64 << 10,
			ForceAttemptHTTP2:      true,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// A 3xx is a status result, not a new destination. Following it
			// would let a public URL redirect the checker somewhere else.
			return http.ErrUseLastResponse
		},
	}
	return c
}

// Check requests target once. Exactly HTTP 200 means the event is on.
func (c *Checker) Check(ctx context.Context, target string) Result {
	result := Result{CheckedAt: c.now()}
	normalised, err := ValidateTargetURL(target)
	if err != nil {
		result.Err = err
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalised, nil)
	if err != nil {
		result.Err = err
		return result
	}
	req.Header.Set("User-Agent", "DropPulse/0.1 (local availability check)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	resp, err := c.client.Do(req)
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()
	// Do not download a ticket page. The status line and headers are all this
	// watch uses; draining a little keeps the connection handling tidy.
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	status := resp.StatusCode
	result.StatusCode = &status
	return result
}

// ValidateTargetURL accepts only absolute HTTPS URLs without credentials or a
// fragment. IP literals must be public; hostnames are checked again after DNS
// resolution at request time.
func ValidateTargetURL(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", errors.New("URL is required")
	}
	if len(target) > domain.MaxEventURLLength {
		return "", fmt.Errorf("URL must be at most %d characters", domain.MaxEventURLLength)
	}
	if strings.ContainsAny(target, "\x00\r\n\t ") {
		return "", errors.New("URL must not contain whitespace or control characters")
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", errors.New("URL is not valid")
	}
	if u.Scheme != "https" {
		return "", errors.New("URL must use https")
	}
	if u.Host == "" || u.Opaque != "" {
		return "", errors.New("URL must be absolute")
	}
	if u.User != nil {
		return "", errors.New("URL must not contain credentials")
	}
	if u.Fragment != "" {
		return "", errors.New("URL must not contain a fragment")
	}
	if port := u.Port(); port != "" {
		n, parseErr := strconv.Atoi(port)
		if parseErr != nil || n < 1 || n > 65535 {
			return "", errors.New("URL port must be between 1 and 65535")
		}
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("URL must include a host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", errors.New("URL must not point at localhost")
	}
	if strings.Contains(host, "%") {
		return "", errors.New("URL must not contain an IPv6 zone")
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if !IsPublicIP(ip) {
			return "", errors.New("URL must point at a public address")
		}
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// IsPublicIP reports whether addr is safe for the checker to dial. Loopback,
// private, link-local, multicast, unspecified, CGNAT, benchmarking and
// reserved ranges are all excluded.
func IsPublicIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	if addr.Is4() {
		b := addr.As4()
		switch {
		case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // 100.64.0.0/10 CGNAT
			return false
		case b[0] == 192 && b[1] == 0 && b[2] == 0: // 192.0.0.0/24
			return false
		case b[0] == 198 && (b[1] == 18 || b[1] == 19): // 198.18.0.0/15
			return false
		case b[0] >= 240: // 240.0.0.0/4 reserved, includes broadcast
			return false
		}
	}
	return true
}

// dialPublic resolves the host once, rejects the whole answer if any address
// is non-public, and then dials one of the resolved IPs directly. Dialling the
// IP rather than the name removes the DNS-rebinding window between resolution
// and connection.
func (c *Checker) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split destination: %w", err)
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		ip = ip.Unmap()
		if !IsPublicIP(ip) {
			return nil, fmt.Errorf("refusing non-public address %s", ip)
		}
		return c.dial(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	addresses, err := c.lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range addresses {
		if !IsPublicIP(ip) {
			return nil, fmt.Errorf("refusing non-public address %s for %s", ip.Unmap(), host)
		}
	}
	var lastErr error
	for _, ip := range addresses {
		conn, dialErr := c.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
