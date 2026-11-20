package comment

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestUnsubscribeTokenRoundTrip(t *testing.T) {
	key := UnsubscribeKey("secret")
	for _, kind := range []UnsubscribeKind{UnsubscribeReplies, UnsubscribeModerator} {
		id := uuid.New()
		tok := UnsubscribeToken(key, kind, id)
		gotKind, gotID, ok := VerifyUnsubscribe(key, tok)
		if !ok || gotKind != kind || gotID != id {
			t.Errorf("kind %c: got %c %s %v, want %s", kind, gotKind, gotID, ok, id)
		}
	}
}

func TestUnsubscribeTokenAlphabet(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	for i := 0; i < 200; i++ {
		if tok := UnsubscribeToken(UnsubscribeKey("s"), UnsubscribeReplies, uuid.New()); !re.MatchString(tok) {
			t.Fatalf("token %q has characters outside [A-Za-z0-9_-]", tok)
		}
	}
}

func TestUnsubscribeTokenRejects(t *testing.T) {
	key := UnsubscribeKey("secret")
	id := uuid.New()
	good := UnsubscribeToken(key, UnsubscribeReplies, id)
	raw, err := base64.RawURLEncoding.DecodeString(good)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	flip := func(i int) string {
		b := append([]byte(nil), raw...)
		b[i] ^= 1
		return enc(b)
	}
	// A correctly signed token of an unknown kind must fail too.
	unknown := append([]byte{'x'}, id[:]...)
	unknown = append(unknown, unsubscribeMAC(key, unknown)...)

	cases := map[string]struct {
		key []byte
		tok string
	}{
		"flipped kind":    {key, flip(0)},
		"flipped id":      {key, flip(5)},
		"flipped mac":     {key, flip(len(raw) - 1)},
		"wrong key":       {UnsubscribeKey("other"), good},
		"unknown kind":    {key, enc(unknown)},
		"truncated":       {key, good[:len(good)-2]},
		"extended":        {key, enc(append(append([]byte(nil), raw...), 0))},
		"empty":           {key, ""},
		"padded":          {key, good + "=="},
		"standard base64": {key, strings.NewReplacer("-", "+", "_", "/").Replace(good) + "!"},
		"garbage":         {key, strings.Repeat("A", 1024)},
	}
	for name, c := range cases {
		if _, _, ok := VerifyUnsubscribe(c.key, c.tok); ok {
			t.Errorf("%s: token accepted", name)
		}
	}
}
