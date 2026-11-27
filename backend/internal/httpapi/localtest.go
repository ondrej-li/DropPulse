package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

const testPersonaCookie = "dp_test_persona"

// TestUserStore resolves or creates test accounts when LOCAL_TEST_AUTH=true.
type TestUserStore interface {
	EnsureTestUser(ctx context.Context, ident, email string, role domain.Role, explicitRole bool) (domain.User, domain.Entitlements, error)
}

var _ TestUserStore = (*store.Store)(nil)

type impersonateRequest struct {
	User  string `json:"user"`
	Role  string `json:"role"`
	Email string `json:"email"`
}

// localTestAuth is the strictly-guarded authentication wrapper for local
// headless testing (LOCAL_TEST_AUTH=true). Every request must originate
// directly from a loopback peer without reverse-proxy forwarding headers and
// target a loopback Host header. Callers may act as any user or role via:
//
//   - X-Test-User / X-Test-Role / X-Test-Email request headers (stateless CLI / scripts)
//   - POST /api/v1/dev/impersonate cookie (headless browsers / local UI switcher)
func (s *Server) localTestAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.LocalTestAuth || s.cfg.Environment == "production" {
			s.unauthenticated(w, r)
			return
		}
		if !isDirectLoopbackRequest(r) {
			writeError(w, http.StatusForbidden, "loopback_only", "local test auth only accepts direct loopback requests without proxy headers")
			return
		}
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "host_not_allowed", "local test auth only accepts loopback Host headers")
			return
		}
		// Protect against cross-site browser requests while allowing headless
		// HTTP clients (which omit Origin and Sec-Fetch-Site) without friction.
		if !isSafeMethod(r.Method) {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "cross-site browser requests are not allowed")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) && !s.cfg.OriginAllowed(origin) {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
				return
			}
		}

		if ok, retry := s.limiter.Allow("local-test:"+s.clientIP(r), s.cfg.APIRatePerMinute, time.Minute); !ok {
			seconds := int(retry.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; try again shortly")
			return
		}

		ident, email, role, explicitRole, err := s.resolveTestPersona(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_test_persona", err.Error())
			return
		}
		if s.testUsers == nil {
			internalError(w)
			return
		}
		user, entitlements, err := s.testUsers.EnsureTestUser(r.Context(), ident, email, role, explicitRole)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "user_not_found", "requested test user does not exist")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_test_persona", err.Error())
			return
		}
		if user.Status != domain.StatusActive {
			writeError(w, http.StatusForbidden, "account_suspended", "this account is suspended or inactive")
			return
		}

		principal := domain.Principal{
			User:         user,
			AuthKind:     "local_test",
			Entitlements: entitlements,
		}
		ctx := context.WithValue(r.Context(), principalContextKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) resolveTestPersona(r *http.Request) (ident, email string, role domain.Role, explicitRole bool, err error) {
	hdrUser := strings.TrimSpace(r.Header.Get("X-Test-User"))
	hdrRole := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Test-Role")))
	hdrEmail := strings.TrimSpace(r.Header.Get("X-Test-Email"))

	if hdrUser != "" || hdrRole != "" || hdrEmail != "" {
		ident = hdrUser
		email = hdrEmail
		if ident == "" {
			if hdrRole != "" && hdrRole != string(domain.RoleAdmin) {
				ident = "test-" + hdrRole
			} else {
				ident = s.cfg.SingleUserName
			}
		}
		if hdrRole != "" {
			role = domain.Role(hdrRole)
			if !role.Valid() {
				return "", "", "", false, errors.New("X-Test-Role must be one of: admin, user, vendor, agent, ai")
			}
			explicitRole = true
		}
		return ident, email, role, explicitRole, nil
	}

	if rawCookie := cookieValue(r, testPersonaCookie); rawCookie != "" && len(rawCookie) <= 512 {
		if vals, parseErr := url.ParseQuery(rawCookie); parseErr == nil {
			cUser := strings.TrimSpace(vals.Get("user"))
			cRole := strings.ToLower(strings.TrimSpace(vals.Get("role")))
			cEmail := strings.TrimSpace(vals.Get("email"))
			if cUser != "" || cRole != "" {
				if cUser == "" {
					cUser = s.cfg.SingleUserName
				}
				if cRole != "" {
					role = domain.Role(cRole)
					if !role.Valid() {
						return "", "", "", false, errors.New("persona role must be one of: admin, user, vendor, agent, ai")
					}
					explicitRole = true
				}
				return cUser, cEmail, role, explicitRole, nil
			}
		}
	}

	defaultName := s.cfg.SingleUserName
	if defaultName == "" {
		defaultName = "admin"
	}
	return defaultName, "", domain.RoleAdmin, false, nil
}

func isDirectLoopbackRequest(r *http.Request) bool {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return false
		}
	}
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = parsedHost
	}
	ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(host), "[]"))
	if err != nil {
		return false
	}
	return ip.Unmap().IsLoopback()
}

func (s *Server) handleDevListUsers(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	var users []domain.AdminUser
	if s.adminStore != nil {
		list, err := s.adminStore.AdminListUsers(r.Context(), 100)
		if err == nil {
			users = list
		}
	}
	if users == nil {
		users = []domain.AdminUser{{User: principal.User}}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current": principal.User,
		"users":   users,
		"roles":   []domain.Role{domain.RoleAdmin, domain.RoleUser, domain.RoleVendor, domain.RoleAgent, domain.RoleAI},
	})
}

func (s *Server) handleDevImpersonate(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.LocalTestAuth || s.testUsers == nil {
		http.NotFound(w, r)
		return
	}
	var req impersonateRequest
	if err := decodeJSON(w, r, s.cfg.MaxRequestBytes, &req); err != nil {
		badRequest(w, err)
		return
	}
	ident := strings.TrimSpace(req.User)
	roleStr := strings.ToLower(strings.TrimSpace(req.Role))
	email := strings.TrimSpace(req.Email)
	var (
		role         domain.Role
		explicitRole bool
	)
	if roleStr != "" {
		role = domain.Role(roleStr)
		if !role.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_test_persona", "role must be one of: admin, user, vendor, agent, ai")
			return
		}
		explicitRole = true
	}
	if ident == "" {
		if explicitRole && role != domain.RoleAdmin {
			ident = "test-" + string(role)
		} else {
			ident = s.cfg.SingleUserName
			if ident == "" {
				ident = "admin"
			}
		}
	}

	user, ent, err := s.testUsers.EnsureTestUser(r.Context(), ident, email, role, explicitRole)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user_not_found", "requested test user does not exist")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_test_persona", err.Error())
		return
	}

	cookieVals := url.Values{}
	cookieVals.Set("user", user.ID)
	if explicitRole {
		cookieVals.Set("role", string(user.Role))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     testPersonaCookie,
		Value:    cookieVals.Encode(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         user,
		"entitlements": ent,
	})
}

func (s *Server) handleDevClearImpersonate(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     testPersonaCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeNoContent(w)
}
