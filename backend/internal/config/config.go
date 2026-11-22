// Package config loads and validates the backend's environment configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort              = "8080"
	defaultPublicAPIURL      = "http://localhost:8080"
	defaultWebAppURL         = "http://localhost:8000"
	defaultSessionIdle       = 30 * 24 * time.Hour
	defaultSessionAbsolute   = 90 * 24 * time.Hour
	defaultOAuthFlowTTL      = 10 * time.Minute
	defaultDBMaxConns        = int32(10)
	defaultAuthRatePerMinute = 30
	defaultAPIRatePerMinute  = 600
	defaultMaxRequestBytes   = int64(1 << 20)
)

// ProviderConfig holds the credentials for one OAuth provider. A provider is
// enabled only when its required fields are present.
type ProviderConfig struct {
	Name                 string
	ClientID             string
	ClientSecret         string
	AppleTeamID          string
	AppleKeyID           string
	ApplePrivateKey      string
	FacebookGraphVersion string
}

// Config is the validated runtime configuration. Secrets are kept only in
// memory and are never logged or serialised.
type Config struct {
	Environment string
	Port        string

	DatabaseURL string
	DBMaxConns  int32

	PublicAPIURL   *url.URL
	WebAppURL      *url.URL
	AllowedOrigins []string

	CookieSecure       bool
	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration
	OAuthFlowTTL       time.Duration

	RunMigrations bool
	LogLevel      slog.Level

	AdminEmails map[string]struct{}
	Providers   map[string]ProviderConfig

	TrustedProxyPrefixes []netip.Prefix
	AuthRatePerMinute    int
	APIRatePerMinute     int
	MaxRequestBytes      int64
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom is the testable form of Load.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	get := func(key string) string {
		value, _ := lookup(key)
		return strings.TrimSpace(value)
	}
	getDefault := func(key, fallback string) string {
		if value := get(key); value != "" {
			return value
		}
		return fallback
	}

	cfg := Config{
		Environment:        getDefault("APP_ENV", "development"),
		Port:               getDefault("PORT", defaultPort),
		DatabaseURL:        get("DATABASE_URL"),
		RunMigrations:      true,
		LogLevel:           slog.LevelInfo,
		AdminEmails:        make(map[string]struct{}),
		Providers:          make(map[string]ProviderConfig),
		SessionIdleTTL:     defaultSessionIdle,
		SessionAbsoluteTTL: defaultSessionAbsolute,
		OAuthFlowTTL:       defaultOAuthFlowTTL,
		AuthRatePerMinute:  defaultAuthRatePerMinute,
		APIRatePerMinute:   defaultAPIRatePerMinute,
		MaxRequestBytes:    defaultMaxRequestBytes,
		DBMaxConns:         defaultDBMaxConns,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if err := validatePort(cfg.Port); err != nil {
		return Config{}, err
	}

	var err error
	cfg.PublicAPIURL, err = parseServiceURL(getDefault("PUBLIC_API_URL", defaultPublicAPIURL), "PUBLIC_API_URL")
	if err != nil {
		return Config{}, err
	}
	cfg.WebAppURL, err = parseServiceURL(getDefault("WEB_APP_URL", defaultWebAppURL), "WEB_APP_URL")
	if err != nil {
		return Config{}, err
	}
	if cfg.PublicAPIURL.Scheme == "http" && !isLoopbackURL(cfg.PublicAPIURL) {
		return Config{}, errors.New("PUBLIC_API_URL must use https unless it points at localhost")
	}
	if cfg.WebAppURL.Scheme == "http" && !isLoopbackURL(cfg.WebAppURL) {
		return Config{}, errors.New("WEB_APP_URL must use https unless it points at localhost")
	}
	cfg.CookieSecure = cfg.PublicAPIURL.Scheme == "https"

	cfg.AllowedOrigins, err = parseAllowedOrigins(get("ALLOWED_ORIGINS"), cfg.PublicAPIURL, cfg.WebAppURL)
	if err != nil {
		return Config{}, err
	}
	if cfg.CookieSecure {
		// A secure deployment must not accept credentialed CORS requests from
		// an insecure origin: the Secure cookie would not be sent, and an
		// attacker-controlled http origin must never be trusted.
		for _, origin := range cfg.AllowedOrigins {
			if !strings.HasPrefix(origin, "https://") {
				return Config{}, fmt.Errorf("ALLOWED_ORIGINS must use https when PUBLIC_API_URL does (got %q)", origin)
			}
		}
	}

	if value := get("SESSION_IDLE_TTL"); value != "" {
		cfg.SessionIdleTTL, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_IDLE_TTL: %w", err)
		}
	}
	if value := get("SESSION_ABSOLUTE_TTL"); value != "" {
		cfg.SessionAbsoluteTTL, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_ABSOLUTE_TTL: %w", err)
		}
	}
	if value := get("OAUTH_FLOW_TTL"); value != "" {
		cfg.OAuthFlowTTL, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("OAUTH_FLOW_TTL: %w", err)
		}
	}
	if cfg.SessionIdleTTL <= 0 || cfg.SessionAbsoluteTTL <= 0 || cfg.OAuthFlowTTL <= 0 {
		return Config{}, errors.New("session and OAuth flow durations must be positive")
	}
	if cfg.SessionAbsoluteTTL < cfg.SessionIdleTTL {
		return Config{}, errors.New("SESSION_ABSOLUTE_TTL must be at least SESSION_IDLE_TTL")
	}
	if cfg.OAuthFlowTTL > time.Hour {
		return Config{}, errors.New("OAUTH_FLOW_TTL must not exceed one hour")
	}

	if value := get("RUN_MIGRATIONS"); value != "" {
		cfg.RunMigrations, err = strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("RUN_MIGRATIONS: %w", err)
		}
	}
	if value := get("DB_MAX_CONNS"); value != "" {
		n, parseErr := strconv.Atoi(value)
		if parseErr != nil || n < 1 || n > 100 {
			return Config{}, errors.New("DB_MAX_CONNS must be an integer between 1 and 100")
		}
		cfg.DBMaxConns = int32(n)
	}
	if value := get("LOG_LEVEL"); value != "" {
		level, parseErr := parseLogLevel(value)
		if parseErr != nil {
			return Config{}, parseErr
		}
		cfg.LogLevel = level
	}
	if value := get("AUTH_RATE_LIMIT_PER_MINUTE"); value != "" {
		cfg.AuthRatePerMinute, err = parsePositiveInt(value, "AUTH_RATE_LIMIT_PER_MINUTE")
		if err != nil {
			return Config{}, err
		}
	}
	if value := get("API_RATE_LIMIT_PER_MINUTE"); value != "" {
		cfg.APIRatePerMinute, err = parsePositiveInt(value, "API_RATE_LIMIT_PER_MINUTE")
		if err != nil {
			return Config{}, err
		}
	}
	if value := get("MAX_REQUEST_BYTES"); value != "" {
		n, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || n < 1024 || n > 16<<20 {
			return Config{}, errors.New("MAX_REQUEST_BYTES must be between 1024 and 16777216")
		}
		cfg.MaxRequestBytes = n
	}

	cfg.TrustedProxyPrefixes, err = parsePrefixes(getDefault("TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128"))
	if err != nil {
		return Config{}, err
	}

	for _, raw := range strings.Split(get("ADMIN_EMAILS"), ",") {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n") {
			return Config{}, fmt.Errorf("ADMIN_EMAILS contains an invalid address %q", email)
		}
		cfg.AdminEmails[email] = struct{}{}
	}

	if err := loadProviders(&cfg, get, getDefault); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Provider returns a configured provider by name.
