package permtext

import (
	"slices"
	"testing"

	"hatamoto.dev/sdk"
)

func TestEveryPermissionHasHelp(t *testing.T) {
	for _, p := range sdk.Permissions {
		if Lookup(p).Help == "" || Lookup(p).Help == p {
			t.Errorf("permission %q has no help text", p)
		}
	}
}

func TestWarnSet(t *testing.T) {
	var got []string
	for _, p := range sdk.Permissions {
		if tx := Lookup(p); tx.Warn {
			got = append(got, p)
			if tx.Short == "" {
				t.Errorf("%q warns without a short label", p)
			}
		}
	}
	slices.Sort(got)
	want := []string{"builder.island", "http.fetch.any", "site.scripts"}
	if !slices.Equal(got, want) {
		t.Fatalf("warn set = %v, want %v", got, want)
	}
}

func TestUnknownFallsBackToName(t *testing.T) {
	if got := Lookup("future.perm"); got != (Text{Help: "future.perm"}) {
		t.Fatalf("got %+v", got)
	}
}
