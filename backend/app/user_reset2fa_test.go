package app

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/twofactor"
)

func TestResetTwoFactorWording(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rm   twofactor.Removed
		want string
	}{
		{twofactor.Removed{TOTP: true, Passkeys: 2, RecoveryCodes: 10}, "Removed the authenticator app, 2 passkeys and the recovery codes for ana@example.com."},
		{twofactor.Removed{Passkeys: 1}, "Removed 1 passkey for ana@example.com."},
		{twofactor.Removed{TOTP: true, RecoveryCodes: 3}, "Removed the authenticator app and the recovery codes for ana@example.com."},
		{twofactor.Removed{}, "ana@example.com had no second factor to remove."},
	}
	for _, c := range cases {
		if got := describeReset2FA(c.rm, "ana@example.com"); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestResetTwoFactorFlagsRejectPassword(t *testing.T) {
	t.Parallel()

	if _, err := parseUserFlags("reset-2fa", []string{"--email", "a@b.c", "--password", "x"}, false); err == nil {
		t.Error("--password accepted")
	}
	if _, err := parseUserFlags("reset-2fa", []string{"--email", "a@b.c"}, false); err != nil {
		t.Error(err)
	}
	if _, err := parseUserFlags("reset-2fa", nil, false); err == nil || !strings.Contains(err.Error(), "--email") {
		t.Errorf("missing email: %v", err)
	}
}