func (c Config) Provider(name string) (ProviderConfig, bool) {
	p, ok := c.Providers[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// ProviderNames returns the configured provider names in a stable order.
func (c Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for _, name := range []string{"google", "apple", "facebook"} {
		if _, ok := c.Providers[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

// OriginAllowed reports whether origin is an exact configured frontend origin
// or the API's own origin. It never performs prefix or wildcard matching.
func (c Config) OriginAllowed(origin string) bool {
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return false
	}
	for _, allowed := range c.AllowedOrigins {
		if canonical == allowed {
			return true
		}
	}
	return false
}

func loadProviders(cfg *Config, get func(string) string, getDefault func(string, string) string) error {
	googleID := get("GOOGLE_CLIENT_ID")
	googleSecret := get("GOOGLE_CLIENT_SECRET")
	if (googleID == "") != (googleSecret == "") {
		return errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together")
	}
	if googleID != "" {
		cfg.Providers["google"] = ProviderConfig{
			Name:         "google",
			ClientID:     googleID,
			ClientSecret: googleSecret,
		}
	}

	appleID := get("APPLE_CLIENT_ID")
	appleTeam := get("APPLE_TEAM_ID")
	appleKeyID := get("APPLE_KEY_ID")
	appleKey := get("APPLE_PRIVATE_KEY")
	appleKeyFile := get("APPLE_PRIVATE_KEY_FILE")
	set := 0
	for _, value := range []string{appleID, appleTeam, appleKeyID} {
		if value != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		return errors.New("APPLE_CLIENT_ID, APPLE_TEAM_ID and APPLE_KEY_ID must be set together")
	}
	if appleKey != "" && appleKeyFile != "" {
		return errors.New("set only one of APPLE_PRIVATE_KEY and APPLE_PRIVATE_KEY_FILE")
	}
	if appleKeyFile != "" {
		content, err := os.ReadFile(appleKeyFile)
		if err != nil {
			return fmt.Errorf("read APPLE_PRIVATE_KEY_FILE: %w", err)
		}
		appleKey = string(content)
	}
	if set == 3 {
		if appleKey == "" {
			return errors.New("APPLE_PRIVATE_KEY or APPLE_PRIVATE_KEY_FILE is required when Apple login is configured")
		}
		cfg.Providers["apple"] = ProviderConfig{
			Name:            "apple",
			ClientID:        appleID,
			AppleTeamID:     appleTeam,
			AppleKeyID:      appleKeyID,
			ApplePrivateKey: strings.ReplaceAll(appleKey, `\n`, "\n"),
		}
	}

	facebookID := get("FACEBOOK_CLIENT_ID")
	facebookSecret := get("FACEBOOK_CLIENT_SECRET")
	if (facebookID == "") != (facebookSecret == "") {
		return errors.New("FACEBOOK_CLIENT_ID and FACEBOOK_CLIENT_SECRET must be set together")
	}
	if facebookID != "" {
		version := getDefault("FACEBOOK_GRAPH_VERSION", "v23.0")
		if !validGraphVersion(version) {
			return errors.New("FACEBOOK_GRAPH_VERSION must look like vNN.N")
		}
		cfg.Providers["facebook"] = ProviderConfig{
			Name:                 "facebook",
			ClientID:             facebookID,
			ClientSecret:         facebookSecret,
			FacebookGraphVersion: version,
		}
	}
	return nil
}

func parseServiceURL(raw, name string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s must use http or https", name)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s must be an absolute origin without credentials, query or fragment", name)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("%s must not contain a path", name)
	}
	u.Path = ""
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	if u.Scheme == "http" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	return u, nil
}

func isLoopbackURL(u *url.URL) bool {
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func parseAllowedOrigins(raw string, apiURL, webURL *url.URL) ([]string, error) {
	origins := make([]string, 0, 4)
	if strings.TrimSpace(raw) == "" {
		origins = append(origins, canonicalURLOrigin(webURL))
	} else {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			canonical, err := canonicalOrigin(part)
			if err != nil {
				return nil, fmt.Errorf("ALLOWED_ORIGINS: %w", err)
			}
			origins = append(origins, canonical)
		}
		if len(origins) == 0 {
			return nil, errors.New("ALLOWED_ORIGINS must contain at least one origin")
		}
	}
	apiOrigin := canonicalURLOrigin(apiURL)
	if !contains(origins, apiOrigin) {
		origins = append(origins, apiOrigin)
	}
	return origins, nil
}

func canonicalURLOrigin(u *url.URL) string {
	host := strings.ToLower(u.Host)
	switch u.Scheme {
	case "https":
		host = strings.TrimSuffix(host, ":443")
	case "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return u.Scheme + "://" + host
}

func canonicalOrigin(raw string) (string, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return "", errors.New("origin must not be empty or padded")
	}
	if raw == "*" {
		return "", errors.New("wildcard origins are not allowed")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("origin %q must use http or https", raw)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin %q must be an exact origin", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("origin %q must not contain a path", raw)
	}
	return canonicalURLOrigin(u), nil
}

func validatePort(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("PORT must be an integer between 1 and 65535")
	}
	return nil
}

func parsePositiveInt(raw, name string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 1_000_000 {
		return 0, fmt.Errorf("%s must be an integer between 1 and 1000000", name)
	}
	return n, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("LOG_LEVEL must be debug, info, warn or error")
	}
}

func parsePrefixes(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	prefixes := make([]netip.Prefix, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func validGraphVersion(raw string) bool {
	if len(raw) < 4 || raw[0] != 'v' {
		return false
	}
	dot := strings.IndexByte(raw, '.')
	if dot < 2 || dot == len(raw)-1 {
		return false
	}
	for _, part := range []string{raw[1:dot], raw[dot+1:]} {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
