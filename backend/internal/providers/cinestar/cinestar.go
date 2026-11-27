// Package cinestar checks whether a film screening is published on CineStar's
// public programme (Kind 2 — CapabilityScheduleOnly).
//
// Unlike Cinema City's quickbook JSON, CineStar's public programme embeds
// scheduled screenings inside Nuxt 3 SSR (#__NUXT_DATA__) while seat maps sit
// behind Colosseum WebSale session tokens and Cloudflare Waiting Room queues.
// Per DropPulse policy, this provider never touches websale.cinestar.cz or
// attempts to obtain seat maps — it monitors show/schedule availability only
// (for example: "Does CineStar Anděl screen Dune 3 on 2026-12-21?").
package cinestar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

const (
	siteBase         = "https://cinestar.cz"
	userAgent        = "DropPulse/0.1 (+https://dp.ondralinek.cz; availability monitor)"
	maxResponseBytes = 4 << 20 // CineStar SSR HTML pages are ~450 KB
	requestTimeout   = 20 * time.Second
)

// Cinema describes one of CineStar Czechia's 13 multiplexes.
type Cinema struct {
	Slug string `json:"slug"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

var (
	cinemas = []Cinema{
		{Slug: "praha5", ID: "1550", Name: "Praha — Anděl"},
		{Slug: "praha9", ID: "1538", Name: "Praha — Černý Most"},
		{Slug: "hradec", ID: "1535", Name: "Hradec Králové"},
		{Slug: "plzen", ID: "1547", Name: "Plzeň"},
		{Slug: "ostrava", ID: "1548", Name: "Ostrava"},
		{Slug: "olomouc", ID: "1541", Name: "Olomouc"},
		{Slug: "liberec", ID: "1549", Name: "Liberec"},
		{Slug: "pardubice", ID: "1542", Name: "Pardubice"},
		{Slug: "budejovice-igy", ID: "1543", Name: "České Budějovice — IGY Centrum"},
		{Slug: "budejovice", ID: "1532", Name: "České Budějovice — Čtyři Dvory"},
		{Slug: "jihlava", ID: "1553", Name: "Jihlava"},
		{Slug: "boleslav", ID: "1530", Name: "Mladá Boleslav"},
		{Slug: "opava", ID: "1552", Name: "Opava"},
	}

	cinemaBySlug = func() map[string]Cinema {
		m := make(map[string]Cinema, len(cinemas))
		for _, c := range cinemas {
			m[c.Slug] = c
		}
		return m
	}()

	titleIDPattern  = regexp.MustCompile(`^[0-9]{1,12}$`)
	movieSegPattern = regexp.MustCompile(`^([0-9]{1,12})(?:-([A-Za-z0-9_-]{1,160}))?$`)
	nuxtDataRe      = regexp.MustCompile(`(?is)<script[^>]*\bid=["']__NUXT_DATA__["'][^>]*>(.*?)</script>`)
	anchorMovieRe   = regexp.MustCompile(`(?is)<a\b[^>]*\bhref=["'](/cz/([a-z0-9-]+)/filmy/movie/([0-9]{1,12})-([A-Za-z0-9_-]{1,160}))(?:#[^"']*)?["'][^>]*>(.*?)</a>`)
	hrefMovieRe     = regexp.MustCompile(`/cz/([a-z0-9-]+)/filmy/movie/([0-9]{1,12})-([A-Za-z0-9_-]{1,160})`)
	htmlTagStripRe  = regexp.MustCompile(`<[^>]+>`)
	nowFunc         = time.Now

	prague = func() *time.Location {
		loc, err := time.LoadLocation("Europe/Prague")
		if err != nil {
			return time.FixedZone("CET", 60*60)
		}
		return loc
	}()
)

// Cinemas returns a copy of all supported CineStar multiplexes.
func Cinemas() []Cinema {
	out := make([]Cinema, len(cinemas))
	copy(out, cinemas)
	return out
}

// ValidCinemaSlug reports whether slug is a known CineStar multiplex.
func ValidCinemaSlug(slug string) bool {
	_, ok := cinemaBySlug[strings.ToLower(strings.TrimSpace(slug))]
	return ok
}

// BlockedError reports rate limiting, HTTP 403, Cloudflare challenges, or
// Waiting Room queues (__cfwaitingroom).
type BlockedError struct {
	StatusCode int
	RetryAfter time.Duration
	Reason     string
}

func (e *BlockedError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("CineStar blocked request (HTTP %d)", e.StatusCode)
}

// BlockedInfo exposes status and Retry-After to the shared provider registry.
func (e *BlockedError) BlockedInfo() (int, time.Duration) {
	return e.StatusCode, e.RetryAfter
}

// Doer executes an HTTP request using the SSRF-guarded client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Target identifies a CineStar film at a specific multiplex with optional date
// and format tag filters.
type Target struct {
	CinemaSlug string
	TitleID    string
	Date       string
	Tag        string
}

// CheckResult is the outcome of one CineStar schedule check.
type CheckResult struct {
	Kind      domain.CheckKind
	Available bool
	Detail    string
}

// Film is one scheduled or upcoming movie on CineStar's programme.
type Film struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Scheduled bool   `json:"scheduled"`
}

