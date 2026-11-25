package server

import "testing"

func TestMergePatchContentType(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/merge-patch+json":                true,
		"Application/Merge-Patch+JSON; charset=utf-8": true,
		"application/json":                            false,
		"application/json-patch+json":                 false,
		"":                                            false,
		"garbage;;":                                   false,
	} {
		if got := isMergePatch(ct); got != want {
			t.Errorf("isMergePatch(%q) = %v, want %v", ct, got, want)
		}
	}
}
