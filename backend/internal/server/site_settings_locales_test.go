package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/testutil"
)

// localeStore is fakeSiteStore that also applies the locale fields.
type localeStore struct{ fakeSiteStore }

func (f *localeStore) Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if in.Locales != nil {
		f.cfg.Locales = *in.Locales
	}
	if in.PrefixAll != nil {
		f.cfg.PrefixAll = *in.PrefixAll
	}
	if in.XDefaultLocale != nil {
		f.cfg.XDefaultLocale = *in.XDefaultLocale
	}
	if in.VisitorEntry != nil {
		f.cfg.VisitorEntry = *in.VisitorEntry
	}
	if in.SitemapNaming != nil {
		f.cfg.SitemapNaming = *in.SitemapNaming
	}
	return f.fakeSiteStore.Update(ctx, in)
}

func localeRouter(store *localeStore, check func(context.Context, []string) error) http.Handler {
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	{
		r := r.Group("", injectRole("owner"))
		mountSiteSettings(r, &siteSettingsHandlers{
			store:        store,
			logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			invalidate:   func(context.Context) {},
			checkLocales: check,
		})
	}
	return mux
}

const threeLocales = `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"},{"tag":"nl","segment":"nl"}]`

// Every rejection of the settings save, and the happy path that stores the
// list normalized.
func TestSiteSettings_LocaleValidation(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		check func(context.Context, []string) error
		want  string // substring of the 400 message; "" = accepted
	}{
		{"valid list", `{"locales":` + threeLocales + `,"x_default_locale":"en"}`, nil, ""},
		{"tags are normalized", `{"locales":[{"tag":"en"},{"tag":"FR_ca","segment":"fr-ca"}]}`, nil, ""},
		{"invalid tag", `{"locales":[{"tag":"en"},{"tag":"not a tag","segment":"x"}]}`, nil, "invalid locales"},
		{"duplicate tag", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"a"},{"tag":"fr","segment":"b"}]}`, nil, "listed twice"},
		{"duplicate segment", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"a"},{"tag":"nl","segment":"a"}]}`, nil, "share the segment"},
		{"bad segment", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"Fr/x"}]}`, nil, "segment"},
		{"missing segment", `{"locales":[{"tag":"en"},{"tag":"fr"}]}`, nil, "needs a URL segment"},
		{"default not in list", `{"locales":[{"tag":"fr","segment":"fr"},{"tag":"nl","segment":"nl"}]}`, nil, "default locale"},
		{"x-default not in list", `{"locales":` + threeLocales + `,"x_default_locale":"es"}`, nil, "invalid x_default_locale"},
		{"prefix-all needs a default segment", `{"locales":` + threeLocales + `,"prefix_all":true}`, nil, "needs a URL segment"},
		{"reserved segment", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"api"}]}`, func(_ context.Context, segs []string) error {
			if reason := reservedSegmentReason(segs[0]); reason != "" {
				return errors.New("segment " + segs[0] + " is " + reason)
			}
			return nil
		}, "is a route of its own"},
		{"shadowed by the site", `{"locales":` + threeLocales + `}`, func(_ context.Context, segs []string) error {
			return errors.New("segment " + segs[0] + " is claimed by the redirect rule /fr/**")
		}, "redirect rule"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &localeStore{}
			h := localeRouter(store, tt.check)
			rec := doSite(h, http.MethodPatch, "/settings/site/", tt.body)
			if tt.want == "" {
				testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				return
			}
			testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("body %q lacks %q", rec.Body, tt.want)
			}
			if len(store.cfg.Locales) != 0 {
				t.Errorf("a rejected change stored %v", store.cfg.Locales)
			}
		})
	}
}

