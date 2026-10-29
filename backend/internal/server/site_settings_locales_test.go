package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
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

func TestSiteSettings_OneLocaleResponseUnchanged(t *testing.T) {
	store := &localeStore{}
	rec := doSite(localeRouter(store, nil), http.MethodGet, "/settings/site/", "")
	testutil.Equal(t, http.StatusOK, rec.Code)
	if !strings.Contains(rec.Body.String(), `"locales":[],"prefix_all":false,"x_default_locale":""`) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestReservedSegmentReason(t *testing.T) {
	for _, seg := range []string{"api", "media", "admin", "search", "category", "tag", "sitemap-x", "auth", "authors", "feed", "p"} {
		if reservedSegmentReason(seg) == "" {
			t.Errorf("%q is not reserved", seg)
		}
	}
	for _, seg := range []string{"fr", "nl", "pt-br", "en"} {
		if reservedSegmentReason(seg) != "" {
			t.Errorf("%q is reserved", seg)
		}
	}
}
