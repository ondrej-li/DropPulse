// Package cinemacity checks seat availability in Cinema City's public
// programme.
//
// A watch target is an ordinary Cinema City film page URL with the cinema and,
// optionally, the auditorium in the query string:
//
//	https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX
//
// The film page itself is never fetched. The checker reads the same public
// quickbook API the programme page uses, so the watch follows the programme
// without a browser, cookies or a login. The URL carries the target rather than
// adding provider-specific columns to the events table. The data API is not
// disallowed by robots.txt; the provider identifies itself and runs at the
// watch interval (five minutes by default).
package cinemacity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	apiBase          = "https://www.cinemacity.cz/cz/data-api-service/v1/quickbook/10101"
	userAgent        = "DropPulse/0.1 (+https://dp.ondralinek.cz; availability monitor)"
	maxResponseBytes = 1 << 20
	// A film's programme is normally one or two weeks; capping at 30 future
	// screening dates covers premiere presales without turning a sold-out check
	// into an unbounded crawl or exceeding requestTimeout.
	maxDatesPerCheck = 30
	requestTimeout   = 25 * time.Second
)

var (
	filmIDPattern   = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)
	cinemaIDPattern = regexp.MustCompile(`^[0-9]{1,8}$`)
	dateFetchDelay  = 100 * time.Millisecond
	nowFunc         = time.Now

	// eventDateTime has no offset because it is the cinema's local wall clock.
	prague = func() *time.Location {
		location, err := time.LoadLocation("Europe/Prague")
		if err != nil {
			return time.FixedZone("CET", 60*60)
		}
		return location
	}()
)

// BlockedError reports that Cinema City or its edge WAF refused automated
// access (HTTP 403/429 or an anti-bot/CAPTCHA challenge page). Callers must
// back off and never attempt to bypass it.
type BlockedError struct {
	StatusCode int
	RetryAfter time.Duration
	Reason     string
}

func (e *BlockedError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("Cinema City blocked request (HTTP %d)", e.StatusCode)
}

// CheckResult is the outcome of one Cinema City availability lookup.
type CheckResult struct {
	Available bool
	Detail    string
}

// Doer is the subset of http.Client used here. The caller supplies a client
// with the same dial guard and redirect policy as the generic checker.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Target is one film at one cinema. Auditorium and Date are optional filters.
type Target struct {
	FilmID     string
	CinemaID   string
	Auditorium string
	Date       string
}