func TestSiteSettings_LocalesRoundTripAndLanguageGuard(t *testing.T) {
	store := &localeStore{}
	store.cfg.Language = "en"
	h := localeRouter(store, nil)

	rec := doSite(h, http.MethodPatch, "/settings/site/", `{"locales":[{"tag":"en"},{"tag":"FR_ca","segment":"fr-ca"}],"prefix_all":false,"x_default_locale":""}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	if !strings.Contains(rec.Body.String(), `"locales":[{"tag":"en","segment":""},{"tag":"fr-CA","segment":"fr-ca"}]`) {
		t.Fatalf("body: %s", rec.Body)
	}
	// Changing the language alone must keep the default locale in the list.
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"language":"nl"}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	testutil.Equal(t, "en", store.cfg.Language)
	// Clearing the list makes the site single-locale and drops the flags.
	store.cfg.PrefixAll, store.cfg.XDefaultLocale = true, "en"
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"locales":[]}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	if len(store.cfg.Locales) != 0 || store.cfg.PrefixAll || store.cfg.XDefaultLocale != "" {
		t.Errorf("cleared config = %+v", store.cfg)
	}
	// A patch that does not mention the locale fields leaves them alone.
	store.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"name":"Beta"}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, 2, len(store.cfg.Locales))
}

func TestSiteSettings_SitemapNaming(t *testing.T) {
	store := &localeStore{}
	h := localeRouter(store, nil)
	rec := doSite(h, http.MethodGet, "/settings/site/", "")
	if !strings.Contains(rec.Body.String(), `"sitemap_naming":"group"`) {
		t.Errorf("default not group: %s", rec.Body)
	}
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"sitemap_naming":"legacy"}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "legacy", store.cfg.SitemapNaming)
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"sitemap_naming":"x"}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	testutil.Equal(t, "legacy", store.cfg.SitemapNaming)
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"name":"Beta"}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "legacy", store.cfg.SitemapNaming)
}

func TestReservedSegmentReason_SitemapNames(t *testing.T) {
	for _, seg := range []string{"index", "misc"} {
		if reservedSegmentReason(seg) == "" {
			t.Errorf("segment %q is not reserved", seg)
		}
	}
}

func TestSiteSettings_OneLocaleResponseUnchanged(t *testing.T) {
	store := &localeStore{}
	rec := doSite(localeRouter(store, nil), http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	if !strings.Contains(rec.Body.String(), `"locales":[],"prefix_all":false,"x_default_locale":""`) {
		t.Errorf("body: %s", rec.Body)
	}
}

// failingLocaleStore fails the read or the write of the site settings.
type failingLocaleStore struct {
	localeStore
	getErr, updateErr error
}

func (f *failingLocaleStore) Get(ctx context.Context) (*siteconfig.Config, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.localeStore.Get(ctx)
}

func (f *failingLocaleStore) Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.localeStore.Update(ctx, in)
}

// A failing segment check is told apart from a rejected segment: both are 400,
// but the infrastructure failure gets a fixed message that never echoes the
// driver text. A failing store (read or write) stays a 500 with no leak.
func TestSiteSettings_LocaleCheckErrors(t *testing.T) {
	const raw = "dial tcp 10.0.0.1:5432: connection refused"
	body := `{"locales":` + threeLocales + `}`
	infra := func(context.Context, []string) error {
		return &localeCheckInfraError{segment: "fr", err: errors.New(raw)}
	}
	wrapped := func(context.Context, []string) error {
		return fmt.Errorf("check: %w", &localeCheckInfraError{err: errors.New(raw)})
	}
	rule := func(context.Context, []string) error { return errors.New(`segment "fr" is a route of its own`) }
	tests := []struct {
		name  string
		store *failingLocaleStore
		check func(context.Context, []string) error
		code  int
		want  string
	}{
		{"infra error with segment", &failingLocaleStore{}, infra, http.StatusBadRequest, `could not verify segment \"fr\" right now, try again`},
		{"infra error wrapped", &failingLocaleStore{}, wrapped, http.StatusBadRequest, "could not verify the locale segments right now, try again"},
		{"rule violation", &failingLocaleStore{}, rule, http.StatusBadRequest, "is a route of its own"},
		{"stored settings unreadable", &failingLocaleStore{getErr: errors.New(raw)}, nil, http.StatusInternalServerError, "failed to update site settings"},
		{"store write fails", &failingLocaleStore{updateErr: errors.New(raw)}, nil, http.StatusInternalServerError, "failed to update site settings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			g := httpx.NewGroup(mux, "").Group("", injectRole("owner"))
			mountSiteSettings(g, &siteSettingsHandlers{
				store:        tt.store,
				logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
				invalidate:   func(context.Context) {},
				checkLocales: tt.check,
			})
			rec := doSite(mux, http.MethodPatch, "/settings/site/", body)
			testutil.Equal(t, tt.code, rec.Code, rec.Body.String())
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("body %q lacks %q", rec.Body, tt.want)
			}
			if strings.Contains(rec.Body.String(), raw) {
				t.Errorf("body leaks the driver error: %s", rec.Body)
			}
		})
	}
}

func TestReservedSegmentReason(t *testing.T) {
	for _, seg := range []string{"api", "media", "admin", "search", "category", "tag", "sitemap-x", "auth", "authors", "feed", "p", "plugin-ui", "forms", "locale"} {
		if reservedSegmentReason(seg) == "" {
			t.Errorf("%q is not reserved", seg)
		}
	}
	for _, seg := range []string{"fr", "nl", "pt-br", "en", "zh-hans-cn"} {
		if reservedSegmentReason(seg) != "" {
			t.Errorf("%q is reserved", seg)
		}
	}
}

