// Package watch checks whether an HTTPS page is available, or delegates a
// recognised provider URL to that provider's checker.
//
// The generic checker is deliberately narrow: one GET, no redirects, no
// cookies, no proxy, and a dialer that refuses every non-public destination.
// These rules are what keep a user-supplied URL from becoming a request
// forgery primitive. Providers receive the same guarded client.
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
	"sync"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/providers"
	"github.com/ondrej-li/DropPulse/backend/internal/providers/cinemacity"
	"github.com/ondrej-li/DropPulse/backend/internal/providers/cinestar"
)

// Result is one check. StatusCode is nil when the request did not produce an
// HTTP response at all. Available is set by a provider check; nil means the
// result is the generic HTTP status result.
type Result struct {
	CheckKind  domain.CheckKind
	Outcome    domain.CheckOutcome
	StatusCode *int
	Available  *bool
	Detail     string
	RetryAfter time.Duration
	Duration   time.Duration
	Err        error
	CheckedAt  time.Time
}

type lookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type cachedFilms struct {
	films     []cinemacity.Film
	expiresAt time.Time
}

type cachedCineStarFilms struct {
	films     []cinestar.Film
	expiresAt time.Time
}

// Checker performs the HTTPS status check. It is safe for concurrent use.
type Checker struct {
	client   *http.Client
	registry *providers.Registry
	lookup   lookupFunc
	dial     dialFunc
	now      func() time.Time

	cacheMu       sync.Mutex
	filmCache     map[string]cachedFilms
	cinestarCache map[string]cachedCineStarFilms
}

