package cinemacity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

func init() {
	// Keep unit tests instantaneous.
	dateFetchDelay = 0
}

type fakeDoer struct {
	calls   []string
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls = append(f.calls, req.URL.String())
	return f.respond(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		matched bool
		want    Target
		wantErr bool
	}{
		{
			name:    "film with auditorium",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
			matched: true,
			want:    Target{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX"},
		},
		{
			name:    "date filter",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&date=2026-12-17",
			matched: true,
			want:    Target{FilmID: "8105s2r", CinemaID: "1052", Date: "2026-12-17"},
		},
		{
			name:    "apex host",
			raw:     "https://cinemacity.cz/films/x/abc123?cinema=1",
			matched: true,
			want:    Target{FilmID: "abc123", CinemaID: "1"},
		},
		{
			name:    "language prefix",
			raw:     "https://www.cinemacity.cz/cz/films/x/abc123?cinema=1",
			matched: true,
			want:    Target{FilmID: "abc123", CinemaID: "1"},
		},
		{
			name:    "trailing dot",
			raw:     "https://www.cinemacity.cz./films/x/abc123?cinema=1",
			matched: true,
			want:    Target{FilmID: "abc123", CinemaID: "1"},
		},
		{
			name:    "other host",
			raw:     "https://example.com/films/x/abc123?cinema=1",
			matched: false,
		},
		{
			name:    "not a film page",
			raw:     "https://www.cinemacity.cz/cinemas/flora/1052?cinema=1052",
			matched: true,
			wantErr: true,
		},
		{
			name:    "missing cinema",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r",
			matched: true,
			wantErr: true,
		},
		{
			name:    "invalid cinema",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=flora",
			matched: true,
			wantErr: true,
		},
		{
			name:    "invalid film id",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/../?cinema=1052",
			matched: true,
			wantErr: true,
		},
		{
			name:    "invalid date",
			raw:     "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&date=2026-02-30",
			matched: true,
			wantErr: true,
		},
		{
			name:    "insecure scheme",
			raw:     "http://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052",
			matched: true,
			wantErr: true,
		},
		{
			name:    "credentials",
			raw:     "https://user:pass@www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052",
			matched: true,
			wantErr: true,
		},
		{
			name:    "unexpected port",
			raw:     "https://www.cinemacity.cz:8443/films/duna-cast-treti/8105s2r?cinema=1052",
			matched: true,
			wantErr: true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, matched, err := Match(test.raw)
			if matched != test.matched {
				t.Fatalf("matched = %v, want %v", matched, test.matched)
			}
			if test.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("target = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestHasAvailable(t *testing.T) {
	imax := Target{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX"}
	cases := []struct {
		name   string
		target Target
		events []event
		want   bool
	}{
		{
			name:   "tiny auditorium name",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "IMAX", AvailabilityRatio: 0.02}},
			want:   true,
		},
		{
			name:   "full auditorium name",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX VOLVO", AvailabilityRatio: 0.5}},
			want:   true,
		},
		{
			name:   "full name target",
			target: Target{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX VOLVO"},
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX VOLVO", AvailabilityRatio: 0.5}},
			want:   true,
		},
		{
			name:   "short name needs a word boundary",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "IMAX2", AvailabilityRatio: 0.5}},
			want:   false,
		},
		{
			name:   "sold out",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "IMAX", SoldOut: true, AvailabilityRatio: 0.5}},
			want:   false,
		},
		{
			name:   "no seats released",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "IMAX", AvailabilityRatio: 0}},
			want:   false,
		},
		{
			name:   "other auditorium",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "Sál 01", AvailabilityRatio: 0.9}},
			want:   false,
		},
		{
			name:   "other film",
			target: imax,
			events: []event{{FilmID: "9999s2r", CinemaID: "1052", AuditoriumTinyName: "IMAX", AvailabilityRatio: 0.2}},
			want:   false,
		},
		{
			name:   "other cinema",
			target: imax,
			events: []event{{FilmID: "8105s2r", CinemaID: "8888", AuditoriumTinyName: "IMAX", AvailabilityRatio: 0.2}},
			want:   false,
		},
		{
			name:   "missing film metadata still matches",
			target: imax,
			events: []event{{AuditoriumTinyName: "IMAX", AvailabilityRatio: 0.1}},
			want:   true,
		},
		{
			name:   "any auditorium when none is requested",
			target: Target{FilmID: "8105s2r", CinemaID: "1052"},
			events: []event{{FilmID: "8105s2r", CinemaID: "1052", AuditoriumTinyName: "Sál 01", AvailabilityRatio: 0.5}},
			want:   true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 12, 18, 9, 0, 0, 0, prague)
			if got := hasAvailable(test.events, test.target, now); got != test.want {
				t.Fatalf("hasAvailable = %v, want %v", got, test.want)
			}
		})
	}

	past := []event{{
		FilmID:             "8105s2r",
		CinemaID:           "1052",
		AuditoriumTinyName: "IMAX",
		EventDateTime:      "2026-12-18T10:40:00",
		AvailabilityRatio:  0.2,
	}}
	if hasAvailable(past, imax, time.Date(2026, 12, 18, 12, 0, 0, 0, prague)) {
		t.Fatal("a show that has already started must not turn the watch on")
	}
	if !hasAvailable(past, imax, time.Date(2026, 12, 18, 10, 0, 0, 0, prague)) {
		t.Fatal("an upcoming show with seats must turn the watch on")
	}
}

