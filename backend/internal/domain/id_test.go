package domain

import "testing"

func TestValidUUID(t *testing.T) {
	valid := []string{
		"00000000-0000-0000-0000-000000000000",
		"7f1f4d8e-1234-4abc-9def-0123456789ab",
	}
	for _, value := range valid {
		if !ValidUUID(value) {
			t.Errorf("ValidUUID(%q) = false", value)
		}
	}
	invalid := []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-00000000000",
		"00000000-0000-0000-0000-00000000000g",
		"000000000000000000000000000000000000",
	}
	for _, value := range invalid {
		if ValidUUID(value) {
			t.Errorf("ValidUUID(%q) = true", value)
		}
	}
}

func TestNormaliseEmail(t *testing.T) {
	if got := NormaliseEmail("  User@Example.COM "); got != "user@example.com" {
		t.Fatalf("NormaliseEmail = %q", got)
	}
}
