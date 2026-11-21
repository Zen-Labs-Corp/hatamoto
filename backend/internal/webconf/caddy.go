package webconf

import (
	"fmt"
	"regexp"
	"strings"
)

// caddyAddress is a Caddy site address: plain host[:port] for https (Caddy
// manages the certificate), http:// kept when the site is plain http.
func caddyAddress(origin string) string {
	scheme, hp, _ := splitOrigin(origin)
	if scheme == "http" {
		return "http://" + hp
	}
	return hp
}

func caddy(in Input, rules []rule) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	w("# Static site published by Hatamoto: %s is served from disk.", in.Root)
	if in.Live == "" {
		w("# Island paths, the admin and previews are passed to Hatamoto on %s.", in.Upstream)
	} else {
		w("# Islands and the admin live on %s, which is passed to Hatamoto on %s.", hostOf(in.Live), in.Upstream)
	}
	w("%s {", caddyAddress(in.Site))
	if in.Live == "" {
		caddyProxyHandles(w, in)
	}
	w("\thandle {")
	w("\t\troot * %s", in.Root)
	w("\t\t@hidden path %s", strings.Join(hiddenFiles, " "))
	w("\t\trespond @hidden 404")
	if len(in.Headers) > 0 {
		w("\t\theader {")
		for _, h := range in.Headers {
			w("\t\t\t%s %q", h.Name, h.Value)
		}
		w("\t\t}")
	}
	for _, l := range caddyRedirects(rules) {
		w("\t\t%s", l)
	}
	w("\t\ttry_files {path} {path}/index.html")
	w("\t\tfile_server")
	w("\t}")
	w("\thandle_errors {")
	w("\t\troot * %s", in.Root)
	w("\t\trewrite * /404.html")
	w("\t\tfile_server")
	w("\t}")
	w("}")

	if in.Live != "" {
		w("")
		w("%s {", caddyAddress(in.Live))
		w("\treverse_proxy %s", in.Upstream)
		w("}")
	}
	return b.String()
}

// caddyProxyHandles writes the matchers and handles that pass Hatamoto's paths on.
func caddyProxyHandles(w func(string, ...any), in Input) {
	var paths, regexps, fallback []string
	for _, p := range in.Proxy {
		switch {
		case p.Fallback:
			fallback = append(fallback, p.Path+"*")
		case p.Locale:
			regexps = append(regexps, "^/[^/]+"+regexp.QuoteMeta(p.Path)+"$")
		case p.Prefix:
			paths = append(paths, p.Path+"*")
		default:
			paths = append(paths, p.Path)
		}
	}
	// A theme preview is a query or a cookie on any page: the owner must see the
	// previewed theme, not the exported one.
	w("\t@hatamoto_preview_query query %s=*", previewName)
	w("\thandle @hatamoto_preview_query {")
	w("\t\treverse_proxy %s", in.Upstream)
	w("\t}")
	w("\t@hatamoto_preview_cookie header_regexp Cookie (^|;\\s*)%s=", previewName)
	w("\thandle @hatamoto_preview_cookie {")
	w("\t\treverse_proxy %s", in.Upstream)
	w("\t}")
	if len(paths) > 0 {
		w("\t@hatamoto path %s", strings.Join(paths, " "))
		w("\thandle @hatamoto {")
		w("\t\treverse_proxy %s", in.Upstream)
		w("\t}")
	}
	if len(fallback) > 0 {
		w("\t@hatamoto_fallback path %s", strings.Join(fallback, " "))
		w("\thandle @hatamoto_fallback {")
		w("\t\troot * %s", in.Root)
		w("\t\t@missing not file")
		w("\t\treverse_proxy @missing %s", in.Upstream)
		w("\t\tfile_server")
		w("\t}")
	}
	for i, re := range regexps {
		w("\t@hatamoto_locale%d path_regexp %s", i+1, re)
		w("\thandle @hatamoto_locale%d {", i+1)
		w("\t\treverse_proxy %s", in.Upstream)
		w("\t}")
	}
}
