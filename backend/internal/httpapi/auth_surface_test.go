package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/config"
)

func multiUserTestServer(t *testing.T) *Server {
	t.Helper()
	apiURL, err := url.Parse("http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	webURL, err := url.Parse("http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		PublicAPIURL:       apiURL,
		WebAppURL:          webURL,
		AllowedOrigins:     []string{"http://localhost:8080"},
		AuthRatePerMinute:  60,
		APIRatePerMinute:   60,
		MaxRequestBytes:    1 << 20,
		SessionIdleTTL:     30 * 24 * time.Hour,
		SessionAbsoluteTTL: 90 * 24 * time.Hour,
		Providers: map[string]config.ProviderConfig{
			"google": {Name: "google", ClientID: "id", ClientSecret: "secret"},
			"apple":  {Name: "apple"},
		},
	}
	return &Server{
		cfg:      cfg,
		auth:     auth.NewService(cfg, nil, nil),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter:  NewLimiter(time.Now),
		now:      time.Now,
		frontend: []byte("<!doctype html><title>DropPulse</title><p id=\"landing\">Sign in</p>"),
	}
}

func TestLandingAndAppPagesArePublic(t *testing.T) {
	server := multiUserTestServer(t)
	for _, path := range []string{"/", "/app/"} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost:8080"+path, nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "landing") {
			t.Fatalf("GET %s body = %q", path, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Fatalf("GET %s CSP = %q", path, got)
		}
	}
}

func TestProviderListIsPublic(t *testing.T) {
	server := multiUserTestServer(t)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/v1/auth/providers", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET providers = %d, want 200; body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Providers []string `json:"providers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(body.Providers, ","); got != "google,apple" {
		t.Fatalf("providers = %q, want google,apple", got)
	}
}

func TestWatchRoutesRequireAuthentication(t *testing.T) {
	server := multiUserTestServer(t)
	handler := server.Handler()
	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/events"},
		{http.MethodPost, "/api/v1/events"},
		{http.MethodGet, "/api/v1/events/22222222-2222-2222-2222-222222222222"},
		{http.MethodPut, "/api/v1/events/22222222-2222-2222-2222-222222222222"},
		{http.MethodDelete, "/api/v1/events/22222222-2222-2222-2222-222222222222"},
		{http.MethodPost, "/api/v1/events/22222222-2222-2222-2222-222222222222/check"},
		{http.MethodGet, "/api/v1/providers/cinemacity/shows"},
		{http.MethodGet, "/api/v1/admin/overview"},
		{http.MethodGet, "/api/v1/admin/watchers"},
		{http.MethodPost, "/api/v1/admin/watchers"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/logs/checks"},
		{http.MethodGet, "/api/v1/admin/logs/audit"},
	}
	for _, request := range requests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(request.method, "http://localhost:8080"+request.path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", request.method, request.path, recorder.Code)
		}
	}
}