// Match parses a cinemacity.cz URL. matched is false when the URL belongs to
// another host. When matched is true, a non-nil error explains why the URL is
// not a supported watch target; it must not silently fall back to a generic
// HTTP status check.
func Match(raw string) (target Target, matched bool, err error) {
	u, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil {
		return Target{}, false, nil
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host != "www.cinemacity.cz" && host != "cinemacity.cz" {
		return Target{}, false, nil
	}
	if u.Scheme != "https" {
		return Target{}, true, errors.New("URL must use https")
	}
	if u.User != nil {
		return Target{}, true, errors.New("URL must not contain credentials")
	}
	if port := u.Port(); port != "" && port != "443" {
		return Target{}, true, errors.New("Cinema City links must not include a port")
	}

	segments := pathSegments(u.Path)
	if len(segments) < 3 || segments[len(segments)-3] != "films" {
		return Target{}, true, errors.New("Cinema City links must point at a film page")
	}
	slug := segments[len(segments)-2]
	filmID := segments[len(segments)-1]
	if slug == "" || !filmIDPattern.MatchString(filmID) {
		return Target{}, true, errors.New("Cinema City film id is not valid")
	}

	query := u.Query()
	cinemaID := strings.TrimSpace(query.Get("cinema"))
	if cinemaID == "" {
		return Target{}, true, errors.New("Cinema City links must include ?cinema=<cinema id>")
	}
	if !cinemaIDPattern.MatchString(cinemaID) {
		return Target{}, true, errors.New("Cinema City cinema id is not valid")
	}

	auditorium := strings.TrimSpace(query.Get("auditorium"))
	if len([]rune(auditorium)) > 64 || strings.ContainsAny(auditorium, "\x00\r\n\t") {
		return Target{}, true, errors.New("Cinema City auditorium is not valid")
	}

	date := strings.TrimSpace(query.Get("date"))
	if date != "" && !validDate(date) {
		return Target{}, true, errors.New("Cinema City date must be YYYY-MM-DD")
	}

	return Target{
		FilmID:     filmID,
		CinemaID:   cinemaID,
		Auditorium: auditorium,
		Date:       date,
	}, true, nil
}

// Check reports whether at least one matching show has seats available. A
// request or decode failure is an error, never a sold-out result, so a
// temporary upstream problem cannot turn an event off.
func Check(ctx context.Context, doer Doer, target Target) (bool, error) {
	res, err := CheckDetailed(ctx, doer, target)
	return res.Available, err
}

// CheckDetailed checks seat availability and returns a human-readable summary
// of the first matching open show (or why none matched).
func CheckDetailed(ctx context.Context, doer Doer, target Target) (CheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	now := nowFunc().In(prague)
	dates := []string{target.Date}
	if target.Date == "" {
		var err error
		dates, err = fetchDates(ctx, doer, target, now)
		if err != nil {
			return CheckResult{}, err
		}
	}
	if len(dates) == 0 {
		return CheckResult{Available: false, Detail: "No upcoming dates scheduled"}, nil
	}

	for i, date := range dates {
		if i > 0 && dateFetchDelay > 0 {
			timer := time.NewTimer(dateFetchDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return CheckResult{}, fmt.Errorf("Cinema City request: %w", ctx.Err())
			case <-timer.C:
			}
		}
		events, err := fetchEvents(ctx, doer, target, date)
		if err != nil {
			return CheckResult{}, err
		}
		if show, ok := firstAvailable(events, target, now); ok {
			return CheckResult{Available: true, Detail: formatShowDetail(show)}, nil
		}
	}
	if len(dates) == 1 {
		return CheckResult{Available: false, Detail: "No open seats on " + dates[0]}, nil
	}
	return CheckResult{
		Available: false,
		Detail:    fmt.Sprintf("No open seats across %d dates", len(dates)),
	}, nil
}

type datesPayload struct {
	Body *struct {
		Dates []string `json:"dates"`
	} `json:"body"`
}

func fetchDates(ctx context.Context, doer Doer, target Target, now time.Time) ([]string, error) {
	today := now.Format("2006-01-02")
	until := now.AddDate(0, 0, 180).Format("2006-01-02")
	endpoint := apiBase + "/dates/with-film/" + url.PathEscape(target.FilmID) +
		"/until/" + until + "?" + url.Values{"attr": {""}, "ids": {target.CinemaID}}.Encode()

	var payload datesPayload
	if err := getJSON(ctx, doer, endpoint, &payload); err != nil {
		return nil, err
	}
	if payload.Body == nil {
		return nil, errors.New("Cinema City returned no programme body")
	}

	dates := make([]string, 0, len(payload.Body.Dates))
	for _, raw := range payload.Body.Dates {
		if !validDate(raw) {
			return nil, errors.New("Cinema City returned an invalid programme date")
		}
		if raw < today {
			continue
		}
		dates = append(dates, raw)
	}
	sort.Strings(dates)
	if len(dates) > maxDatesPerCheck {
		dates = dates[:maxDatesPerCheck]
	}
	return dates, nil
}

type eventsPayload struct {
	Body *struct {
		Events []event `json:"events"`
	} `json:"body"`
}

type event struct {
	FilmID             string  `json:"filmId"`
	CinemaID           string  `json:"cinemaId"`
	Auditorium         string  `json:"auditorium"`
	AuditoriumTinyName string  `json:"auditoriumTinyName"`
	EventDateTime      string  `json:"eventDateTime"`
	SoldOut            bool    `json:"soldOut"`
	AvailabilityRatio  float64 `json:"availabilityRatio"`
}

func fetchEvents(ctx context.Context, doer Doer, target Target, date string) ([]event, error) {
	endpoint := apiBase + "/cinema-events/with-film/" + url.PathEscape(target.FilmID) +
		"/at-date/" + url.PathEscape(date) + "?" +
		url.Values{"attr": {""}, "ids": {target.CinemaID}}.Encode()

	var payload eventsPayload
	if err := getJSON(ctx, doer, endpoint, &payload); err != nil {
		return nil, err
	}
	if payload.Body == nil {
		return nil, errors.New("Cinema City returned no events body")
	}
	return payload.Body.Events, nil
}

func getJSON(ctx context.Context, doer Doer, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build Cinema City request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("Cinema City request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Cinema City response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return errors.New("Cinema City response is too large")
	}

	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), nowFunc())
	if resp.StatusCode == http.StatusTooManyRequests {
		return &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "Cinema City rate-limited requests (HTTP 429)",
		}
	}
	if resp.StatusCode == http.StatusForbidden {
		return &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "Cinema City blocked request (HTTP 403)",
		}
	}
	if resp.StatusCode == http.StatusServiceUnavailable && looksLikeChallenge(resp.Header, body) {
		return &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "Cinema City returned an anti-bot challenge (HTTP 503)",
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Cinema City returned HTTP %d", resp.StatusCode)
	}
	if looksLikeChallenge(resp.Header, body) {
		return &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "Cinema City returned an anti-bot challenge page",
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Cinema City response: %w", err)
	}
	return nil
}

