package twofactor

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 4226 Appendix D: secret "12345678901234567890", 6 digits.
func TestCodeRFC4226(t *testing.T) {
	secret := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := Code(secret, int64(i)); got != w {
			t.Errorf("counter %d: %s, want %s", i, got, w)
		}
	}
}

// RFC 6238 Appendix B (SHA-1), the 8-digit values truncated to their last 6.
func TestCodeRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, c := range []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		if got := Code(secret, StepAt(time.Unix(c.unix, 0))); got != c.want {
			t.Errorf("t=%d: %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestCheckWindowAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111109, 0)
	cur := StepAt(now)

	for _, d := range []int64{-1, 0, 1} {
		step, ok := Check(secret, now, Code(secret, cur+d), 0)
		if !ok || step != cur+d {
			t.Errorf("offset %d: step=%d ok=%v", d, step, ok)
		}
	}
	for _, d := range []int64{-2, 2, 10} {
		if _, ok := Check(secret, now, Code(secret, cur+d), 0); ok {
			t.Errorf("offset %d must be refused", d)
		}
	}
	// A step at or below the last accepted one is a replay.
	if _, ok := Check(secret, now, Code(secret, cur), cur); ok {
		t.Error("replay of the last step accepted")
	}
	if _, ok := Check(secret, now, Code(secret, cur-1), cur); ok {
		t.Error("older step accepted after a newer one was used")
	}
	if _, ok := Check(secret, now, Code(secret, cur+1), cur); !ok {
		t.Error("a newer step must still be accepted")
	}
}

func TestCheckRejectsMalformed(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(59, 0)
	good := Code(secret, StepAt(now))
	for _, in := range []string{"", "12345", "1234567", "abcdef", good + "0", " "} {
		if _, ok := Check(secret, now, in, 0); ok && in != good {
			t.Errorf("%q accepted", in)
		}
	}
	if _, ok := Check(secret, now, good[:3]+" "+good[3:], 0); !ok {
		t.Error("a code typed with a space must work")
	}
}

func TestSecretAndURI(t *testing.T) {
	s, err := NewSecret()
	if err != nil || len(s) != 20 {
		t.Fatalf("secret len %d err %v", len(s), err)
	}
	s2, _ := NewSecret()
	if string(s) == string(s2) {
		t.Fatal("two secrets equal")
	}
	if strings.Contains(EncodeSecret(s), "=") {
		t.Error("base32 must have no padding")
	}
	u, err := url.Parse(ProvisioningURI("My Site", "ana@example.com", s))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || q.Get("secret") != EncodeSecret(s) ||
		q.Get("issuer") != "My Site" || q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("uri = %s", u)
	}
	if !strings.HasPrefix(u.Path, "/My Site:ana@example.com") {
		t.Fatalf("label = %q", u.Path)
	}
}
