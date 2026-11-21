package admin

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
)

// Limits of the single sign-on form.
const (
	maxOIDCLabel    = 64
	maxOIDCIssuer   = 512
	maxOIDCClientID = 255
	maxOIDCSecret   = 1024
	maxOIDCDomains  = 50
)

// SignInOIDCInput is the single sign-on form as saved.
type SignInOIDCInput struct {
	Enabled      bool
	Label        string
	Issuer       string
	ClientID     string
	Secret       string // write-only; blank keeps the saved secret
	Domains      []string
	DefaultRole  string
	LinkExisting bool
}

// SignInOIDCView is what the card shows: the saved values, never the secret.
type SignInOIDCView struct {
	SignInOIDCInput // Secret is always ""
	// SecretSaved: a client secret is stored and can be read.
	SecretSaved bool
	// SecretLost: a client secret is stored but cannot be read (the server
	// SECRET changed); the owner must type it again.
	SecretLost bool
}

// SignInSettingsService is the OIDC sign-in configuration saved in the admin
// and applied to the running server at once.
type SignInSettingsService interface {
	Get(ctx context.Context) (SignInOIDCView, error)
	// Save validates again, seals the secret and applies the settings.
	// keepSecret keeps the saved secret instead of in.Secret.
	Save(ctx context.Context, in SignInOIDCInput, keepSecret bool) error
	// Check fetches the issuer's discovery document and signing keys. It sends
	// no secret.
	Check(ctx context.Context, issuer string) error
}

// oidcEcho is what the form carried, kept to redisplay after a failed save.
// The secret is never echoed.
type oidcEcho struct {
	SignInOIDCInput
	DomainsRaw string
	// KeepSecret: the secret field was blank and the saved secret stays.
	KeepSecret bool
}

var oidcDomainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// parseOIDCDomains splits on commas, spaces and line breaks, lower-cases,
// strips a leading "@" and drops duplicates. bad lists entries that are not
// domain names.
func parseOIDCDomains(raw string) (domains, bad []string) {
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		d := strings.ToLower(strings.TrimPrefix(f, "@"))
		d = strings.TrimSuffix(d, ".")
		switch {
		case seen[d]:
		case !oidcDomainRE.MatchString(d):
			bad = append(bad, f)
		default:
			seen[d] = true
			domains = append(domains, d)
		}
	}
	return domains, bad
}

// validateSignInOIDC reads the single sign-on form. redirectURL is empty when
// no site address is configured. The same rules are enforced again when the
// settings are stored.
func validateSignInOIDC(form url.Values, stored SignInOIDCView, redirectURL string) (SignInOIDCInput, oidcEcho, []ui.FieldError) {
	var errs []ui.FieldError
	bad := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }
	typed := func(k string) string { return strings.TrimSpace(form.Get(k)) }

	in := SignInOIDCInput{
		Enabled:      form.Get("enabled") != "",
		Label:        typed("label"),
		Issuer:       typed("issuer"),
		ClientID:     typed("client-id"),
		Secret:       form.Get("secret"),
		DefaultRole:  typed("role"),
		LinkExisting: form.Get("link-existing") != "",
	}
	echo := oidcEcho{SignInOIDCInput: in, DomainsRaw: form.Get("domains")}
	echo.Secret = ""

	if utf8.RuneCountInString(in.Label) > maxOIDCLabel {
		bad("oidc-label", "Provider name", "Use 64 characters or fewer.")
	}
	switch u, err := url.Parse(in.Issuer); {
	case in.Issuer == "":
		if in.Enabled {
			bad("oidc-issuer", "Issuer URL", "Enter the issuer URL your provider gave you, for example https://login.example.com.")
		}
	case len(in.Issuer) > maxOIDCIssuer:
		bad("oidc-issuer", "Issuer URL", "The issuer URL is too long.")
	case err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		bad("oidc-issuer", "Issuer URL", "Enter an https address without a query, a fragment or a user name.")
	}
	if len(in.ClientID) > maxOIDCClientID {
		bad("oidc-client-id", "Client ID", "The client ID is too long.")
	} else if in.ClientID == "" && in.Enabled {
		bad("oidc-client-id", "Client ID", "Enter the client ID from your provider.")
	}
	if len(in.Secret) > maxOIDCSecret {
		bad("oidc-secret", "Client secret", "The client secret is too long.")
	}

	domains, badDomains := parseOIDCDomains(echo.DomainsRaw)
	in.Domains = domains
	switch {
	case len(badDomains) > 0:
		bad("oidc-domains", "Allowed domains", "These are not domain names: "+strings.Join(badDomains, ", ")+". Use names like example.com.")
	case len(domains) > maxOIDCDomains:
		bad("oidc-domains", "Allowed domains", "Use 50 domains or fewer.")
	case len(domains) == 0 && in.Enabled:
		bad("oidc-domains", "Allowed domains", "Enter at least one domain, for example example.com.")
	}

	if !contains(roleValues(), in.DefaultRole) {
		bad("oidc-role", "New users get", "Choose one of the listed roles.")
	}

	switch {
	case in.Secret != "":
	case stored.SecretSaved && !stored.SecretLost:
		echo.KeepSecret = true
	case in.Enabled && stored.SecretLost:
		bad("oidc-secret", "Client secret", "The saved client secret can no longer be read. Enter it again.")
	case in.Enabled:
		bad("oidc-secret", "Client secret", "Enter the client secret from your provider.")
	}

	if in.Enabled && redirectURL == "" {
		bad("oidc-enabled", "Single sign-on", "Set the site address in Settings > General first.")
	}
	return in, echo, errs
}

// roleValues are the roles an owner can give new single sign-on users.
func roleValues() []string {
	opts := roleOptions("")
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Value
	}
	return out
}