func looksLikeChallenge(headers http.Header, body []byte) bool {
	if strings.EqualFold(strings.TrimSpace(headers.Get("Cf-Mitigated")), "challenge") {
		return true
	}
	ct := strings.ToLower(headers.Get("Content-Type"))
	if strings.Contains(ct, "application/json") {
		return false
	}
	sample := body
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	lower := strings.ToLower(string(sample))
	for _, marker := range []string{
		"just a moment",
		"cf-challenge",
		"challenge-platform",
		"attention required",
		"captcha",
		"verify you are human",
		"access denied",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
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

// hasAvailable applies the target filters again even though the API endpoints
// already narrow the result. The extra check keeps a changed or misbehaving
// upstream response from turning on a watch for another film or auditorium.
// Shows that have already started are ignored: leftover seats on a past
// screening are not a ticket someone can buy.
func hasAvailable(events []event, target Target, now time.Time) bool {
	_, ok := firstAvailable(events, target, now)
	return ok
}

func firstAvailable(events []event, target Target, now time.Time) (event, bool) {
	for _, show := range events {
		if show.FilmID != "" && show.FilmID != target.FilmID {
			continue
		}
		if show.CinemaID != "" && show.CinemaID != target.CinemaID {
			continue
		}
		if target.Auditorium != "" && !auditoriumMatches(show, target.Auditorium) {
			continue
		}
		if started(show.EventDateTime, now) {
			continue
		}
		if !show.SoldOut && show.AvailabilityRatio > 0 {
			return show, true
		}
	}
	return event{}, false
}

func formatShowDetail(show event) string {
	room := strings.TrimSpace(show.Auditorium)
	if room == "" {
		room = strings.TrimSpace(show.AuditoriumTinyName)
	}
	when := strings.TrimSpace(show.EventDateTime)
	if parsed, err := time.Parse("2006-01-02T15:04:05", when); err == nil {
		when = parsed.Format("2006-01-02 15:04")
	}
	parts := make([]string, 0, 3)
	if room != "" {
		parts = append(parts, room)
	}
	if when != "" {
		parts = append(parts, when)
	}
	pct := int(math.Round(show.AvailabilityRatio * 100))
	if pct < 1 && show.AvailabilityRatio > 0 {
		pct = 1
	}
	if pct > 100 {
		pct = 100
	}
	if pct > 0 {
		parts = append(parts, fmt.Sprintf("~%d%% open", pct))
	}
	if len(parts) == 0 {
		return "Seats available"
	}
	return strings.Join(parts, " · ")
}

func auditoriumMatches(show event, wanted string) bool {
	return nameMatches(show.AuditoriumTinyName, wanted) ||
		nameMatches(show.Auditorium, wanted)
}

// nameMatches accepts the exact room name and a short name that prefixes a
// longer one at a word boundary, so "IMAX" also selects "IMAX VOLVO".
func nameMatches(name, wanted string) bool {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, wanted) {
		return true
	}
	if len(name) <= len(wanted) || !strings.EqualFold(name[:len(wanted)], wanted) {
		return false
	}
	switch name[len(wanted)] {
	case ' ', '-', '/':
		return true
	default:
		return false
	}
}

func started(raw string, now time.Time) bool {
	if raw == "" {
		return false
	}
	start, err := time.ParseInLocation("2006-01-02T15:04:05", raw, now.Location())
	if err != nil {
		return false
	}
	return !start.After(now)
}

func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" {
			return nil
		}
	}
	return segments
}

func validDate(raw string) bool {
	date, err := time.Parse("2006-01-02", raw)
	if err != nil || date.Format("2006-01-02") != raw {
		return false
	}
	return date.Year() >= 2000 && date.Year() <= 2100
}
