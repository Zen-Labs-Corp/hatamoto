package admin

import (
	"context"
	"errors"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// mountLocaleSettings registers the pages of Settings > Locales. Each tab adds
// its routes here.
func (a *Admin) mountLocaleSettings(owner *httpx.Group) {
}

// saveLocales is the one write path of the locale list: validate against the
// running site, store, then clear the rendered pages so a change (a Public
// toggle, say) shows at once. It returns the form error per field ("locales",
// "xdefault") when the input is rejected, and an error when the save itself
// fails. An empty list makes the site single-locale and drops the flags.
func (a *Admin) saveLocales(ctx context.Context, cfg *siteconfig.Config, entries []siteconfig.LocaleEntry, prefixAll bool, xdef string) (map[string]string, error) {
	if len(entries) == 0 {
		entries, xdef, prefixAll = []siteconfig.LocaleEntry{}, "", false
	} else {
		if a.deps.ValidateLocales == nil {
			return map[string]string{"locales": "Locales are not available here"}, nil
		}
		var err error
		if entries, xdef, err = a.deps.ValidateLocales(ctx, cfg.Language, entries, prefixAll, xdef); err != nil {
			field := "locales"
			var lf localeFielder
			if errors.As(err, &lf) && lf.LocaleField() == "x_default_locale" {
				field = "xdefault"
			}
			return map[string]string{field: err.Error()}, nil
		}
	}
	in := siteconfig.UpdateInput{Locales: &entries, PrefixAll: &prefixAll, XDefaultLocale: &xdef}
	if _, err := a.deps.Site.Update(ctx, in); err != nil {
		return nil, err
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	return nil, nil
}
