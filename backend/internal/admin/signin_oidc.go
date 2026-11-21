package admin

import (
	"context"
	"net/http"
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

// oidcCard is the single sign-on card of Settings > Sign-in.
type oidcCard struct {
	Form    oidcEcho
	Errs    map[string]string
	Summary []ui.FieldError
	// SecretSaved: a client secret is stored and readable. SecretLost: it is
	// stored but unreadable.
	SecretSaved, SecretLost bool
	// RedirectURL is the address to register at the provider; empty when no
	// site address is configured.
	RedirectURL string
	RoleOptions []ui.Option
}

const signInPath = "/settings/users/sign-in"

// oidcRedirectURL is the callback the provider must allow. It comes from the
// configured site address only, the same value core sends to the provider.
func (a *Admin) oidcRedirectURL(r *http.Request) string { return a.providerCallbackURL(r, "oidc") }

// newOIDCCard builds the card from the saved view; echo (a refused post)
// replaces the saved values.
func (a *Admin) newOIDCCard(r *http.Request, view SignInOIDCView, echo *oidcEcho, errs []ui.FieldError) *oidcCard {
	form := oidcEcho{SignInOIDCInput: view.SignInOIDCInput, DomainsRaw: strings.Join(view.Domains, ", ")}
	form.Secret = ""
	if echo != nil {
		form = *echo
	}
	c := &oidcCard{
		Form: form, Summary: errs, Errs: map[string]string{},
		SecretSaved: view.SecretSaved, SecretLost: view.SecretLost,
		RedirectURL: a.oidcRedirectURL(r), RoleOptions: roleOptions(form.DefaultRole),
	}
	for _, e := range errs {
		c.Errs[e.ID] = e.Message
	}
	return c
}

// signInSettings returns the single sign-on service and the saved view, or
// answers the request itself and reports false.
func (a *Admin) signInSettings(w http.ResponseWriter, r *http.Request) (SignInSettingsService, SignInOIDCView, bool) {
	svc := a.deps.SignInSettings
	if svc == nil {
		a.unavailable(w, r, "Single sign-on")
		return nil, SignInOIDCView{}, false
	}
	view, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("read single sign-on settings failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The sign-in settings could not be read.")
		return nil, SignInOIDCView{}, false
	}
	return svc, view, true
}

// handleSignInOIDCSave saves the single sign-on settings. It sits behind sudo.
func (a *Admin) handleSignInOIDCSave(w http.ResponseWriter, r *http.Request) {
	svc, stored, ok := a.signInSettings(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	in, echo, errs := validateSignInOIDC(r.PostForm, stored, a.oidcRedirectURL(r))
	if len(errs) > 0 {
		a.renderSignIn(w, r, http.StatusUnprocessableEntity, "", a.newOIDCCard(r, stored, &echo, errs))
		return
	}
	if err := svc.Save(r.Context(), in, echo.KeepSecret); err != nil {
		a.logger().Error("save single sign-on settings failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The single sign-on settings could not be saved.")
		return
	}
	a.audit(r, "signin.oidc.settings", "sign-in", map[string]any{
		"enabled": in.Enabled, "issuer": in.Issuer, "client_id": in.ClientID, "domains": in.Domains,
		"default_role": in.DefaultRole, "link_existing": in.LinkExisting, "secret_changed": in.Secret != "",
	})
	a.SetFlash(w, r, "success", "Single sign-on settings saved.")
	http.Redirect(w, r, a.adminPath+signInPath, http.StatusSeeOther)
}

// handleSignInOIDCTest checks that the saved issuer answers with a discovery
// document and signing keys. It signs nobody in and sends no secret.
func (a *Admin) handleSignInOIDCTest(w http.ResponseWriter, r *http.Request) {
	svc, stored, ok := a.signInSettings(w, r)
	if !ok {
		return
	}
	to := a.adminPath + signInPath
	if stored.Issuer == "" {
		a.SetFlash(w, r, "error", "Save the issuer URL first, then test it.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if err := svc.Check(r.Context(), stored.Issuer); err != nil {
		a.logger().Info("single sign-on check failed", "err", err)
		a.SetFlash(w, r, "error", "Could not reach the provider's discovery document at "+stored.Issuer+". Check the issuer URL.")
	} else {
		label := stored.Label
		if label == "" {
			label = "Single sign-on"
		}
		a.SetFlash(w, r, "success", "The provider answered and its signing keys loaded. Sign out and use Continue with "+label+" to try a full sign-in.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
