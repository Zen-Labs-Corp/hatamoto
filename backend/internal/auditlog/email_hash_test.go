package auditlog

import (
	"strings"
	"testing"
)

func TestEmailHashNormalisesAndSeparatesSecrets(t *testing.T) {
	t.Parallel()

	h := NewEmailHasher("0123456789abcdef0123456789abcdef")
	got := h("Jane@Example.com ")
	if got != h("jane@example.com") {
		t.Fatalf("case and space must not change the hash: %q", got)
	}
	if other := NewEmailHasher("fedcba9876543210fedcba9876543210")("jane@example.com"); other == got {
		t.Fatal("a different secret must give a different hash")
	}
	if h("john@example.com") == got {
		t.Fatal("different addresses must differ")
	}
	if !strings.HasPrefix(got, EmailHashPrefix) || len(got) != len(EmailHashPrefix)+32 {
		t.Fatalf("shape = %q", got)
	}
	if strings.Contains(got, "@") || strings.Contains(got, "jane") {
		t.Fatalf("hash leaks the input: %q", got)
	}
}
