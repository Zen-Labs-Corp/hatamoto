package twofactor

import (
	"bytes"
	"regexp"
	"testing"
)

func TestNewRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != 10 {
		t.Fatalf("len %d err %v", len(codes), err)
	}
	re := regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}$`)
	seen := map[string]bool{}
	for _, c := range codes {
		if !re.MatchString(c) {
			t.Errorf("bad shape %q", c)
		}
		if seen[c] {
			t.Errorf("duplicate %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeAndHash(t *testing.T) {
	key := RecoveryKey("server-secret")
	h := HashRecoveryCode(key, "abcd-efgh-ijkl")
	for _, in := range []string{"ABCD-EFGH-IJKL", "abcdefghijkl", " abcd efgh ijkl ", "AbCd-eFgH-iJkL"} {
		if !bytes.Equal(h, HashRecoveryCode(key, in)) {
			t.Errorf("%q hashes differently", in)
		}
	}
	if bytes.Equal(h, HashRecoveryCode(key, "abcd-efgh-ijkm")) {
		t.Error("different codes hash equal")
	}
	if bytes.Equal(h, HashRecoveryCode(RecoveryKey("other"), "abcd-efgh-ijkl")) {
		t.Error("the hash must depend on the key")
	}
	if len(h) != 32 || bytes.Contains(h, []byte("ABCDEFGHIJKL")) {
		t.Error("hash form")
	}
	if NormalizeCode("ab-cd ef") != "ABCDEF" {
		t.Error("normalize")
	}
}