// Match parses a cinestar.cz URL. matched is false for non-CineStar hosts.
func Match(raw string) (Target, bool, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Target{}, false, nil
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host != "cinestar.cz" && host != "www.cinestar.cz" {
		return Target{}, false, nil
	}
	if u.Scheme != "https" {
		return Target{}, true, errors.New("URL must use https")
	}
	if u.User != nil {
		return Target{}, true, errors.New("URL must not contain credentials")
	}
	if port := u.Port(); port != "" && port != "443" {
		return Target{}, true, errors.New("CineStar links must not include a port")
	}

	query := u.Query()
	switch strings.ToLower(strings.TrimSpace(query.Get("mode"))) {
	case "", "schedule":
	default:
		return Target{}, true, errors.New("CineStar only supports show schedule monitoring (seat availability is not exposed publicly)")
	}

	segments := pathSegments(u.Path)
	// Expected: /cz/<cinema-slug>/filmy/movie/<TitleId>[-<slug>]
	// or /en/<cinema-slug>/movies/movie/<TitleId>[-<slug>]
	var (
		pathCinema string
		movieSeg   string
	)
	if len(segments) >= 5 && (segments[0] == "cz" || segments[0] == "en") &&
		(segments[2] == "filmy" || segments[2] == "movies") && segments[3] == "movie" {
		pathCinema = strings.ToLower(segments[1])
		movieSeg = segments[4]
	} else if len(segments) >= 3 && (segments[0] == "filmy" || segments[0] == "movies") && segments[1] == "movie" {
		movieSeg = segments[2]
	} else {
		return Target{}, true, errors.New("CineStar links must point at a film page (/cz/<cinema>/filmy/movie/<id>-<slug>)")
	}

	m := movieSegPattern.FindStringSubmatch(movieSeg)
	if m == nil || !titleIDPattern.MatchString(m[1]) {
		return Target{}, true, errors.New("CineStar film id is not valid")
	}
	titleID := m[1]

	cinemaSlug := strings.ToLower(strings.TrimSpace(query.Get("cinema")))
	if cinemaSlug == "" {
		cinemaSlug = pathCinema
	}
	if cinemaSlug == "" {
		return Target{}, true, errors.New("CineStar links must specify a cinema slug (for example /cz/praha5/filmy/movie/...)")
	}
	if !ValidCinemaSlug(cinemaSlug) {
		return Target{}, true, fmt.Errorf("CineStar cinema %q is not recognised", cinemaSlug)
	}

	date := strings.TrimSpace(query.Get("date"))
	if date != "" && !validDate(date) {
		return Target{}, true, errors.New("CineStar date must be YYYY-MM-DD")
	}

	tag := strings.TrimSpace(query.Get("tag"))
	if len([]rune(tag)) > 48 || strings.ContainsAny(tag, "\x00\r\n\t") {
		return Target{}, true, errors.New("CineStar format tag is not valid")
	}

	return Target{
		CinemaSlug: cinemaSlug,
		TitleID:    titleID,
		Date:       date,
		Tag:        tag,
	}, true, nil
}

// CheckDetailed fetches the cinema's SSR programme page once and reports
// whether at least one upcoming screening matches target.
func CheckDetailed(ctx context.Context, doer Doer, target Target) (CheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	endpoint := siteBase + "/cz/" + url.PathEscape(target.CinemaSlug) + "/program"
	pageHTML, err := fetchHTML(ctx, doer, endpoint)
	if err != nil {
		return CheckResult{}, err
	}

	screenings, _, err := parseNuxtProgramme(pageHTML)
	if err != nil {
		return CheckResult{}, err
	}

	now := nowFunc().In(prague)
	matches := make([]screening, 0, 8)
	for _, sc := range screenings {
		if sc.TitleID != target.TitleID {
			continue
		}
		if !sc.Start.After(now) {
			continue
		}
		localStart := sc.Start.In(prague)
		if target.Date != "" && localStart.Format("2006-01-02") != target.Date {
			continue
		}
		if target.Tag != "" && !hasFormatTag(sc.Tags, target.Tag) {
			continue
		}
		sc.Start = localStart
		matches = append(matches, sc)
	}

	if len(matches) == 0 {
		detail := "No upcoming screenings scheduled yet"
		if target.Date != "" {
			detail = "Not scheduled on " + target.Date + " yet"
		}
		return CheckResult{
			Kind:      domain.CheckKindSchedule,
			Available: false,
			Detail:    detail,
		}, nil
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Start.Before(matches[j].Start)
	})
	return CheckResult{
		Kind:      domain.CheckKindSchedule,
		Available: true,
		Detail:    formatScreeningDetail(matches[0], len(matches)),
	}, nil
}

