package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/memstore"
	"github.com/ondrej-li/DropPulse/backend/internal/watch"
)

func localReq(method, path string, headers map[string]string, body any) *http.Request {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, rdr)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "127.0.0.1:18080"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestLocalTestAuthHeadlessRoleAndUserSwitching(t *testing.T) {
	mem := memstore.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	avail := true
	svc := events.NewService(mem, &stubChecker{
		result: watch.Result{
			CheckKind: domain.CheckKindSchedule,
			Outcome:   domain.OutcomeOK,
			Available: &avail,
			Detail:    "Scheduled · 2026-12-21 19:30",
			Duration:  80 * time.Millisecond,
			CheckedAt: time.Now().UTC(),
		},
	}, nil, logger, domain.User{}, "http://127.0.0.1:18080/")
	apiURL, _ := url.Parse("http://127.0.0.1:18080")
	cfg := config.Config{
		Environment:      "development",
		BindAddr:         "127.0.0.1",
		Port:             "18080",
		LocalTestAuth:    true,
		SingleUserName:   "admin",
		PublicAPIURL:     apiURL,
		WebAppURL:        apiURL,
		AllowedOrigins:   []string{"http://127.0.0.1:18080", "http://localhost:18080"},
		APIRatePerMinute: 600,
		MaxRequestBytes:  1 << 20,
	}
	srv, err := New(cfg, nil, nil, svc, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetStores(mem, mem)
	handler := srv.Handler()

	// 1. Default headless request without headers acts as admin.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, localReq(http.MethodGet, "/api/v1/me", nil, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("default /me = %d: %s", rec.Code, rec.Body.String())
	}
	var me meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.User.Role != domain.RoleAdmin || !me.LocalTestAuth {
		t.Fatalf("default persona = %+v", me)
	}

	// 2. Act as regular user 'dana' via X-Test-User + X-Test-Role (headless POST without Origin/CSRF).
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, localReq(http.MethodPost, "/api/v1/events", map[string]string{
		"X-Test-User": "dana",
		"X-Test-Role": "user",
	}, map[string]any{
		"name": "Dana CineStar Watch",
		"url":  "https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti?date=2026-12-21",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("dana create event = %d: %s", rec.Code, rec.Body.String())
	}
	var created domain.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.CheckKind != domain.CheckKindSchedule {
		t.Fatalf("created CheckKind = %q, want schedule", created.CheckKind)
	}

	// 3. Non-admin roles cannot access /api/v1/admin/*.
	for _, role := range []string{"user", "vendor", "agent", "ai"} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, localReq(http.MethodGet, "/api/v1/admin/overview", map[string]string{
			"X-Test-User": "actor-" + role,
			"X-Test-Role": role,
		}, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("role %s /admin/overview = %d, want 403", role, rec.Code)
		}
	}

	// 4. Cookie impersonation via /api/v1/dev/impersonate switches identity for subsequent requests.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, localReq(http.MethodPost, "/api/v1/dev/impersonate", nil, map[string]any{
		"user": "vendor-partner",
		"role": "vendor",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("impersonate = %d: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected persona cookie")
	}
	reqWithCookie := localReq(http.MethodGet, "/api/v1/me", nil, nil)
	reqWithCookie.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reqWithCookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.User.Username != "vendor-partner" || me.User.Role != domain.RoleVendor {
		t.Fatalf("cookie persona = %+v", me.User)
	}

	// 5. Banning a user blocks subsequent impersonation of that user with 403 account_suspended.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, localReq(http.MethodPost, "/api/v1/admin/users/"+me.User.ID+"/ban", nil, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin ban = %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, localReq(http.MethodGet, "/api/v1/me", map[string]string{"X-Test-User": me.User.ID}, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suspended user /me = %d, want 403", rec.Code)
	}

	// 6. Proxied or non-loopback requests are strictly rejected even when LOCAL_TEST_AUTH=true.
	proxied := localReq(http.MethodGet, "/api/v1/me", map[string]string{"X-Forwarded-For": "203.0.113.9"}, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, proxied)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("proxied request = %d, want 403", rec.Code)
	}

	nonLoopback := localReq(http.MethodGet, "/api/v1/me", nil, nil)
	nonLoopback.RemoteAddr = "198.51.100.4:5555"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, nonLoopback)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback RemoteAddr = %d, want 403", rec.Code)
	}

	badHost := localReq(http.MethodGet, "/api/v1/me", nil, nil)
	badHost.Host = "dp.ondralinek.cz"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, badHost)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback Host = %d, want 403", rec.Code)
	}
}
