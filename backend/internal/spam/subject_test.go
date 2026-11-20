package spam

import (
	"testing"
	"time"
)

func TestTokenBytesUnchangedForFormSlugs(t *testing.T) {
	t.Parallel()
	got := IssueToken([]byte("golden-key"), "contact", time.Unix(1700000000, 0))
	const want = "v1.1700000000.msLys9CRzwfVtDrbOeelsotWU1-MELlmhfszjY-TFDQ" // frozen: live form pages carry these tokens
	if got != want {
		t.Fatalf("form token bytes changed: %s", got)
	}
}

func TestCommentSubjectTokens(t *testing.T) {
	t.Parallel()
	key := []byte("golden-key")
	now := time.Unix(1700000100, 0)
	tok := IssueToken(key, CommentSubject("e1"), time.Unix(1700000000, 0))

	if _, ok := CheckToken(key, CommentSubject("e1"), tok, now); !ok {
		t.Error("comment token does not round trip")
	}
	if _, ok := CheckToken(key, "e1", tok, now); ok {
		t.Error("comment token verified for the bare entity id")
	}
	if _, ok := CheckToken(key, CommentSubject("e2"), tok, now); ok {
		t.Error("comment token verified for another entity")
	}
	formTok := IssueToken(key, "contact", time.Unix(1700000000, 0))
	if _, ok := CheckToken(key, CommentSubject("contact"), formTok, now); ok {
		t.Error("form token verified as a comment subject")
	}
}
