package siteconfig

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConfig_Fields(t *testing.T) {
	// Lock the field shape — these are referenced by JSON-marshalling
	// callers in handlers.go and must stay stable.
	c := &Config{
		CompanyName:       "Example Co",
		LogoURL:           "/media/logo.png",
		CustomDomain:      "example.com",
		PrimaryColor:      "#DC2626",
		ThemeExtra:        json.RawMessage(`{"borderRadius":"4px"}`),
		Tagline:           "tagline",
		Timezone:          "UTC",
		HomepageEntityID:  uuid.New(),
		NotificationEmail: "ops@example.com",
		UpdatedAt:         time.Now(),
	}
	if c.CompanyName == "" {
		t.Fatal("CompanyName field missing")
	}
	if c.HomepageEntityID == uuid.Nil {
		t.Fatal("HomepageEntityID field missing")
	}
}

func TestCacheTTL_Reasonable(t *testing.T) {
	if cacheTTL < 10*time.Second {
		t.Fatalf("cacheTTL %v too short — will hammer DB on hot paths", cacheTTL)
	}
	if cacheTTL > 5*time.Minute {
		t.Fatalf("cacheTTL %v too long — admin UI edits will appear delayed", cacheTTL)
	}
}

func TestErrNotConfigured_String(t *testing.T) {
	if ErrNotConfigured.Error() == "" {
		t.Fatal("ErrNotConfigured must have a message")
	}
}

func TestUpdateInput_ZeroValueLeavesPageCacheUnchanged(t *testing.T) {
	// Patch semantics: nil pointers mean "keep the stored value", so a zero
	// UpdateInput must never touch the page cache columns.
	var in UpdateInput
	if in.PageCacheEnabled != nil || in.PageCacheTTLSeconds != nil ||
		in.PageCacheMaxMB != nil || in.PagePreloadEnabled != nil ||
		in.PagePreloadIntervalSeconds != nil {
		t.Fatal("zero UpdateInput must leave every page cache field nil")
	}
}

func TestConfig_PageCacheFieldsHoldSentinels(t *testing.T) {
	c := Config{PageCacheEnabled: true, PageCacheTTLSeconds: 3600, PageCacheMaxMB: 300,
		PagePreloadEnabled: true, PagePreloadIntervalSeconds: 3600}
	if !c.PageCacheEnabled || c.PageCacheTTLSeconds != 3600 || c.PageCacheMaxMB != 300 ||
		!c.PagePreloadEnabled || c.PagePreloadIntervalSeconds != 3600 {
		t.Fatalf("page cache fields not preserved: %+v", c)
	}
}

func TestUpdateInput_ValidatesPageCache(t *testing.T) {
	ptr := func(i int) *int { return &i }
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   UpdateInput
		ok   bool
	}{
		{"default sentinel", UpdateInput{PageCacheTTLSeconds: ptr(0)}, true},
		{"30 days", UpdateInput{PageCacheTTLSeconds: ptr(MaxPageCacheTTLSeconds)}, true},
		{"too long", UpdateInput{PageCacheTTLSeconds: ptr(MaxPageCacheTTLSeconds + 1)}, false},
		{"too short", UpdateInput{PageCacheTTLSeconds: ptr(29)}, false},
		{"mode missing", UpdateInput{PagePreloadMode: str(PreloadModeMissing)}, true},
		{"mode all", UpdateInput{PagePreloadMode: str(PreloadModeAll)}, true},
		{"mode bogus", UpdateInput{PagePreloadMode: str("some")}, false},
		{"untouched", UpdateInput{}, true},
	}
	for _, c := range cases {
		err := c.in.validatePageCache()
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrInvalidPageCache)) {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestEffectivePageCacheTTL(t *testing.T) {
	cases := []struct {
		name         string
		secs, legacy int
		want         int
	}{
		{"mirrored 30 days", 2592000, 86400, 2592000},
		{"mirrored short", 3600, 3600, 3600},
		{"rolled back then set 1 hour", 2592000, 3600, 3600},
		{"rolled back then set default", 2592000, 0, 0},
		{"new column unset", 0, 600, 600},
		{"both default", 0, 0, 0},
	}
	for _, c := range cases {
		if got := effectivePageCacheTTL(c.secs, c.legacy); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
