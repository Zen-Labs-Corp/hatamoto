package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authstate"
)

// fakeProviderSignIn is the registered providers as the login page sees them.
type fakeProviderSignIn struct {
	infos []authstate.ProviderInfo
	// start and finish script the handlers; nil means "not used by this test".
	start  func(id, callbackURL, state string) (string, string, error)
	finish func(id string, req authstate.FinishRequest) (uuid.UUID, bool, error)
}

func (f *fakeProviderSignIn) Providers() []authstate.ProviderInfo { return f.infos }

func (f *fakeProviderSignIn) Start(_ context.Context, id, callbackURL, state string) (string, string, error) {
	return f.start(id, callbackURL, state)
}

func (f *fakeProviderSignIn) Finish(_ context.Context, id string, req authstate.FinishRequest) (uuid.UUID, bool, error) {
	return f.finish(id, req)
}

func TestLoginPageListsProviders(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) {
		d.ProviderSignIn = &fakeProviderSignIn{infos: []authstate.ProviderInfo{{ID: "example", Name: "Example IdP"}}}
	})
	body := g.do("GET", "/admin/login?next=%2Fadmin%2Fsettings", "", nil, nil).Body.String()
	for _, want := range []string{"Continue with Example IdP", `href="/admin/login/provider/example/start?next=%2Fadmin%2Fsettings"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("login page lacks %q", want)
		}
	}
	compareGolden(t, "pages/auth-login-providers.html", normalizePage(g.do("GET", "/admin/login", "", nil, nil).Body.String()))
}

func TestLoginPageWithoutProvidersHasNoProviderBlock(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if body := g.do("GET", "/admin/login", "", nil, nil).Body.String(); strings.Contains(body, "Continue with") {
		t.Fatal("provider block rendered with no provider registered")
	}
}
