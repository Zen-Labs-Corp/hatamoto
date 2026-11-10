package packages

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

func devEntry(name string, pub string) Entry {
	return Entry{Kind: KindPlugin, Name: name, Version: "v1.0.0", URL: "p/" + name + ".hmp", SHA256: strings.Repeat("a", 64), SignerKey: pub, Tier: TierDeveloper}
}

func TestDeveloperIndexIsVerifiedAgainstTheListingKeyOnly(t *testing.T) {
	listing, firstParty, dev := newKey(t), newKey(t), newKey(t)
	now := time.Now().UTC()
	mutate := func(idx *Index) { idx.Packages = []Entry{devEntry("acme", dev.Public)} }

	idx, err := ParseIndex(signedIndex(t, listing, mutate), trust(t, listing), now)
	if err != nil || len(idx.Packages) != 1 || idx.Packages[0].SignerKey != dev.Public || idx.Packages[0].Tier != TierDeveloper {
		t.Fatalf("listing-signed index: %+v, %v", idx, err)
	}

	// Another key, even a trusted package key, is not the listing key.
	if _, err := ParseIndex(signedIndex(t, firstParty, mutate), trust(t, listing), now); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("index signed by another key: %v, want ErrUntrustedKey", err)
	}

	expired := signedIndex(t, listing, func(i *Index) {
		mutate(i)
		i.IssuedAt = now.Add(-48 * time.Hour).Format(time.RFC3339)
		i.ExpiresAt = now.Add(-time.Hour).Format(time.RFC3339)
	})
	if _, err := ParseIndex(expired, trust(t, listing), now); !errors.Is(err, ErrIndexExpired) {
		t.Errorf("expired index: %v, want ErrIndexExpired", err)
	}

	unsigned := []byte(`{"index":{"version":1,"issued_at":"` + now.Add(-time.Minute).Format(time.RFC3339) + `","expires_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `","packages":[]},"signer":"","signature":""}`)
	if _, err := ParseIndex(unsigned, trust(t, listing), now); !errors.Is(err, ErrUnsigned) {
		t.Errorf("unsigned index: %v, want ErrUnsigned", err)
	}
}

func TestParseIndexDeveloperEntries(t *testing.T) {
	listing, dev := newKey(t), newKey(t)
	doc := signedIndex(t, listing, func(idx *Index) {
		keyless := devEntry("keyless", "")
		badKey := devEntry("badkey", "not-a-key")
		plain := devEntry("plain", "not-a-key")
		plain.Tier = TierCommunity
		idx.Packages = []Entry{devEntry("good", dev.Public), keyless, badKey, plain}
	})
	idx, err := ParseIndex(doc, trust(t, listing), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range idx.Packages {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "good,plain" {
		t.Fatalf("kept %v, want good,plain (developer tier without a valid key is dropped)", names)
	}
	if idx.Packages[1].SignerKey != "" {
		t.Errorf("invalid signer_key kept: %q", idx.Packages[1].SignerKey)
	}
}

func TestParseIndexSanitisesRevocations(t *testing.T) {
	listing, dev := newKey(t), newKey(t)
	long := strings.Repeat("x", 500)
	doc := signedIndex(t, listing, func(idx *Index) {
		idx.Packages = nil
		idx.Revoked = []Revocation{
			{SignerKey: dev.Public, Reason: "  key\x00 leaked\n "},
			{Name: "acme", Version: "v1.0.0", Reason: long},
			{SignerKey: "garbage", Reason: "dropped"},
			{Name: "Bad Name!", Reason: "dropped"},
			{Reason: "dropped"},
			{SignerKey: "garbage", Name: "acme", Reason: "key cleared, name kept"},
		}
	})
	idx, err := ParseIndex(doc, trust(t, listing), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Revoked) != 3 {
		t.Fatalf("revoked = %+v, want 3 kept", idx.Revoked)
	}
	if got := idx.Revoked[0]; got.SignerKey != dev.Public || got.Reason != "key leaked" {
		t.Errorf("first = %+v", got)
	}
	if got := idx.Revoked[1]; len([]rune(got.Reason)) != maxRevocationReason || got.Name != "acme" || got.Version != "v1.0.0" {
		t.Errorf("second = %d runes, %+v", len([]rune(got.Reason)), got)
	}
	if got := idx.Revoked[2]; got.SignerKey != "" || got.Name != "acme" {
		t.Errorf("third = %+v", got)
	}
}

func TestFilterDeveloperShadowRule(t *testing.T) {
	dev := newKey(t)
	idxDev := &Index{Version: 1, Revoked: []Revocation{{Name: "x"}}, Packages: []Entry{
		devEntry("mine", dev.Public),
		devEntry("inmain", dev.Public),
		devEntry(FirstPartyPlugins[0], dev.Public),
		devEntry("nokey", ""),
		{Kind: KindTheme, Name: "inmain", Version: "v1.0.0", URL: "p/t.hmp", SHA256: strings.Repeat("a", 64), SignerKey: dev.Public, Tier: TierCommunity},
	}}
	main := &Index{Version: 1, Packages: []Entry{{Kind: KindPlugin, Name: "inmain"}}}

	got := FilterDeveloper(idxDev, main)
	var names []string
	for _, e := range got.Packages {
		names = append(names, string(e.Kind)+":"+e.Name)
		if e.Tier != TierDeveloper {
			t.Errorf("%s tier = %q, want developer", e.Name, e.Tier)
		}
	}
	// The same name as another kind is not shadowed; a first-party name always is.
	if strings.Join(names, ",") != "plugin:mine,theme:inmain" {
		t.Errorf("kept %v", names)
	}
	if len(got.Revoked) != 1 {
		t.Errorf("revocations must survive the filter: %+v", got.Revoked)
	}
	if got := FilterDeveloper(idxDev, nil); len(got.Packages) != 3 {
		t.Errorf("nil main: kept %d, want 3 (only first-party and keyless dropped)", len(got.Packages))
	}
}

// A server built before signer_key and revoked existed reads a developer index unchanged.
func TestDeveloperIndexReadableByOldEntry(t *testing.T) {
	type oldEntry struct {
		Kind    Kind   `json:"kind"`
		Name    string `json:"name"`
		Version string `json:"version"`
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
	}
	type oldIndex struct {
		Version  int        `json:"version"`
		Packages []oldEntry `json:"packages"`
	}
	listing, dev := newKey(t), newKey(t)
	raw, err := trust(t, listing).VerifyIndex(signedIndex(t, listing, func(idx *Index) {
		idx.Packages = []Entry{devEntry("acme", dev.Public)}
		idx.Revoked = []Revocation{{Name: "acme", Reason: "x"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	var old oldIndex
	if err := jsonx.Unmarshal(raw, &old); err != nil || len(old.Packages) != 1 || old.Packages[0].Name != "acme" {
		t.Fatalf("old parser: %+v, %v", old, err)
	}
}