func TestCheckStopsAtFirstAvailableAuditorium(t *testing.T) {
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/dates/with-film/"):
			return jsonResponse(`{"body":{"dates":["2026-12-15","2026-12-16"]}}`), nil
		case strings.Contains(req.URL.Path, "/at-date/2026-12-15"):
			return jsonResponse(`{"body":{"events":[
				{"filmId":"8105s2r","cinemaId":"1052","auditorium":"IMAX VOLVO","auditoriumTinyName":"IMAX","soldOut":true,"availabilityRatio":0.5},
				{"filmId":"8105s2r","cinemaId":"1052","auditorium":"Sál 01","auditoriumTinyName":"Sál 01","soldOut":false,"availabilityRatio":0.9}
			]}}`), nil
		case strings.Contains(req.URL.Path, "/at-date/2026-12-16"):
			return jsonResponse(`{"body":{"events":[
				{"filmId":"8105s2r","cinemaId":"1052","auditorium":"IMAX VOLVO","auditoriumTinyName":"IMAX","soldOut":false,"availabilityRatio":0.0156}
			]}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL)
		}
	}}

	target := Target{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX"}
	available, err := Check(context.Background(), doer, target)
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("an available IMAX seat must turn the watch on")
	}
	if len(doer.calls) != 3 {
		t.Fatalf("made %d requests, want 3: %v", len(doer.calls), doer.calls)
	}
	if !strings.Contains(doer.calls[0], "/dates/with-film/8105s2r/until/") {
		t.Fatalf("first request = %s, want the dates endpoint", doer.calls[0])
	}
	if !strings.Contains(doer.calls[1], "/at-date/2026-12-15") {
		t.Fatalf("second request = %s, want the first date", doer.calls[1])
	}
	if !strings.Contains(doer.calls[2], "/at-date/2026-12-16") {
		t.Fatalf("third request = %s, want the second date", doer.calls[2])
	}
	for _, call := range doer.calls {
		if !strings.Contains(call, "ids=1052") {
			t.Fatalf("request %s does not filter on the cinema", call)
		}
	}
}

func TestCheckReportsNoSeats(t *testing.T) {
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/dates/with-film/") {
			return jsonResponse(`{"body":{"dates":["2026-12-15"]}}`), nil
		}
		return jsonResponse(`{"body":{"events":[
			{"filmId":"8105s2r","cinemaId":"1052","auditoriumTinyName":"IMAX","soldOut":true,"availabilityRatio":0.4}
		]}}`), nil
	}}

	available, err := Check(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052", Auditorium: "IMAX"})
	if err != nil {
		t.Fatal(err)
	}
	if available {
		t.Fatal("a sold-out programme must leave the watch off")
	}
}

func TestCheckWithDateSkipsDateDiscovery(t *testing.T) {
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/at-date/2026-12-17") {
			return nil, fmt.Errorf("unexpected request %s", req.URL)
		}
		return jsonResponse(`{"body":{"events":[
			{"filmId":"8105s2r","cinemaId":"1052","auditoriumTinyName":"IMAX","soldOut":false,"availabilityRatio":0.1}
		]}}`), nil
	}}

	available, err := Check(context.Background(), doer, Target{
		FilmID:     "8105s2r",
		CinemaID:   "1052",
		Auditorium: "IMAX",
		Date:       "2026-12-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("the fixed date has seats and must turn the watch on")
	}
	if len(doer.calls) != 1 {
		t.Fatalf("made %d requests, want 1", len(doer.calls))
	}
}

func TestCheckPropagatesUpstreamFailure(t *testing.T) {
	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}}

	if _, err := Check(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"}); err == nil {
		t.Fatal("an upstream failure must be an error, not a sold-out result")
	}
}

func TestCheckRejectsOversizedResponse(t *testing.T) {
	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return jsonResponse(strings.Repeat("x", maxResponseBytes+1)), nil
	}}

	if _, err := Check(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"}); err == nil {
		t.Fatal("an oversized response must be rejected")
	}
}

func TestFetchDatesSortsFiltersPastAndCaps(t *testing.T) {
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, prague)
	dates := make([]string, 0, maxDatesPerCheck+7)
	// Two past dates that must be skipped.
	dates = append(dates, "2026-11-29", "2026-11-30")
	for i := maxDatesPerCheck + 4; i >= 0; i-- {
		dates = append(dates, start.AddDate(0, 0, i).Format("2006-01-02"))
	}
	payload, err := json.Marshal(map[string]any{"body": map[string]any{"dates": dates}})
	if err != nil {
		t.Fatal(err)
	}
	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return jsonResponse(string(payload)), nil
	}}

	got, err := fetchDates(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"}, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxDatesPerCheck {
		t.Fatalf("got %d dates, want %d", len(got), maxDatesPerCheck)
	}
	if got[0] != "2026-12-01" {
		t.Fatalf("first date = %s, want 2026-12-01", got[0])
	}
	if got[len(got)-1] != start.AddDate(0, 0, maxDatesPerCheck-1).Format("2006-01-02") {
		t.Fatalf("last date = %s, want the end of the capped range", got[len(got)-1])
	}
}

func TestCheckDetectsRateLimitAndChallengeAsBlocked(t *testing.T) {
	t.Run("429 with Retry-After", func(t *testing.T) {
		doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"120"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}}
		_, err := CheckDetailed(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"})
		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("want BlockedError, got %v", err)
		}
		if blocked.StatusCode != 429 || blocked.RetryAfter != 120*time.Second {
			t.Fatalf("blocked = %+v", blocked)
		}
	})

	t.Run("503 cloudflare challenge", func(t *testing.T) {
		doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header: http.Header{
					"Content-Type": []string{"text/html"},
					"Cf-Mitigated": []string{"challenge"},
				},
				Body: io.NopCloser(strings.NewReader("<html><title>Just a moment...</title></html>")),
			}, nil
		}}
		_, err := CheckDetailed(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"})
		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("want BlockedError, got %v", err)
		}
	})
}

func TestCheckDetailedFormatsOpenShow(t *testing.T) {
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 12, 16, 10, 0, 0, 0, prague) }
	defer func() { nowFunc = oldNow }()

	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"body":{"events":[
			{"filmId":"8105s2r","cinemaId":"1052","auditorium":"IMAX VOLVO","auditoriumTinyName":"IMAX","eventDateTime":"2026-12-17T19:30:00","soldOut":false,"availabilityRatio":0.045}
		]}}`), nil
	}}
	res, err := CheckDetailed(context.Background(), doer, Target{
		FilmID:     "8105s2r",
		CinemaID:   "1052",
		Auditorium: "IMAX",
		Date:       "2026-12-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || !strings.Contains(res.Detail, "IMAX VOLVO") || !strings.Contains(res.Detail, "2026-12-17 19:30") {
		t.Fatalf("unexpected detail: %+v", res)
	}
}

