package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

func randomToken(bytes int) (string, error) {
	if bytes < 16 {
		return "", errors.New("token entropy is too low")
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func sha256Bytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

func equalStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func newPKCE() (verifier, challenge string, err error) {
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// SafeReturnTo validates a post-login path. Only same-origin relative paths are
// accepted: no absolute URL, no scheme-relative //host, no backslash (which
// browsers may normalise to a slash) and no parent-directory segment.
func SafeReturnTo(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if len(raw) > 2048 {
		return "", errors.New("return path is too long")
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", errors.New("return path must be relative to this site")
	}
	if strings.ContainsAny(raw, "\\\r\n\x00") {
		return "", errors.New("return path contains an unsafe character")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("return path is not a valid URL path")
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("return path must not be an absolute URL")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == ".." {
			return "", errors.New("return path must not traverse directories")
		}
	}
	if parsed.Path == "" {
		return "/", nil
	}
	return raw, nil
}
