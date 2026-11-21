package admin

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const oidcRedirect = "https://site.example/admin/login/provider/oidc/callback"

func oidcForm(kv ...string) url.Values {
	v := url.Values{
		"enabled": {"1"}, "label": {"Acme SSO"}, "issuer": {"https://login.example.com"},
		"client-id": {"client-1"}, "secret": {"s3cret"}, "domains": {"acme.com"}, "role": {"editor"},
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "-" {
			v.Del(kv[i])
		} else {
			v.Set(kv[i], kv[i+1])
		}
	}
	return v
}

func TestValidateSignInOIDC(t *testing.T) {
	t.Parallel()

	saved := SignInOIDCView{SecretSaved: true}
	lost := SignInOIDCView{SecretSaved: true, SecretLost: true}
	for name, tc := range map[string]struct {
		form     url.Values
		stored   SignInOIDCView
		redirect string
		wantErr  string // field id, "" for none
		keep     bool
	}{
		"valid":                    {oidcForm(), SignInOIDCView{}, oidcRedirect, "", false},
		"owner role":               {oidcForm("role", "owner"), SignInOIDCView{}, oidcRedirect, "", false},
		"admin role":               {oidcForm("role", "admin"), SignInOIDCView{}, oidcRedirect, "oidc-role", false},
		"http issuer":              {oidcForm("issuer", "http://login.example.com"), SignInOIDCView{}, oidcRedirect, "oidc-issuer", false},
		"issuer with query":        {oidcForm("issuer", "https://login.example.com/?a=b"), SignInOIDCView{}, oidcRedirect, "oidc-issuer", false},
		"issuer with user":         {oidcForm("issuer", "https://u:p@login.example.com"), SignInOIDCView{}, oidcRedirect, "oidc-issuer", false},
		"enabled no issuer":        {oidcForm("issuer", ""), SignInOIDCView{}, oidcRedirect, "oidc-issuer", false},
		"enabled no client id":     {oidcForm("client-id", ""), SignInOIDCView{}, oidcRedirect, "oidc-client-id", false},
		"enabled no domains":       {oidcForm("domains", " "), SignInOIDCView{}, oidcRedirect, "oidc-domains", false},
		"bad domain":               {oidcForm("domains", "acme.com, not a domain!"), SignInOIDCView{}, oidcRedirect, "oidc-domains", false},
		"enabled no site address":  {oidcForm(), SignInOIDCView{}, "", "oidc-enabled", false},
		"enabled no secret":        {oidcForm("secret", ""), SignInOIDCView{}, oidcRedirect, "oidc-secret", false},
		"blank secret keeps saved": {oidcForm("secret", ""), saved, oidcRedirect, "", true},
		"blank secret, lost":       {oidcForm("secret", ""), lost, oidcRedirect, "oidc-secret", false},
		"typed secret, lost":       {oidcForm(), lost, oidcRedirect, "", false},
		"disabled incomplete":      {oidcForm("enabled", "-", "issuer", "", "client-id", "", "secret", "", "domains", ""), SignInOIDCView{}, "", "", false},
		"label too long":           {oidcForm("label", strings.Repeat("a", 65)), SignInOIDCView{}, oidcRedirect, "oidc-label", false},
		"long issuer":              {oidcForm("issuer", "https://x.example/"+strings.Repeat("a", 512)), SignInOIDCView{}, oidcRedirect, "oidc-issuer", false},
	} {
		in, echo, errs := validateSignInOIDC(tc.form, tc.stored, tc.redirect)
		if tc.wantErr == "" {
			if len(errs) != 0 {
				t.Errorf("%s: unexpected errors %+v", name, errs)
			}
		} else if len(errs) != 1 || errs[0].ID != tc.wantErr {
			t.Errorf("%s: errors %+v, want one for %s", name, errs, tc.wantErr)
		}
		if echo.KeepSecret != tc.keep {
			t.Errorf("%s: KeepSecret = %v, want %v", name, echo.KeepSecret, tc.keep)
		}
		if echo.Secret != "" {
			t.Errorf("%s: the secret is echoed", name)
		}
		if name == "valid" && (!in.Enabled || in.Secret != "s3cret" || in.Issuer != "https://login.example.com" || in.ClientID != "client-1") {
			t.Errorf("valid input = %+v", in)
		}
	}
}

func TestValidateSignInOIDCDomains(t *testing.T) {
	t.Parallel()

	in, echo, errs := validateSignInOIDC(oidcForm("domains", "@Acme.COM, acme.com\nsub.Example.org"), SignInOIDCView{}, oidcRedirect)
	if len(errs) != 0 {
		t.Fatalf("errors: %+v", errs)
	}
	if want := []string{"acme.com", "sub.example.org"}; !reflect.DeepEqual(in.Domains, want) {
		t.Errorf("domains = %v, want %v", in.Domains, want)
	}
	if echo.DomainsRaw != "@Acme.COM, acme.com\nsub.Example.org" {
		t.Errorf("echo domains = %q", echo.DomainsRaw)
	}
	_, _, errs = validateSignInOIDC(oidcForm("domains", strings.Repeat("a.com ", 1)+manyDomains(51)), SignInOIDCView{}, oidcRedirect)
	if len(errs) != 1 || errs[0].ID != "oidc-domains" {
		t.Errorf("51 domains: %+v", errs)
	}
}

func manyDomains(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("d")
		b.WriteString(strings.Repeat("x", i%5))
		b.WriteString(url.PathEscape(string(rune('a' + i%26))))
		b.WriteString(".example.com ")
	}
	return b.String()
}
