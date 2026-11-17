package app

import (
	"strings"
	"testing"
)

func TestParseSignOutFlags(t *testing.T) {
	if yes, err := parseSignOutFlags(nil); err != nil || yes {
		t.Errorf("no args: yes=%v err=%v", yes, err)
	}
	if yes, err := parseSignOutFlags([]string{"--yes"}); err != nil || !yes {
		t.Errorf("--yes: yes=%v err=%v", yes, err)
	}
	if _, err := parseSignOutFlags([]string{"--yes", "extra"}); err == nil {
		t.Error("extra argument accepted")
	}
	if _, err := parseSignOutFlags([]string{"--nope"}); err == nil {
		t.Error("unknown flag accepted")
	}
}

func TestRenderSignOut(t *testing.T) {
	var b strings.Builder
	renderSignOut(&b, signOutCounts{Sessions: 4, APIKeys: 3, Tokens: 1, Previews: 2})
	want := "sessions      4 deleted\n" +
		"api keys      3 revoked\n" +
		"invite/reset  1 revoked\n" +
		"preview links 2 revoked\n" +
		"users, passwords and second factors are unchanged\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestUsageListsSignOutAll(t *testing.T) {
	if !strings.Contains(usage, "user sign-out-all") {
		t.Error("usage does not list user sign-out-all")
	}
}

func TestSignOutRefusalText(t *testing.T) {
	const want = "error: signs out every user and revokes every API key; re-run with --yes"
	if signOutRefusal != want {
		t.Errorf("refusal = %q, want %q", signOutRefusal, want)
	}
}
