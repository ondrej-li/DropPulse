package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSafeReturnTo(t *testing.T) {
	valid := map[string]string{
		"":            "/",
		"/":           "/",
		"/events":     "/events",
		"/events?x=1": "/events?x=1",
	}
	for input, want := range valid {
		got, err := SafeReturnTo(input)
		if err != nil {
			t.Errorf("SafeReturnTo(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", input, got, want)
		}
	}
	invalid := []string{
		"https://evil.example/",
		"//evil.example/",
		"/\\evil.example/",
		"/a/../b",
		"/%2e%2e/b",
		"/events?x=1#top",
		"javascript:alert(1)",
	}
	for _, input := range invalid {
		if _, err := SafeReturnTo(input); err == nil {
			t.Errorf("SafeReturnTo(%q) = nil error", input)
		}
	}
}

func TestPKCEChallengeIsDeterministic(t *testing.T) {
	verifier, challenge, err := newPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("PKCE verifier length = %d", len(verifier))
	}
	if got := pkceChallenge(verifier); got != challenge {
		t.Fatalf("pkceChallenge = %q, want %q", got, challenge)
	}
	if strings.ContainsAny(verifier, "+/=") {
		t.Fatalf("PKCE verifier is not base64url: %q", verifier)
	}
}

func TestAppleClientSecret(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	now := time.Now().UTC()

	secret, err := AppleClientSecret("TEAMID1234", "KEYID12345", "cz.example.droppulse", string(keyPEM), now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(secret,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != "ES256" {
				t.Fatalf("unexpected signing method %q", token.Method.Alg())
			}
			return &key.PublicKey, nil
		},
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithAudience("https://appleid.apple.com"),
		jwt.WithIssuer("TEAMID1234"),
	)
	if err != nil {
		t.Fatalf("parse Apple client secret: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("Apple client secret is not valid")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("unexpected claims type")
	}
	if claims["sub"] != "cz.example.droppulse" {
		t.Fatalf("sub = %v", claims["sub"])
	}
	if parsed.Header["kid"] != "KEYID12345" {
		t.Fatalf("kid = %v", parsed.Header["kid"])
	}
}
