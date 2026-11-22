package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

const facebookGraphBase = "https://graph.facebook.com/"

type facebookProvider struct {
	clientID     string
	clientSecret string
	graphVersion string
	httpClient   *http.Client
}

func (p *facebookProvider) Name() string { return "facebook" }

func (p *facebookProvider) AuthCodeURL(state, nonce, verifier, redirectURI string) string {
	// Facebook is OAuth 2.0 rather than OIDC; the nonce is accepted by the
	// signature for a common Provider interface but is not used here.
	_ = nonce
	cfg := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:   "https://www.facebook.com/" + p.graphVersion + "/dialog/oauth",
			TokenURL:  facebookGraphBase + p.graphVersion + "/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: []string{"email", "public_profile"},
	}
	return cfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func (p *facebookProvider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (domain.ExternalIdentity, error) {
	_ = nonce
	cfg := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:   "https://www.facebook.com/" + p.graphVersion + "/dialog/oauth",
			TokenURL:  facebookGraphBase + p.graphVersion + "/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: []string{"email", "public_profile"},
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.httpClient)
	token, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return domain.ExternalIdentity{}, fmt.Errorf("exchange Facebook code: %w", err)
	}
	if token.AccessToken == "" {
		return domain.ExternalIdentity{}, fmt.Errorf("Facebook returned no access token")
	}

	var debug struct {
		Data struct {
			AppID     string   `json:"app_id"`
			IsValid   bool     `json:"is_valid"`
			UserID    string   `json:"user_id"`
			ExpiresAt int64    `json:"expires_at"`
			Scopes    []string `json:"scopes"`
		} `json:"data"`
	}
	appToken := p.clientID + "|" + p.clientSecret
	if err := p.graphGet(ctx, "debug_token", url.Values{
		"input_token":  {token.AccessToken},
		"access_token": {appToken},
	}, &debug); err != nil {
		return domain.ExternalIdentity{}, err
	}
	if !debug.Data.IsValid || debug.Data.UserID == "" {
		return domain.ExternalIdentity{}, fmt.Errorf("Facebook token is not valid")
	}
	if debug.Data.AppID != p.clientID {
		return domain.ExternalIdentity{}, fmt.Errorf("Facebook token belongs to another app")
	}
	if debug.Data.ExpiresAt > 0 && debug.Data.ExpiresAt <= time.Now().Add(30*time.Second).Unix() {
		return domain.ExternalIdentity{}, fmt.Errorf("Facebook token has expired")
	}

	var profile struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}
	if err := p.graphGet(ctx, "me", url.Values{
		"fields":       {"id,name,email,picture.type(large)"},
		"access_token": {token.AccessToken},
	}, &profile); err != nil {
		return domain.ExternalIdentity{}, err
	}
	if profile.ID == "" || profile.ID != debug.Data.UserID {
		return domain.ExternalIdentity{}, fmt.Errorf("Facebook profile does not match the token")
	}
	return domain.ExternalIdentity{
		Provider:      "facebook",
		Subject:       profile.ID,
		Email:         domain.NormaliseEmail(profile.Email),
		EmailVerified: false, // Facebook provides no verification assertion.
		DisplayName:   strings.TrimSpace(profile.Name),
		AvatarURL:     strings.TrimSpace(profile.Picture.Data.URL),
	}, nil
}

func (p *facebookProvider) graphGet(ctx context.Context, path string, params url.Values, out any) error {
	endpoint := facebookGraphBase + p.graphVersion + "/" + path
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Facebook request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DropPulse/1.0")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Facebook request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Facebook Graph returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Facebook response: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Facebook response: %w", err)
	}
	return nil
}