// NewChecker builds a checker with a hard per-request timeout.
func NewChecker(timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	c := &Checker{
		registry:      providers.DefaultRegistry(),
		now:           time.Now,
		filmCache:     make(map[string]cachedFilms),
		cinestarCache: make(map[string]cachedCineStarFilms),
	}
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
			MaxIdleConns:           16,
			MaxIdleConnsPerHost:    2,
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

// Check performs one availability check. A built-in provider target is
// interpreted by that provider (seat availability for Kind 1, schedule
// availability for Kind 2); every other target is on exactly when it returns
// HTTP 200.
func (c *Checker) Check(ctx context.Context, target string) Result {
	start := c.now()
	result := Result{CheckedAt: start}
	defer func() {
		result.Duration = c.now().Sub(start)
		if result.Duration < 0 {
			result.Duration = 0
		}
	}()

	normalised, err := ValidateTargetURL(target)
	if err != nil {
		result.Outcome = domain.OutcomeError
		result.Err = err
		return result
	}
	reg := c.registry
	if reg == nil {
		reg = providers.DefaultRegistry()
	}
	if prov, kind, matched, matchErr := reg.Match(normalised); matched {
		result.CheckKind = kind
		if matchErr != nil {
			result.Outcome = domain.OutcomeError
			result.Err = matchErr
			return result
		}
		res, checkErr := prov.Check(ctx, c.client, normalised)
		if checkErr != nil {
			var blocked providers.Blocked
			if errors.As(checkErr, &blocked) {
				statusCode, retryAfter := blocked.BlockedInfo()
				result.Outcome = domain.OutcomeBlocked
				result.RetryAfter = retryAfter
				if statusCode > 0 {
					result.StatusCode = &statusCode
				}
			} else {
				result.Outcome = domain.OutcomeError
			}
			result.Err = checkErr
			return result
		}
		if res.Kind != "" {
			result.CheckKind = res.Kind
		}
		result.Outcome = domain.OutcomeOK
		available := res.Available
		result.Available = &available
		result.Detail = res.Detail
		return result
	}

	result.CheckKind = domain.CheckKindHTTPStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalised, nil)
	if err != nil {
		result.Outcome = domain.OutcomeError
		result.Err = err
		return result
	}
	req.Header.Set("User-Agent", "DropPulse/0.1 (+https://dp.ondralinek.cz; availability monitor)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	resp, err := c.client.Do(req)
	if err != nil {
		result.Outcome = domain.OutcomeError
		result.Err = err
		return result
	}
	defer resp.Body.Close()
	// Do not download a ticket page. Read only a small prefix to detect
	// challenge headers/markers while keeping connection handling tidy.
	prefix, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	status := resp.StatusCode
	result.StatusCode = &status

	retryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"), c.now())
	if status == http.StatusTooManyRequests {
		result.Outcome = domain.OutcomeBlocked
		result.RetryAfter = retryAfter
		result.Err = errors.New("target site rate-limited requests (HTTP 429)")
		return result
	}
	if (status == http.StatusForbidden || status == http.StatusServiceUnavailable) && isChallengeResponse(resp.Header, prefix) {
		result.Outcome = domain.OutcomeBlocked
		result.RetryAfter = retryAfter
		result.Err = fmt.Errorf("target site returned an anti-bot challenge (HTTP %d)", status)
		return result
	}
	result.Outcome = domain.OutcomeOK
	return result
}

func isChallengeResponse(headers http.Header, body []byte) bool {
	if strings.EqualFold(strings.TrimSpace(headers.Get("Cf-Mitigated")), "challenge") {
		return true
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{
		"just a moment",
		"cf-challenge",
		"challenge-platform",
		"attention required",
		"captcha",
		"verify you are human",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseRetryAfterHeader(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// ValidateProviderTarget applies built-in providers' URL rules. URLs from
// other hosts are left to ValidateTargetURL.
func ValidateProviderTarget(raw string) error {
	return providers.ValidateTarget(raw)
}

const (
	cinemaCityFilmCacheTTL = 10 * time.Minute
	cineStarFilmCacheTTL   = 15 * time.Minute
)

// ListCinemaCityFilms returns the scheduled and coming-soon Cinema City films
// for a cinema and format attribute, caching successful lookups briefly.
func (c *Checker) ListCinemaCityFilms(ctx context.Context, cinemaID, attr string) ([]cinemacity.Film, error) {
	cinemaID = strings.TrimSpace(cinemaID)
	if cinemaID == "" {
		cinemaID = "1052"
	}
	attr = strings.ToLower(strings.TrimSpace(attr))
	key := cinemaID + ":" + attr
	now := c.now()

	c.cacheMu.Lock()
	if entry, ok := c.filmCache[key]; ok && now.Before(entry.expiresAt) {
		copied := make([]cinemacity.Film, len(entry.films))
		copy(copied, entry.films)
		c.cacheMu.Unlock()
		return copied, nil
	}
	c.cacheMu.Unlock()

	films, err := cinemacity.ListFilms(ctx, c.client, cinemaID, attr)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	if c.filmCache == nil {
		c.filmCache = make(map[string]cachedFilms)
	}
	copied := make([]cinemacity.Film, len(films))
	copy(copied, films)
	c.filmCache[key] = cachedFilms{
		films:     copied,
		expiresAt: c.now().Add(cinemaCityFilmCacheTTL),
	}
	c.cacheMu.Unlock()
	return films, nil
}

// ListCineStarFilms returns the scheduled and upcoming CineStar films for a
// cinema slug, caching successful lookups in memory.
func (c *Checker) ListCineStarFilms(ctx context.Context, cinemaSlug string) ([]cinestar.Film, error) {
	cinemaSlug = strings.ToLower(strings.TrimSpace(cinemaSlug))
	if cinemaSlug == "" {
		cinemaSlug = "praha5"
	}
	now := c.now()

	c.cacheMu.Lock()
	if entry, ok := c.cinestarCache[cinemaSlug]; ok && now.Before(entry.expiresAt) {
		copied := make([]cinestar.Film, len(entry.films))
		copy(copied, entry.films)
		c.cacheMu.Unlock()
		return copied, nil
	}
	c.cacheMu.Unlock()

	films, err := cinestar.ListFilms(ctx, c.client, cinemaSlug)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	if c.cinestarCache == nil {
		c.cinestarCache = make(map[string]cachedCineStarFilms)
	}
	copied := make([]cinestar.Film, len(films))
	copy(copied, films)
	c.cinestarCache[cinemaSlug] = cachedCineStarFilms{
		films:     copied,
		expiresAt: c.now().Add(cineStarFilmCacheTTL),
	}
	c.cacheMu.Unlock()
	return films, nil
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