func TestListFilmsBuildsCanonicalIMAXWatchURLs(t *testing.T) {
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/films/until/") || req.URL.Query().Get("attr") != "imax" {
			return nil, fmt.Errorf("unexpected catalogue request: %s", req.URL)
		}
		return jsonResponse(`{"body":{"films":[
			{"id":"7803s2r","name":"Hunger Games: Úsvit sklizně","length":159,"link":"https://www.cinemacity.cz/films/hunger-games-usvit-sklizne/7803s2r","releaseYear":"2026","releaseDate":"2026-11-19T00:00:00"},
			{"id":"6377s2r1","name":"Duna","length":168,"link":"https://www.cinemacity.cz/films/duna/6377s2r1","releaseYear":"2021","releaseDate":"2026-12-03T00:00:00"},
			{"id":"7803s2r","name":"Duplicate ID","length":159,"link":"https://www.cinemacity.cz/films/dup/7803s2r"}
		]}}`), nil
	}}

	films, err := ListFilms(context.Background(), doer, "1052", "imax")
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 2 {
		t.Fatalf("got %d films, want 2: %+v", len(films), films)
	}
	if films[0].ID != "7803s2r" || films[0].ReleaseDate != "2026-11-19" {
		t.Fatalf("first film = %+v", films[0])
	}
	wantURL := "https://www.cinemacity.cz/films/hunger-games-usvit-sklizne/7803s2r?auditorium=IMAX&cinema=1052"
	if films[0].URL != wantURL {
		t.Fatalf("film URL = %q, want %q", films[0].URL, wantURL)
	}
	if _, matched, err := Match(films[0].URL); !matched || err != nil {
		t.Fatalf("generated URL rejected by Match: matched=%v err=%v", matched, err)
	}
}

