package spam

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fakeTokenStore struct {
	used  map[string]time.Time
	calls int
	err   error
}

func newFakeTokenStore() *fakeTokenStore { return &fakeTokenStore{used: map[string]time.Time{}} }

func (f *fakeTokenStore) ConsumeFormToken(_ context.Context, id string, exp time.Time) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	if _, ok := f.used[id]; ok {
		return false, nil
	}
	f.used[id] = exp
	return true, nil
}

func TestCommentToken(t *testing.T) {
	t.Parallel()
	key := CommentTokenKey("secret")
	now := time.Unix(1_800_000_000, 0)
	subj := CommentSubject("e1")

	cases := []struct {
		name      string
		subject   string
		ip        string
		mutate    func(string) string
		at        time.Time
		storeErr  error
		want      TokenResult
		wantCalls int
	}{
		{name: "valid", subject: subj, ip: "ip1", at: now.Add(time.Minute), want: TokenOK, wantCalls: 1},
		{name: "expired", subject: subj, ip: "ip1", at: now.Add(CommentTokenMaxAge + time.Second), want: TokenExpired},
		{name: "other subject", subject: CommentSubject("e2"), ip: "ip1", at: now, want: TokenInvalid},
		{name: "other ip", subject: subj, ip: "ip2", at: now, want: TokenInvalid},
		{name: "tampered", subject: subj, ip: "ip1", at: now, want: TokenInvalid,
			mutate: func(s string) string { return s[:len(s)-1] + flip(s[len(s)-1]) }},
		{name: "oversized", subject: subj, ip: "ip1", at: now, want: TokenInvalid,
			mutate: func(s string) string { return s + strings.Repeat("a", 300) }},
		{name: "malformed", subject: subj, ip: "ip1", at: now, want: TokenInvalid,
			mutate: func(string) string { return "v1.1.2" }},
		{name: "future", subject: subj, ip: "ip1", at: now.Add(-time.Hour), want: TokenInvalid},
		{name: "store error", subject: subj, ip: "ip1", at: now, storeErr: errors.New("db down"), want: TokenInvalid, wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newFakeTokenStore()
			store.err = tc.storeErr
			tok := IssueCommentToken(key, subj, "ip1", now)
			if tc.mutate != nil {
				tok = tc.mutate(tok)
			}
			issued, res := CheckCommentToken(context.Background(), key, store, tc.subject, tc.ip, tok, tc.at)
			if res != tc.want {
				t.Fatalf("result = %d, want %d", res, tc.want)
			}
			if store.calls != tc.wantCalls {
				t.Fatalf("store calls = %d, want %d", store.calls, tc.wantCalls)
			}
			if res == TokenOK && !issued.Equal(now) {
				t.Fatalf("issued = %v, want %v", issued, now)
			}
		})
	}
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

func TestCommentTokenReplayAndStoreArgs(t *testing.T) {
	t.Parallel()
	key := CommentTokenKey("secret")
	now := time.Unix(1_800_000_000, 0)
	store := newFakeTokenStore()
	tok := IssueCommentToken(key, "s", "ip", now)

	if _, res := CheckCommentToken(context.Background(), key, store, "s", "ip", tok, now); res != TokenOK {
		t.Fatalf("first = %d", res)
	}
	if _, res := CheckCommentToken(context.Background(), key, store, "s", "ip", tok, now); res != TokenReplayed {
		t.Fatalf("second = %d, want replayed", res)
	}
	if len(store.used) != 1 {
		t.Fatalf("store has %d ids", len(store.used))
	}
	for id, exp := range store.used {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(id) {
			t.Fatalf("id %q is not 64 hex chars", id)
		}
		if !exp.Equal(now.Add(CommentTokenMaxAge)) {
			t.Fatalf("expiresAt = %v", exp)
		}
	}
}

func TestCommentTokenUnique(t *testing.T) {
	t.Parallel()
	key := CommentTokenKey("secret")
	now := time.Unix(1_800_000_000, 0)
	if a, b := IssueCommentToken(key, "s", "ip", now), IssueCommentToken(key, "s", "ip", now); a == b {
		t.Fatal("two tokens in the same second are equal")
	}
}
