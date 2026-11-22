package domain

import "strings"

// ValidUUID reports whether s is a canonical 8-4-4-4-12 UUID. IDs are kept as
// strings at the Go boundary and cast to uuid in SQL, so path parameters and
// request bodies are checked before they reach Postgres.
func ValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

// NormaliseEmail lower-cases and trims an address. Empty input stays empty.
// Validation that an address is plausible is provider-side; this function only
// makes comparison deterministic.
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
