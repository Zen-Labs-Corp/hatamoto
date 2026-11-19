package sdk

import (
	"slices"
	"testing"
)

func TestEntitiesFilterPermission(t *testing.T) {
	p := Permission("entities.filter")
	if PermEntitiesFilter != p {
		t.Fatalf("PermEntitiesFilter = %q, want %q", PermEntitiesFilter, p)
	}
	if !slices.Contains(Permissions, p) || !ValidPermission(p) || !ValidGrant(p) {
		t.Errorf("entities.filter must be a permission and a valid grant")
	}
	m := Manifest{Name: "hello-world", Version: "0.1.0", SDK: "^0.23", Permissions: []string{PermEntitiesFilter}}
	if err := m.Validate(); err != nil {
		t.Errorf("manifest asking entities.filter: %v", err)
	}
}
