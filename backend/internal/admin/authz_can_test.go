package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// roleViewer is the viewer of a user with role under the default policy.
func roleViewer(role string) viewer {
	return newViewer(context.Background(), slog.Default(), &CurrentUser{ID: "u-" + role, Role: role})
}

func TestCanUserNilUser(t *testing.T) {
	if canUser(context.Background(), slog.Default(), nil, authz.ActionSettingsManage) {
		t.Error("nil user may act")
	}
	if newViewer(context.Background(), slog.Default(), nil).Can(authz.ActionSettingsManage) {
		t.Error("zero viewer may act")
	}
}

func TestViewerDefaultAnswers(t *testing.T) {
	for _, c := range []struct {
		role string
		act  authz.Action
		want bool
	}{
		{"owner", authz.ActionSettingsManage, true},
		{"editor", authz.ActionSettingsManage, false},
		{"owner", authz.ActionEntityExport, true},
		{"editor", authz.ActionEntityExport, true},
	} {
		if got := roleViewer(c.role).Can(c.act); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.role, c.act, got, c.want)
		}
	}
	if v := roleViewer("editor"); v.Role != "editor" {
		t.Errorf("Role = %q", v.Role)
	}
}

func TestViewerDenyAllAndError(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	if roleViewer("owner").Can(authz.ActionSettingsManage) {
		t.Error("owner allowed under DenyAll")
	}
	authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
		return true, errors.New("boom")
	}))
	if roleViewer("owner").Can(authz.ActionSettingsManage) {
		t.Error("owner allowed after an authorizer error")
	}
}

func TestViewerAsksOncePerAction(t *testing.T) {
	rc := &authztest.Recorder{}
	authztest.Use(t, rc)
	u := &CurrentUser{ID: "u1", Role: "owner"}
	v := newViewer(context.Background(), slog.Default(), u)
	if !v.Can(authz.ActionSettingsManage) || !v.Can(authz.ActionSettingsManage) {
		t.Fatal("owner denied")
	}
	calls := rc.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if s := calls[0].Subject; s.UserID != "u1" || s.Role != "owner" || s.KeyID != "" {
		t.Errorf("subject = %+v", s)
	}
}

func TestViewViewerFallsBackToUser(t *testing.T) {
	v := &View{User: &CurrentUser{ID: "u1", Role: "owner"}}
	if !v.viewer().Can(authz.ActionSettingsManage) {
		t.Error("fallback viewer denied the owner")
	}
	if (&View{}).viewer().Can(authz.ActionSettingsManage) {
		t.Error("View without a user may act")
	}
}

func TestRequireCanDenyMatchesForbidden(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	g := newRig(t)
	req := func() *http.Request {
		r := httptest.NewRequest("GET", "/x", nil)
		return r.WithContext(context.WithValue(r.Context(), userKey, &CurrentUser{ID: "u1", Role: "owner"}))
	}
	want := httptest.NewRecorder()
	g.a.forbidden(want, req())
	got := httptest.NewRecorder()
	if g.a.requireCan(got, req(), authz.ActionSettingsManage) {
		t.Fatal("requireCan allowed under DenyAll")
	}
	if got.Code != http.StatusForbidden || got.Body.String() != want.Body.String() {
		t.Errorf("got %d %q, want 403 %q", got.Code, got.Body.String(), want.Body.String())
	}
}
