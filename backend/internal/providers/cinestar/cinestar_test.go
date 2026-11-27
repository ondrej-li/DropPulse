package cinestar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

type fakeDoer struct {
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	return f.respond(req)
}

func htmlResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleProgrammeHTML encodes a realistic Nuxt 3 devalue __NUXT_DATA__ array
// matching cinestar.cz's live SSR structure.
const sampleProgrammeHTML = `<!doctype html><html><body>
<a href="/cz/praha5/filmy/movie/10100-duna-cast-treti">Duna: Část třetí</a>
<a href="/cz/praha5/filmy/movie/11583-hunger-games-usvit-sklizne">Hunger Games: Úsvit sklizně</a>
<script type="application/json" id="__NUXT_DATA__">[
  ["ShallowReactive", 1],
  {"data": 2},
  ["ShallowReactive", 3],
  {"hashScreenings": 4},
  {"scheduledEventsEntries": 5},
  [6, 15],
  {"EventId": 7, "Start": 8, "Finish": 9, "TitleId": 10, "ObjectId": 11, "Properties": 12},
  "1900001",
  "2026-12-21T18:30:00+00:00",
  "2026-12-21T21:15:00+00:00",
  "10100",
  "163",
  [13, 14],
  {"Code": "6", "Name": "GOLD CLASS"},
  {"Code": "52", "Name": "Orig. znění"},
  {"EventId": 16, "Start": 17, "Finish": 18, "TitleId": 19, "ObjectId": 11, "Properties": 20},
  "1900002",
  "2026-11-28T16:00:00+00:00",
  "2026-11-28T18:40:00+00:00",
  "11583",
  [21],
  {"Code": "48", "Name": "Standard"}
]</script>
</body></html>`

func TestMatch(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		matched bool
		want    Target
		wantErr bool
	}{
		{
			name:    "praha5 dune on 21/12",
			raw:     "https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti?date=2026-12-21",
			matched: true,
			want:    Target{CinemaSlug: "praha5", TitleID: "10100", Date: "2026-12-21"},
		},
		{
			name:    "www host with format tag",
			raw:     "https://www.cinestar.cz/cz/praha9/filmy/movie/10100-duna-cast-treti?date=2026-12-21&tag=GOLD+CLASS",
			matched: true,
			want:    Target{CinemaSlug: "praha9", TitleID: "10100", Date: "2026-12-21", Tag: "GOLD CLASS"},
		},
		{
			name:    "reject seat mode on schedule-only provider",
			raw:     "https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti?mode=seats",
			matched: true,
			wantErr: true,
		},
		{
			name:    "unknown cinema slug",
			raw:     "https://cinestar.cz/cz/atlantis/filmy/movie/10100-duna",
			matched: true,
			wantErr: true,
		},
		{
			name:    "other host",
			raw:     "https://www.cinemacity.cz/films/duna/8105s2r?cinema=1052",
			matched: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, matched, err := Match(tc.raw)
			if matched != tc.matched {
				t.Fatalf("matched = %v, want %v", matched, tc.matched)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCheckDetailedScheduleDateAndTag(t *testing.T) {
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 27, 10, 0, 0, 0, prague) }
	defer func() { nowFunc = oldNow }()

	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return htmlResponse(http.StatusOK, nil, sampleProgrammeHTML), nil
	}}

	// 1. Dune 3 on 2026-12-21 in GOLD CLASS -> ON (18:30Z = 19:30 CET in Prague).
	res, err := CheckDetailed(context.Background(), doer, Target{
		CinemaSlug: "praha5",
		TitleID:    "10100",
		Date:       "2026-12-21",
		Tag:        "GOLD CLASS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Kind != domain.CheckKindSchedule || !strings.Contains(res.Detail, "2026-12-21 19:30") || !strings.Contains(res.Detail, "GOLD CLASS") {
		t.Fatalf("unexpected scheduled result: %+v", res)
	}

	// 2. Dune 3 on 2026-12-22 -> OFF ("Not scheduled on 2026-12-22 yet").
	resOff, err := CheckDetailed(context.Background(), doer, Target{
		CinemaSlug: "praha5",
		TitleID:    "10100",
		Date:       "2026-12-22",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resOff.Available || !strings.Contains(resOff.Detail, "Not scheduled on 2026-12-22 yet") {
		t.Fatalf("expected OFF for unscheduled date, got %+v", resOff)
	}
}

func TestCheckDetailedDetectsWaitingRoomAsBlocked(t *testing.T) {
	doer := &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		hdr := http.Header{
			"Content-Type": []string{"text/html"},
			"Set-Cookie":   []string{"__cfwaitingroom=queue-token; Path=/; HttpOnly"},
		}
		return htmlResponse(http.StatusOK, hdr, "<html>Queue</html>"), nil
	}}
	_, err := CheckDetailed(context.Background(), doer, Target{CinemaSlug: "praha5", TitleID: "10100"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected BlockedError on waiting room, got %v", err)
	}
}

func TestListFilmsExtractsScheduledAndComingSoon(t *testing.T) {
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 27, 10, 0, 0, 0, prague) }
	defer func() { nowFunc = oldNow }()

	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/filmy") {
			return htmlResponse(http.StatusOK, nil, `<!doctype html><html><body>
				<a href="/cz/praha5/filmy/movie/99999-avatar-3">Avatar: Oheň a popel</a>
			</body></html>`), nil
		}
		return htmlResponse(http.StatusOK, nil, sampleProgrammeHTML), nil
	}}

	films, err := ListFilms(context.Background(), doer, "praha5")
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 3 {
		t.Fatalf("got %d films, want 3: %+v", len(films), films)
	}
	if !films[0].Scheduled || !films[1].Scheduled || films[2].Scheduled {
		t.Fatalf("expected 2 scheduled + 1 unscheduled film: %+v", films)
	}
	for _, f := range films {
		if _, matched, err := Match(f.URL); !matched || err != nil {
			t.Fatalf("generated URL %q rejected by Match: matched=%v err=%v", f.URL, matched, err)
		}
	}
}
