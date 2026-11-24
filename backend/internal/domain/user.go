package domain

import "time"

// Role is the account type attached to a user account. It is both the
// authorization role and the kind of actor the account represents.
type Role string

const (
	RoleUser   Role = "user"
	RoleAdmin  Role = "admin"
	RoleVendor Role = "vendor"
	RoleAgent  Role = "agent"
	RoleAI     Role = "ai"
)

// Valid reports whether r is a role the database accepts.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAdmin, RoleVendor, RoleAgent, RoleAI:
		return true
	default:
		return false
	}
}

// UserStatus is the lifecycle state of an account.
type UserStatus string

const (
	StatusActive    UserStatus = "active"
	StatusSuspended UserStatus = "suspended"
	StatusDeleted   UserStatus = "deleted"
)

// User is an authenticated DropPulse account. Provider-specific subject
// identifiers live in UserIdentity, never here.
type User struct {
	ID            string     `json:"id"`
	Username      string     `json:"username,omitempty"`
	Email         string     `json:"email,omitempty"`
	EmailVerified bool       `json:"email_verified"`
	DisplayName   string     `json:"display_name,omitempty"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	Role          Role       `json:"role"`
	Status        UserStatus `json:"status"`
	TermsVersion  string     `json:"terms_version,omitempty"`
	TermsAccepted *time.Time `json:"terms_accepted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	DeletedAt     *time.Time `json:"-"`
}

// UserIdentity is one provider account linked to a User.
type UserIdentity struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Provider      string     `json:"provider"`
	Subject       string     `json:"-"`
	Email         string     `json:"email,omitempty"`
	EmailVerified bool       `json:"email_verified"`
	DisplayName   string     `json:"display_name,omitempty"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
}

// ExternalIdentity is the normalised result of a provider login. Providers
// must not put unverified email addresses into User.Email; the value stays on
// the identity until it can be trusted.
type ExternalIdentity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	AvatarURL     string
}

// SessionKind distinguishes a browser cookie session from a mobile token
// session. Both use the same server-side session and revocation model.
type SessionKind string

const (
	SessionWeb    SessionKind = "web"
	SessionMobile SessionKind = "mobile"
)

// Session is a server-side login. Tokens are separate rows and are always
// stored hashed.
type Session struct {
	ID                string      `json:"id"`
	UserID            string      `json:"user_id"`
	Kind              SessionKind `json:"kind"`
	UserAgent         string      `json:"user_agent,omitempty"`
	IP                string      `json:"ip,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	LastUsedAt        time.Time   `json:"last_used_at"`
	IdleExpiresAt     time.Time   `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time   `json:"absolute_expires_at"`
	RevokedAt         *time.Time  `json:"revoked_at,omitempty"`
	CSRFTokenHash     []byte      `json:"-"`
}

// Principal is the authenticated caller attached to a request context.
type Principal struct {
	User         User
	Session      Session
	TokenID      string
	AuthKind     string // "web" or "bearer"
	Entitlements Entitlements
}

// OAuthFlow is a short-lived server-side OAuth transaction. State and browser
// binding values are stored only as SHA-256 hashes.
type OAuthFlow struct {
	ID           string
	Provider     string
	Intent       string
	UserID       string
	StateHash    []byte
	BrowserHash  []byte
	Nonce        string
	CodeVerifier string
	RedirectURI  string
	ReturnTo     string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
