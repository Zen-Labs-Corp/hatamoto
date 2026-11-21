package webconf

// caddyRedirects renders the redirect rules as Caddy directives (TODO-11).
func caddyRedirects([]Redirect) []string { return nil }

// nginxRedirects renders the redirect rules as nginx location blocks (TODO-11).
func nginxRedirects([]Redirect) []string { return nil }
