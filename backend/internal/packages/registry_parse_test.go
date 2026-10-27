package packages

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func signedIndex(t testing.TB, key Key, mutate func(*Index)) []byte {
	t.Helper()
	now := time.Now().UTC()
	idx := Index{
		Version:   1,
		IssuedAt:  now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Packages: []Entry{
			{Kind: KindPlugin, Name: "alpha", Version: "v1.0.0", URL: "p/alpha.hmp", SHA256: strings.Repeat("a", 64), Author: "acme", Source: "https://example.org/alpha", Commit: strings.Repeat("b", 40), Tier: TierCommunity},
			{Kind: KindPlugin, Name: "beta", Version: "v1.0.0", URL: "p/beta.hmp", SHA256: strings.Repeat("c", 64), Tier: "gold"},
			{Kind: KindPlugin, Name: "bad", Version: "v1.0.0", URL: "p/bad.hmp", SHA256: "short"},
		},
	}
	if mutate != nil {
		mutate(&idx)
	}
	body, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := SignIndex(body, key)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseIndex(t *testing.T) {
	key, other := newKey(t), newKey(t)
	now := time.Now()
	cases := []struct {
		name string
		doc  func() []byte
		want error
		text string
	}{
		{"valid", func() []byte { return signedIndex(t, key, nil) }, nil, ""},
		{"unsigned", func() []byte {
			return []byte(`{"index":{"version":1},"signer":"","algorithm":"","signature":""}`)
		}, ErrUnsigned, ""},
		{"tampered", func() []byte {
			return []byte(strings.Replace(string(signedIndex(t, key, nil)), "alpha", "omega", 1))
		}, ErrBadSignature, ""},
		{"expired", func() []byte {
			return signedIndex(t, key, func(i *Index) {
				i.IssuedAt = now.Add(-48 * time.Hour).Format(time.RFC3339)
				i.ExpiresAt = now.Add(-24 * time.Hour).Format(time.RFC3339)
			})
		}, ErrIndexExpired, ""},
		{"untrusted signer", func() []byte { return signedIndex(t, other, nil) }, ErrUntrustedKey, ""},
		{"bad version", func() []byte { return signedIndex(t, key, func(i *Index) { i.Version = 2 }) }, nil, "unsupported version 2"},
	}
	for _, tc := range cases {
		idx, err := ParseIndex(tc.doc(), trust(t, key), now)
		switch {
		case tc.want != nil:
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
			}
		case tc.text != "":
			if err == nil || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.text)
			}
		case err != nil:
			t.Errorf("%s: %v", tc.name, err)
		default:
			if len(idx.Packages) != 2 {
				t.Fatalf("malformed entry not dropped: %+v", idx.Packages)
			}
			a, b := idx.Packages[0], idx.Packages[1]
			if a.Author != "acme" || a.Tier != TierCommunity || a.Commit == "" || a.Source == "" {
				t.Errorf("meta lost: %+v", a)
			}
			if b.Tier != "" {
				t.Errorf("unknown tier kept: %q", b.Tier)
			}
		}
	}
}

// A server built before the meta fields existed reads a new index unchanged.
func TestNewIndexReadableByOldEntry(t *testing.T) {
	type oldEntry struct {
		Kind        Kind     `json:"kind"`
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Description string   `json:"description,omitempty"`
		URL         string   `json:"url"`
		SHA256      string   `json:"sha256"`
		Size        int64    `json:"size,omitempty"`
		Permissions []string `json:"permissions,omitempty"`
		Homepage    string   `json:"homepage,omitempty"`
	}
	type oldIndex struct {
		Version  int        `json:"version"`
		Packages []oldEntry `json:"packages"`
	}
	key := newKey(t)
	raw, err := trust(t, key).VerifyIndex(signedIndex(t, key, nil))
	if err != nil {
		t.Fatal(err)
	}
	var old oldIndex
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	if old.Version != 1 || len(old.Packages) != 3 || old.Packages[0].Name != "alpha" {
		t.Fatalf("old parser result: %+v", old)
	}
}

func TestParseIndexSanitisesScreenshot(t *testing.T) {
	key := newKey(t)
	shots := map[string]string{
		"good":  "screenshots/sunrise.png",
		"up":    "../x.png",
		"abs":   "/screenshots/x.png",
		"url":   "https://example.org/x.png",
		"svg":   "screenshots/sunrise.svg",
		"empty": "",
	}
	doc := signedIndex(t, key, func(idx *Index) {
		idx.Packages = nil
		for name, shot := range shots {
			idx.Packages = append(idx.Packages, Entry{Kind: KindTheme, Name: "t-" + name, Version: "v1.0.0", URL: "p/x.hmp", SHA256: strings.Repeat("a", 64), Screenshot: shot})
		}
	})
	idx, err := ParseIndex(doc, trust(t, key), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx.Packages {
		want := ""
		if e.Name == "t-good" {
			want = shots["good"]
		}
		if e.Screenshot != want {
			t.Errorf("%s: screenshot %q, want %q", e.Name, e.Screenshot, want)
		}
	}
}
