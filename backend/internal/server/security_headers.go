package server

import (
	"context"
	"net/http"
)

// secHeader is one response header the app sets.
type secHeader struct{ Name, Value string }

// apiCSP is the policy the global middleware sets; HTML pages replace it with
// renderCSP.
const apiCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
	"object-src 'none'; base-uri 'self'; form-action 'self'"

// cspHeaderName is Content-Security-Policy once CSP_ENFORCE is on, else Report-Only.
func (s *Server) cspHeaderName() string {
	if s.cfg.Security.CSPEnforce {
		return "Content-Security-Policy"
	}
	return "Content-Security-Policy-Report-Only"
}

// baseSecurityHeaders are the headers securityHeadersMiddleware sets on every
// response, in the order it sets them; nil when headers are disabled. CSP is
// enforced or report-only per CSP_ENFORCE, HSTS only in production.
func (s *Server) baseSecurityHeaders() []secHeader {
	if s.cfg.Security.HeadersDisabled {
		return nil
	}
	hs := []secHeader{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
		{"X-XSS-Protection", "0"},
		// Restrictive default: no browser feature this API needs.
		{"Permissions-Policy", "geolocation=(), camera=(), microphone=()"},
		{s.cspHeaderName(), apiCSP},
	}
	if s.cfg.Environment == "production" {
		hs = append(hs, secHeader{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"})
	}
	return hs
}

// staticPageHeaders are the headers a public page gets (the base set with the
// render overrides), for a web server that serves the exported files itself:
// X-Frame-Options SAMEORIGIN and the page CSP with the captcha, tag and plugin
// extras. A non-empty live adds that origin to script-src, connect-src and
// form-action, because the static pages load the comment script from it and
// post forms to it. nil when headers are disabled.
func (s *Server) staticPageHeaders(ctx context.Context, live string) []secHeader {
	hs := s.baseSecurityHeaders()
	if hs == nil {
		return nil
	}
	h := http.Header{}
	for _, x := range hs {
		h.Set(x.Name, x.Value)
	}
	extras := s.cspExtras(ctx)
	if live != "" {
		extras = extras.merge(cspExtras{Script: []string{live}, Connect: []string{live}})
	}
	applyRenderSecurityHeaders(h, renderCSP, extras)
	if live != "" {
		name := s.cspHeaderName()
		h.Set(name, addToDirective(h.Get(name), "form-action", []string{live}))
	}
	for i, x := range hs {
		hs[i].Value = h.Get(x.Name)
	}
	return hs
}