func TestValidateLocaleSettingsKeepsHiddenAndName(t *testing.T) {
	in := localeSettingsRequest{Default: "en", Entries: []siteconfig.LocaleEntry{
		{Tag: "en"},
		{Tag: "FR_ca", Segment: "fr-ca", Name: "Francais", Hidden: true},
	}}
	got, _, err := validateLocaleSettings(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []siteconfig.LocaleEntry{
		{Tag: "en"},
		{Tag: "fr-CA", Segment: "fr-ca", Name: "Francais", Hidden: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
}

func TestValidateLocaleSettingsRejectsHiddenDefault(t *testing.T) {
	in := localeSettingsRequest{Default: "en", Entries: []siteconfig.LocaleEntry{
		{Tag: "en", Hidden: true}, {Tag: "fr", Segment: "fr"},
	}}
	_, _, err := validateLocaleSettings(context.Background(), in, nil)
	var lerr *localeSettingsError
	if !errors.As(err, &lerr) || lerr.Field != "locales" {
		t.Fatalf("want a locales error, got %v", err)
	}
	in = localeSettingsRequest{Default: "en", XDefault: "fr", Entries: []siteconfig.LocaleEntry{
		{Tag: "en"}, {Tag: "fr", Segment: "fr", Hidden: true},
	}}
	_, _, err = validateLocaleSettings(context.Background(), in, nil)
	if !errors.As(err, &lerr) || lerr.Field != "x_default_locale" {
		t.Fatalf("want an x_default_locale error, got %v", err)
	}
}

// A stored index or misc segment cannot switch to legacy naming, where its
// locale file would share a name with the sitemap index or misc file.
func TestSiteSettings_LegacyRefusedForFileNameSegment(t *testing.T) {
	store := &localeStore{}
	store.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "misc"}}
	h := localeRouter(store, nil)
	rec := doSite(h, http.MethodPatch, "/settings/site/", `{"sitemap_naming":"legacy"}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "sitemap-misc.xml") || store.cfg.SitemapNaming == "legacy" {
		t.Errorf("body %q naming %q", rec.Body, store.cfg.SitemapNaming)
	}
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"sitemap_naming":"group"}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestValidateLocaleSettingsKeepsCountries(t *testing.T) {
	in := localeSettingsRequest{Default: "en", Entries: []siteconfig.LocaleEntry{
		{Tag: "en"},
		{Tag: "fr", Segment: "fr", Countries: []string{"fr", " be "}},
		{Tag: "es", Segment: "es", Countries: []string{"ES"}},
	}}
	got, _, err := validateLocaleSettings(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Countries != nil || !reflect.DeepEqual(got[1].Countries, []string{"FR", "BE"}) || !reflect.DeepEqual(got[2].Countries, []string{"ES"}) {
		t.Fatalf("countries = %v %v %v", got[0].Countries, got[1].Countries, got[2].Countries)
	}
}

func TestValidateLocaleSettingsCountryErrorsNameTheField(t *testing.T) {
	for name, entries := range map[string][]siteconfig.LocaleEntry{
		"one code in two locales": {{Tag: "en"}, {Tag: "fr", Segment: "fr", Countries: []string{"FR"}}, {Tag: "es", Segment: "es", Countries: []string{"fr"}}},
		"not a two-letter code":   {{Tag: "en"}, {Tag: "fr", Segment: "fr", Countries: []string{"FRA"}}},
	} {
		_, _, err := validateLocaleSettings(context.Background(), localeSettingsRequest{Default: "en", Entries: entries}, nil)
		var le *localeSettingsError
		if !errors.As(err, &le) || le.Field != "countries" {
			t.Errorf("%s: err = %v, want a countries error", name, err)
		}
	}
}

func TestSiteSettings_CountriesAndVisitorEntryRoundTrip(t *testing.T) {
	store := &localeStore{}
	store.cfg.Language = "en"
	h := localeRouter(store, nil)

	rec := doSite(h, http.MethodPatch, "/settings/site/", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"fr","countries":["fr"]}],`+
		`"visitor_entry":{"enabled":true,"paths":["/contact/"],"legacy_cookie":"old_loc"}}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, want := range []string{`"countries":["FR"]`, `"visitor_entry":{"enabled":true,"paths":["/contact"],"legacy_cookie":"old_loc"}`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, rec.Body)
		}
	}
	// A patch that names neither leaves both alone.
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"name":"Beta"}`)
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	if !store.cfg.VisitorEntry.Enabled || !reflect.DeepEqual(store.cfg.Locales[1].Countries, []string{"FR"}) {
		t.Errorf("stored = %+v %+v", store.cfg.Locales, store.cfg.VisitorEntry)
	}
	// A rejected entry path stores nothing and names the field.
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"visitor_entry":{"enabled":true,"paths":["/fr/x"]}}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "invalid visitor_entry") || !reflect.DeepEqual(store.cfg.VisitorEntry.Paths, []string{"/contact"}) {
		t.Errorf("body %s, stored %+v", rec.Body, store.cfg.VisitorEntry)
	}
	// Duplicate countries across locales are a 400 on the countries field.
	rec = doSite(h, http.MethodPatch, "/settings/site/", `{"locales":[{"tag":"en"},{"tag":"fr","segment":"fr","countries":["FR"]},{"tag":"es","segment":"es","countries":["FR"]}]}`)
	testutil.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "invalid countries") {
		t.Errorf("body %s", rec.Body)
	}
}

