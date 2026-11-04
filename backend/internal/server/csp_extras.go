package server

import (
	"context"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/spam"
)

// cspExtras are origins a page needs beyond 'self', per directive. Captcha
// providers and enabled tag vendors both feed it; cspWithExtras is the one
// place that writes them into a policy.
type cspExtras struct{ Script, Connect, Style, Font, Img, Frame []string }

// empty reports whether no directive gains an origin.
func (e cspExtras) empty() bool {
	return len(e.Script)+len(e.Connect)+len(e.Style)+len(e.Font)+len(e.Img)+len(e.Frame) == 0
}

// merge returns e plus o, de-duplicated, in first-seen order.
func (e cspExtras) merge(o cspExtras) cspExtras {
	return cspExtras{
		Script:  dedupe(e.Script, o.Script),
		Connect: dedupe(e.Connect, o.Connect),
		Style:   dedupe(e.Style, o.Style),
		Font:    dedupe(e.Font, o.Font),
		Img:     dedupe(e.Img, o.Img),
		Frame:   dedupe(e.Frame, o.Frame),
	}
}

func dedupe(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, x := range l {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out
}

// captchaOrigins are the origins a provider's script and verification traffic
// need beyond 'self'. Its iframes are already covered by frame-src https:.
var captchaOrigins = map[string][]string{
	spam.ProviderTurnstile: {"https://challenges.cloudflare.com"},
	spam.ProviderRecaptcha: {"https://www.google.com", "https://www.gstatic.com"},
	spam.ProviderHCaptcha:  {"https://hcaptcha.com", "https://*.hcaptcha.com"},
}

// captchaExtras are the origins for one provider; none or an unknown one
// yields no extras.
func captchaExtras(provider string) cspExtras {
	o := captchaOrigins[provider]
	return cspExtras{Script: o, Connect: o}
}

// pluginExtras are the hosts the enabled plugins' csp blocks add.
func pluginExtras(set pluginhost.CSPSet) cspExtras {
	return cspExtras{Script: set.Script, Connect: set.Connect, Img: set.Img, Frame: set.Frame}
}

// cspWithExtras appends each list to its directive of base. It works on
// renderCSP and renderCSPStrict alike; a directive missing from base is left
// out, and empty extras return base unchanged.
func cspWithExtras(base string, e cspExtras) string {
	if e.empty() {
		return base
	}
	for _, d := range []struct {
		name    string
		origins []string
	}{
		{"script-src", e.Script}, {"connect-src", e.Connect},
		{"style-src", e.Style}, {"font-src", e.Font},
		{"img-src", e.Img}, {"frame-src", e.Frame},
	} {
		base = addToDirective(base, d.name, d.origins)
	}
	return base
}

func addToDirective(csp, name string, origins []string) string {
	if len(origins) == 0 {
		return csp
	}
	parts := strings.Split(csp, "; ")
	for i, p := range parts {
		if strings.HasPrefix(p, name+" ") {
			// A directive that already allows "https:" covers every https host.
			if slices.Contains(strings.Fields(p), "https:") {
				return csp
			}
			parts[i] = p + " " + strings.Join(origins, " ")
			break
		}
	}
	return strings.Join(parts, "; ")
}

// cspExtras gathers the origins every public and preview response adds to
// its policy. It reads cached settings only.
func (s *Server) cspExtras(ctx context.Context) cspExtras {
	return captchaExtras(s.captchaProvider(ctx)).merge(s.tagsHosts(ctx))
}

// extrasOf reads the extras through fn; nil fn means none.
func extrasOf(ctx context.Context, fn func(context.Context) cspExtras) cspExtras {
	if fn == nil {
		return cspExtras{}
	}
	return fn(ctx)
}