func TestBlockOnlineSalesAndScheduleMode(t *testing.T) {
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 12, 1, 10, 0, 0, 0, prague) }
	defer func() { nowFunc = oldNow }()

	lockedPayload := `{"body":{"events":[
		{"filmId":"8105s2r","cinemaId":"1052","auditorium":"IMAX VOLVO","auditoriumTinyName":"IMAX","eventDateTime":"2026-12-21T19:30:00","soldOut":false,"availabilityRatio":1.0,"compositeBookingLink":{"blockOnlineSales":true}}
	]}}`
	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return jsonResponse(lockedPayload), nil
	}}

	// 1. Default seat mode treats blockOnlineSales=true as OFF with clear detail.
	seatTarget, matched, err := Match("https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX&date=2026-12-21")
	if !matched || err != nil {
		t.Fatalf("Match seats: matched=%v err=%v", matched, err)
	}
	seatRes, err := CheckDetailed(context.Background(), doer, seatTarget)
	if err != nil {
		t.Fatal(err)
	}
	if seatRes.Available || seatRes.Kind != domain.CheckKindSeats || !strings.Contains(seatRes.Detail, "online sales not open yet") {
		t.Fatalf("seat mode with locked sales = %+v", seatRes)
	}

	// 2. Schedule mode (&mode=schedule) turns ON as soon as the show is listed on 2026-12-21.
	schedTarget, matched, err := Match("https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX&date=2026-12-21&mode=schedule")
	if !matched || err != nil {
		t.Fatalf("Match schedule: matched=%v err=%v", matched, err)
	}
	schedRes, err := CheckDetailed(context.Background(), doer, schedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !schedRes.Available || schedRes.Kind != domain.CheckKindSchedule || !strings.Contains(schedRes.Detail, "Scheduled") {
		t.Fatalf("schedule mode = %+v", schedRes)
	}
}

func TestListFilmsMergesComingSoonTrailers(t *testing.T) {
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/films/until/"):
			return jsonResponse(`{"body":{"films":[
				{"id":"7803s2r","name":"Hunger Games: Úsvit sklizně","length":159,"link":"https://www.cinemacity.cz/films/hunger-games-usvit-sklizne/7803s2r","releaseDate":"2026-11-19T00:00:00"}
			]}}`), nil
		case strings.Contains(req.URL.Path, "/trailers/byCinemaId/1052"):
			return jsonResponse(`{"body":[
				{"filmId":"7803s2r","filmName":"Hunger Games Duplicate","filmLink":"https://www.cinemacity.cz/films/hunger-games-usvit-sklizne/7803s2r"},
				{"filmId":"8105s2r","filmName":"Duna: Část třetí","filmLink":"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r"}
			]}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL)
		}
	}}

	films, err := ListFilms(context.Background(), doer, "1052", "imax")
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 2 {
		t.Fatalf("got %d films, want 2: %+v", len(films), films)
	}
	if !films[0].Scheduled || films[0].ID != "7803s2r" {
		t.Fatalf("expected scheduled film first: %+v", films[0])
	}
	if films[1].Scheduled || films[1].ID != "8105s2r" || films[1].Name != "Duna: Část třetí" {
		t.Fatalf("expected unscheduled coming-soon film second: %+v", films[1])
	}
}
