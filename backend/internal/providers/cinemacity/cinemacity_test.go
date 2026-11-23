package cinemacity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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

func TestFetchDatesSortsAndCaps(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dates := make([]string, 0, maxDatesPerCheck+5)
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

	got, err := fetchDates(context.Background(), doer, Target{FilmID: "8105s2r", CinemaID: "1052"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxDatesPerCheck {
		t.Fatalf("got %d dates, want %d", len(got), maxDatesPerCheck)
	}
	if got[0] != "2026-01-01" {
		t.Fatalf("first date = %s, want 2026-01-01", got[0])
	}
	if got[len(got)-1] != start.AddDate(0, 0, maxDatesPerCheck-1).Format("2006-01-02") {
		t.Fatalf("last date = %s, want the end of the capped range", got[len(got)-1])
	}
}
