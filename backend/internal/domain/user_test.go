package domain

import "testing"

func TestRoleValid(t *testing.T) {
	valid := []Role{RoleUser, RoleAdmin, RoleVendor, RoleAgent, RoleAI}
	for _, role := range valid {
		if !role.Valid() {
			t.Errorf("Role(%q).Valid() = false, want true", role)
		}
	}
	invalid := []Role{"", "owner", "User", "ADMIN", "superuser", "ai_assisted"}
	for _, role := range invalid {
		if role.Valid() {
			t.Errorf("Role(%q).Valid() = true, want false", role)
		}
	}
}
