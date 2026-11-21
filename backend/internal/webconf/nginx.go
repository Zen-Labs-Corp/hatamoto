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

func nginx(in Input, rules []rule) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	proxy := func(indent string, stream bool) {
		w(indent+"proxy_pass http://%s;", in.Upstream)
		w(indent + "proxy_http_version 1.1;")
		w(indent + "proxy_set_header Connection \"\";")
		if stream { // MCP answers with a stream; buffering would hold it back
			w(indent + "proxy_buffering off;")
		}
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
	if in.MaxBodyMB > 0 && in.Live == "" {
		w("\tclient_max_body_size %dm;", in.MaxBodyMB)
	}
	if in.Live == "" {
		for _, p := range in.Proxy {
			switch {
			case p.Fallback:
				// The file when the folder has it, else Hatamoto.
				w("\tlocation ^~ %s {", p.Path)
				w("\t\ttry_files $uri @hatamoto;")
				w("\t}")
				continue
			case p.Locale:
				w("\tlocation ~ ^/[^/]+%s$ {", regexp.QuoteMeta(p.Path))
			case p.Prefix:
				w("\tlocation ^~ %s {", p.Path)
			default:
				w("\tlocation = %s {", p.Path)
			}
			proxy("\t\t", strings.HasPrefix(p.Path, "/mcp"))
			w("\t}")
		}
		w("\tlocation @hatamoto {")
		proxy("\t\t", false)
		w("\t}")
	}
	for _, f := range hiddenFiles {
		w("\tlocation = %s {", f)
		w("\t\treturn 404;")
		w("\t}")
	}
	for _, l := range nginxRedirects(rules) {
		w("\t%s", l)
	}
	w("\tlocation / {")
	if in.Live == "" {
		// A theme preview is a query or a cookie on any page: pass it to Hatamoto.
		w("\t\tif ($arg_%s != \"\") {", previewName)
		w("\t\t\treturn 418;")
		w("\t\t}")
		w("\t\tif ($cookie_%s != \"\") {", previewName)
		w("\t\t\treturn 418;")
		w("\t\t}")
	}
	headers()
	w("\t\ttry_files $uri $uri/index.html =404;")
	w("\t}")
	w("\tlocation = /404.html {")
	headers()
	w("\t}")
	w("\terror_page 404 /404.html;")
	if in.Live == "" {
		w("\terror_page 418 = @hatamoto;")
	}
	w("}")

	if in.Live != "" {
		lname, lport := nginxListen(in.Live)
		w("")
		w("server {")
		w("\tlisten %s;", lport)
		w("\t# Add your TLS lines here: listen 443 ssl; ssl_certificate ...; ssl_certificate_key ...;")
		w("\tserver_name %s;", lname)
		if in.MaxBodyMB > 0 {
			w("\tclient_max_body_size %dm;", in.MaxBodyMB)
		}
		w("\tlocation / {")
		proxy("\t\t", true)
		w("\t}")
		w("}")
	}
	return b.String()
}