// ListFilms returns scheduled and upcoming films for cinemaSlug so users can
// pick a title directly in the wizard.
func ListFilms(ctx context.Context, doer Doer, cinemaSlug string) ([]Film, error) {
	cinemaSlug = strings.ToLower(strings.TrimSpace(cinemaSlug))
	if cinemaSlug == "" {
		cinemaSlug = "praha5"
	}
	if !ValidCinemaSlug(cinemaSlug) {
		return nil, fmt.Errorf("CineStar cinema %q is not recognised", cinemaSlug)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	progHTML, err := fetchHTML(ctx, doer, siteBase+"/cz/"+url.PathEscape(cinemaSlug)+"/program")
	if err != nil {
		return nil, err
	}
	screenings, nuxtMovies, err := parseNuxtProgramme(progHTML)
	if err != nil {
		return nil, err
	}

	now := nowFunc().In(prague)
	scheduledIDs := make(map[string]bool)
	for _, sc := range screenings {
		if sc.Start.After(now) && sc.TitleID != "" {
			scheduledIDs[sc.TitleID] = true
		}
	}

	byID := make(map[string]Film)
	mergeMovie := func(id, name, slug string) {
		id = strings.TrimSpace(id)
		if !titleIDPattern.MatchString(id) {
			return
		}
		slug = sanitiseSlug(slug)
		if slug == "" {
			slug = sanitiseSlug(name)
		}
		if slug == "" {
			slug = "film"
		}
		name = cleanMovieName(name)
		if name == "" {
			name = humaniseSlug(slug)
		}
		existing, exists := byID[id]
		if exists {
			// Prefer a real Czech title over a slug-derived fallback.
			if isSlugFallback(existing.Name, slug) && !isSlugFallback(name, slug) {
				existing.Name = name
			}
			existing.Scheduled = existing.Scheduled || scheduledIDs[id]
			byID[id] = existing
			return
		}
		byID[id] = Film{
			ID:        id,
			Name:      name,
			URL:       siteBase + "/cz/" + cinemaSlug + "/filmy/movie/" + id + "-" + slug,
			Scheduled: scheduledIDs[id],
		}
	}

	for _, m := range nuxtMovies {
		mergeMovie(m.ID, m.Name, m.Slug)
	}
	extractAnchorMovies(progHTML, mergeMovie)

	// Best-effort enrichment from /cz/<slug>/filmy for upcoming / unscheduled releases.
	if filmsHTML, filmsErr := fetchHTML(ctx, doer, siteBase+"/cz/"+url.PathEscape(cinemaSlug)+"/filmy"); filmsErr == nil {
		if _, extraMovies, parseErr := parseNuxtProgramme(filmsHTML); parseErr == nil {
			for _, m := range extraMovies {
				mergeMovie(m.ID, m.Name, m.Slug)
			}
		}
		extractAnchorMovies(filmsHTML, mergeMovie)
	} else {
		var blocked *BlockedError
		if errors.As(filmsErr, &blocked) {
			return nil, filmsErr
		}
	}

	out := make([]Film, 0, len(byID))
	for _, f := range byID {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scheduled != out[j].Scheduled {
			return out[i].Scheduled
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

type screening struct {
	EventID string
	TitleID string
	Start   time.Time
	Tags    []string
}

type rawMovieMeta struct {
	ID   string
	Name string
	Slug string
}

func parseNuxtProgramme(pageHTML string) ([]screening, []rawMovieMeta, error) {
	m := nuxtDataRe.FindStringSubmatch(pageHTML)
	if m == nil {
		return nil, nil, errors.New("CineStar programme page did not include __NUXT_DATA__")
	}
	var arr []any
	if err := json.Unmarshal([]byte(m[1]), &arr); err != nil {
		return nil, nil, fmt.Errorf("decode CineStar __NUXT_DATA__: %w", err)
	}
	if len(arr) == 0 {
		return nil, nil, errors.New("CineStar __NUXT_DATA__ is empty")
	}

	root, ok := resolveValue(arr, 0, 0).(map[string]any)
	if !ok {
		return nil, nil, errors.New("CineStar __NUXT_DATA__ root is not an object")
	}
	dataMap, ok := root["data"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("CineStar __NUXT_DATA__ missing data map")
	}

	var (
		screenings []screening
		movies     []rawMovieMeta
	)
	for _, entryVal := range dataMap {
		section, ok := entryVal.(map[string]any)
		if !ok {
			continue
		}
		if rawList, ok := section["scheduledEventsEntries"].([]any); ok {
			for _, item := range rawList {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				sc, ok := decodeScreening(obj)
				if ok {
					screenings = append(screenings, sc)
				}
			}
		}
		collectMoviesFromValue(section, &movies, 0)
	}
	return screenings, movies, nil
}

func decodeScreening(obj map[string]any) (screening, bool) {
	titleID := asString(obj["TitleId"])
	startStr := asString(obj["Start"])
	if titleID == "" || startStr == "" {
		return screening{}, false
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		start, err = time.ParseInLocation("2006-01-02T15:04:05", startStr, prague)
		if err != nil {
			return screening{}, false
		}
	}
	var tags []string
	if props, ok := obj["Properties"].([]any); ok {
		for _, p := range props {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if name := strings.TrimSpace(asString(pm["Name"])); name != "" {
				tags = append(tags, name)
			}
			if code := strings.TrimSpace(asString(pm["Code"])); code != "" {
				tags = append(tags, "#"+code)
			}
		}
	}
	return screening{
		EventID: asString(obj["EventId"]),
		TitleID: titleID,
		Start:   start,
		Tags:    tags,
	}, true
}

func collectMoviesFromValue(val any, out *[]rawMovieMeta, depth int) {
	if depth > 6 || val == nil {
		return
	}
	switch v := val.(type) {
	case []any:
		for _, elem := range v {
			collectMoviesFromValue(elem, out, depth+1)
		}
	case map[string]any:
		typename := asString(v["__typename"])
		id := firstNonEmpty(asString(v["MovieId"]), asString(v["TitleId"]))
		title := firstNonEmpty(asString(v["title"]), asString(v["movieTitle"]), asString(v["Title"]))
		uri := firstNonEmpty(asString(v["uri"]), asString(v["slug"]), asString(v["url"]))
		if id != "" && titleIDPattern.MatchString(id) && (title != "" || strings.Contains(typename, "movies_")) {
			slug := extractSlugFromURI(uri, id)
			*out = append(*out, rawMovieMeta{ID: id, Name: title, Slug: slug})
		}
		for k, child := range v {
			if k == "scheduledEventsEntries" || k == "cinemaHallsEntries" || k == "cinemas" {
				continue
			}
			collectMoviesFromValue(child, out, depth+1)
		}
	}
}

func extractSlugFromURI(uri, id string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(uri, "/"), "/")
	last := parts[len(parts)-1]
	prefix := id + "-"
	if strings.HasPrefix(last, prefix) {
		return last[len(prefix):]
	}
	return last
}

// resolveValue dereferences Nuxt 3's flat devalue array representation with a
// strict recursion cap so malformed or cyclic indices can never loop forever.
func resolveValue(arr []any, idx int, depth int) any {
	if depth > 12 || idx < 0 || idx >= len(arr) {
		return nil
	}
	raw := arr[idx]
	switch v := raw.(type) {
	case []any:
		if len(v) == 2 {
			if tag, ok := v[0].(string); ok {
				switch tag {
				case "ShallowReactive", "Reactive", "Ref", "ShallowRef":
					if nextIdx, ok := asIndex(v[1]); ok {
						return resolveValue(arr, nextIdx, depth+1)
					}
					return nil
				case "EmptyRef", "EmptyShallowRef":
					return nil
				}
			}
		}
		out := make([]any, 0, len(v))
		for _, elem := range v {
			if elemIdx, ok := asIndex(elem); ok {
				out = append(out, resolveValue(arr, elemIdx, depth+1))
			} else {
				out = append(out, elem)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, fieldVal := range v {
			if fieldIdx, ok := asIndex(fieldVal); ok {
				out[k] = resolveValue(arr, fieldIdx, depth+1)
			} else {
				out[k] = fieldVal
			}
		}
		return out
	default:
		return v
	}
}

func asIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		i := int(n)
		if float64(i) == n && i >= 0 {
			return i, true
		}
	case int:
		if n >= 0 {
			return n, true
		}
	}
	return 0, false
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case float64:
		if float64(int64(s)) == s {
			return strconv.FormatInt(int64(s), 10)
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractAnchorMovies(pageHTML string, add func(id, name, slug string)) {
	for _, m := range anchorMovieRe.FindAllStringSubmatch(pageHTML, -1) {
		id := m[3]
		slug := m[4]
		innerText := cleanMovieName(htmlTagStripRe.ReplaceAllString(m[5], " "))
		if innerText != "" &&
			!strings.EqualFold(innerText, "detail") &&
			!strings.EqualFold(innerText, "program") &&
			!strings.EqualFold(innerText, "koupit") &&
			!strings.EqualFold(innerText, "vstupenky") {
			add(id, innerText, slug)
		} else {
			add(id, "", slug)
		}
	}
	for _, m := range hrefMovieRe.FindAllStringSubmatch(pageHTML, -1) {
		add(m[2], "", m[3])
	}
}

func cleanMovieName(raw string) string {
	unescaped := html.UnescapeString(raw)
	fields := strings.Fields(strings.ReplaceAll(unescaped, "\u00a0", " "))
	name := strings.Join(fields, " ")
	if len([]rune(name)) > 120 {
		runes := []rune(name)
		name = string(runes[:120])
	}
	return name
}

func sanitiseSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case (r == '-' || r == '_' || r == ' ') && b.Len() > 0 && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 100 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func humaniseSlug(slug string) string {
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

func isSlugFallback(name, slug string) bool {
	return strings.EqualFold(strings.ReplaceAll(name, " ", "-"), slug)
}

func hasFormatTag(tags []string, wanted string) bool {
	wanted = strings.TrimSpace(wanted)
	if wanted == "" {
		return true
	}
	aliases := []string{wanted}
	switch {
	case strings.EqualFold(wanted, "TDL") || strings.EqualFold(wanted, "THEATRE DELUXE"):
		aliases = []string{"TDL", "THEATRE DELUXE", "THEATRE DELUXE FAMILY"}
	case strings.EqualFold(wanted, "GOLD CLASS"):
		aliases = []string{"GOLD CLASS", "#6"}
	case strings.EqualFold(wanted, "DOLBY ATMOS") || strings.EqualFold(wanted, "ATMOS"):
		aliases = []string{"Dolby Atmos", "ATMOS", "#59"}
	}
	for _, tag := range tags {
		for _, cand := range aliases {
			if strings.EqualFold(tag, cand) {
				return true
			}
		}
	}
	return false
}

func formatScreeningDetail(sc screening, total int) string {
	visibleTags := make([]string, 0, len(sc.Tags))
	for _, t := range sc.Tags {
		if strings.HasPrefix(t, "#") {
			continue
		}
		visibleTags = append(visibleTags, t)
	}
	detail := "Scheduled · " + sc.Start.Format("2006-01-02 15:04")
	if len(visibleTags) > 0 {
		detail += " (" + strings.Join(visibleTags, ", ") + ")"
	}
	if total > 1 {
		detail += fmt.Sprintf(" · +%d more", total-1)
	}
	return detail
}

func fetchHTML(ctx context.Context, doer Doer, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build CineStar request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	resp, err := doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("CineStar request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read CineStar response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return "", errors.New("CineStar response is too large")
	}

	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), nowFunc())
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "CineStar rate-limited requests (HTTP 429)",
		}
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "CineStar blocked request (HTTP 403)",
		}
	}
	if looksLikeChallengeOrQueue(resp.Header, body) {
		return "", &BlockedError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter,
			Reason:     "CineStar returned a waiting-room or anti-bot challenge",
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CineStar returned HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}

func looksLikeChallengeOrQueue(headers http.Header, body []byte) bool {
	if strings.EqualFold(strings.TrimSpace(headers.Get("Cf-Mitigated")), "challenge") {
		return true
	}
	for _, sc := range headers.Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(sc), "__cfwaitingroom") {
			return true
		}
	}
	sample := body
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	lower := strings.ToLower(string(sample))
	for _, marker := range []string{
		"__cfwaitingroom",
		"cf-challenge",
		"challenge-platform",
		"<title>just a moment",
		"verify you are human",
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

func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for _, seg := range segments {
		if seg == "" {
			return nil
		}
	}
	return segments
}

func validDate(raw string) bool {
	d, err := time.Parse("2006-01-02", raw)
	if err != nil || d.Format("2006-01-02") != raw {
		return false
	}
	return d.Year() >= 2000 && d.Year() <= 2100
}
