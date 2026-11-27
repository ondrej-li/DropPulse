package providers

import (
	"testing"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

func TestRegistryCapabilitiesAndClassifyURL(t *testing.T) {
	reg := DefaultRegistry()
	list := reg.Providers()
	if len(list) != 2 {
		t.Fatalf("got %d providers, want 2", len(list))
	}
	if list[0].Name() != "cinemacity" || list[0].Capability() != CapabilitySeats {
		t.Fatalf("provider[0] = %s (%s)", list[0].Name(), list[0].Capability())
	}
	if list[1].Name() != "cinestar" || list[1].Capability() != CapabilityScheduleOnly {
		t.Fatalf("provider[1] = %s (%s)", list[1].Name(), list[1].Capability())
	}

	cases := []struct {
		url  string
		want domain.CheckKind
	}{
		{
			url:  "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
			want: domain.CheckKindSeats,
		},
		{
			url:  "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX&date=2026-12-21&mode=schedule",
			want: domain.CheckKindSchedule,
		},
		{
			url:  "https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti?date=2026-12-21",
			want: domain.CheckKindSchedule,
		},
		{
			url:  "https://example.com/availability",
			want: domain.CheckKindHTTPStatus,
		},
	}
	for _, tc := range cases {
		if got := ClassifyURL(tc.url); got != tc.want {
			t.Errorf("ClassifyURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}

	if err := ValidateTarget("https://cinestar.cz/cz/praha5/filmy/movie/10100-duna?mode=seats"); err == nil {
		t.Fatal("CineStar with mode=seats must be rejected")
	}
}