func TestSiteSettings_VisitorEntryAbsentUntilSet(t *testing.T) {
	store := &localeStore{}
	rec := doSite(localeRouter(store, nil), http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	if strings.Contains(rec.Body.String(), "visitor_entry") {
		t.Errorf("an unused setting appears in the response: %s", rec.Body)
	}
}

func TestValidateVisitorEntry(t *testing.T) {
	segs := []string{"fr", "es"}
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("/p%d", i)
	}
	bad := map[string]siteconfig.VisitorEntry{
		"home":             {Paths: []string{"/"}},
		"no leading slash": {Paths: []string{"contact"}},
		"query":            {Paths: []string{"/a?b=1"}},
		"double slash":     {Paths: []string{"/a//b"}},
		"encoded slash":    {Paths: []string{"/a%2Fb"}},
		"dot segment":      {Paths: []string{"/a/../b"}},
		"locale segment":   {Paths: []string{"/fr"}},
		"under a locale":   {Paths: []string{"/es/contact"}},
		"switcher":         {Paths: []string{"/locale"}},
		"reserved route":   {Paths: []string{"/admin/x"}},
		"sitemap file":     {Paths: []string{"/sitemap.xml"}},
		"plugin refusal":   {Paths: []string{"/robots.txt"}},
		"api prefix":       {Paths: []string{"/api/v2/x"}},
		"duplicate":        {Paths: []string{"/a", "/a/"}},
		"51 paths":         {Paths: many},
		"core cookie name": {LegacyCookie: "hm_locale"},
		"cookie bad chars": {LegacyCookie: "a b"},
		"cookie too long":  {LegacyCookie: strings.Repeat("a", 65)},
	}
	for name, v := range bad {
		_, err := validateVisitorEntry(segs, v, "/api/v2")
		var le *localeSettingsError
		if !errors.As(err, &le) || le.Field != "visitor_entry" {
			t.Errorf("%s: err = %v, want a visitor_entry error", name, err)
		}
	}
	got, err := validateVisitorEntry(segs, siteconfig.VisitorEntry{Enabled: true, Paths: append([]string{"/contact/"}, many[:49]...), LegacyCookie: "old_loc"}, "/api/v2")
	if err != nil || got.Paths[0] != "/contact" || len(got.Paths) != 50 || !got.Enabled || got.LegacyCookie != "old_loc" {
		t.Errorf("valid entry: %+v, %v", got, err)
	}
}

// A locale list is refused when one of its segments equals, or starts, a stored
// visitor entry path, and accepted when none does.
func TestEntryPathUnderSegment(t *testing.T) {
	paths := []string{"/contact", "/es/landing", "/pricing/plans"}
	for _, seg := range []string{"contact", "es", "pricing"} {
		err := entryPathUnderSegment(paths, []string{"fr", seg})
		if err == nil || !strings.Contains(err.Error(), "entry path") || !strings.Contains(err.Error(), `"`+seg+`"`) {
			t.Errorf("segment %q: err = %v, want an entry path refusal", seg, err)
		}
	}
	if err := entryPathUnderSegment(paths, []string{"fr", "en"}); err != nil {
		t.Errorf("unrelated segments refused: %v", err)
	}
	if err := entryPathUnderSegment(nil, []string{"fr"}); err != nil {
		t.Errorf("no entry paths refused: %v", err)
	}
}
