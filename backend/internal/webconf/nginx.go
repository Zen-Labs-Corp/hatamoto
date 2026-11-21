package webconf

import (
	"fmt"
	"regexp"
	"strings"
)

// nginxListen is the listen port: the Site's own, else 80. TLS lines are left
// as a comment because certificate paths are the operator's.
func nginxListen(origin string) (name, port string) {
	_, hp, _ := splitOrigin(origin)
	name, port, _ = strings.Cut(hp, ":")
	if port == "" {
		port = "80"
	}
	return name, port
}

func nginx(in Input) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	proxy := func(indent string) {
		w(indent+"proxy_pass http://%s;", in.Upstream)
		w(indent + "proxy_set_header Host $host;")
		w(indent + "proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;")
		w(indent + "proxy_set_header X-Forwarded-Proto $scheme;")
	}
	headers := func() {
		for _, h := range in.Headers {
			w("\t\tadd_header %s \"%s\" always;", h.Name, h.Value)
		}
	}

	w("# Static site published by Hatamoto: %s is served from disk.", in.Root)
	if in.Live == "" {
		w("# Island paths, the admin and previews are passed to Hatamoto on %s.", in.Upstream)
	} else {
		w("# Islands and the admin live on %s, which is passed to Hatamoto on %s.", hostOf(in.Live), in.Upstream)
	}
	name, port := nginxListen(in.Site)
	w("server {")
	w("\tlisten %s;", port)
	w("\t# Add your TLS lines here: listen 443 ssl; ssl_certificate ...; ssl_certificate_key ...;")
	w("\tserver_name %s;", name)
	w("\troot %s;", in.Root)
	if in.Live == "" {
		for _, p := range in.Proxy {
			switch {
			case p.Locale:
				w("\tlocation ~ ^/[^/]+%s$ {", regexp.QuoteMeta(p.Path))
			case p.Prefix:
				w("\tlocation ^~ %s {", p.Path)
			default:
				w("\tlocation = %s {", p.Path)
			}
			proxy("\t\t")
			w("\t}")
		}
	}
	for _, f := range hiddenFiles {
		w("\tlocation = %s {", f)
		w("\t\treturn 404;")
		w("\t}")
	}
	for _, l := range nginxRedirects(in.Redirects) {
		w("\t%s", l)
	}
	w("\tlocation / {")
	headers()
	w("\t\ttry_files $uri $uri/index.html =404;")
	w("\t}")
	w("\tlocation = /404.html {")
	headers()
	w("\t}")
	w("\terror_page 404 /404.html;")
	w("}")

	if in.Live != "" {
		lname, lport := nginxListen(in.Live)
		w("")
		w("server {")
		w("\tlisten %s;", lport)
		w("\t# Add your TLS lines here: listen 443 ssl; ssl_certificate ...; ssl_certificate_key ...;")
		w("\tserver_name %s;", lname)
		w("\tlocation / {")
		proxy("\t\t")
		w("\t}")
		w("}")
	}
	return b.String()
}
