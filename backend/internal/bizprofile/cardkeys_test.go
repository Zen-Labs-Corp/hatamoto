package bizprofile

import (
	"strings"
	"testing"
)

func TestCanonicalKeys(t *testing.T) {
	t.Parallel()
	norm := func(k string) (string, error) {
		if k == "bad key" {
			return "", errBadKey
		}
		return strings.ToLower(strings.ReplaceAll(k, "_", "-")), nil
	}
	tests := []struct {
		name string
		in   map[string]int
		want map[string]int
	}{
		{"no collision", map[string]int{"fr_CA": 1, "de-xx": 2}, map[string]int{"fr-ca": 1, "de-xx": 2}},
		{"the canonical key wins", map[string]int{"en_b": 1, "en-b": 2, "EN-B": 3}, map[string]int{"en-b": 2}},
		{"no canonical key: the first raw key wins", map[string]int{"en_b": 1, "EN-B": 2, "En-B": 3}, map[string]int{"en-b": 2}},
		{"an invalid key stays", map[string]int{"bad key": 1, "en": 2}, map[string]int{"bad key": 1, "en": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for i := 0; i < 50; i++ { // map order is random: the result must not be
				got := CanonicalKeys(tt.in, norm)
				if len(got) != len(tt.want) {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Fatalf("got %v, want %v", got, tt.want)
					}
				}
			}
		})
	}
}

var errBadKey = errString("bad key")

type errString string

func (e errString) Error() string { return string(e) }
