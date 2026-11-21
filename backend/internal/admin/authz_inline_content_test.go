package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// No t.Parallel: authztest.Use registers a process-wide authorizer.

func TestAuthzInlineMediaFormatsFlag(t *testing.T) {
	g := newContentRig(t)
	g.a.deps.MediaFormats = &fakeMediaFormats{}

	if body := g.do("GET", "/admin/media", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Image formats") {
		t.Fatal("owner does not see the formats panel")
	}

	authztest.Use(t, authztest.DenyAll)
	if body := g.do("GET", "/admin/media", "owner-token", nil, nil).Body.String(); strings.Contains(body, "Image formats") {
		t.Error("formats panel shown although media.settings.manage is denied")
	}
}

func TestAuthzInlineEntitiesListAsksActions(t *testing.T) {
	g := newContentRig(t)
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)

	g.do("GET", "/admin/entities?type=service", "owner-token", nil, nil)

	asked := map[authz.Action]authz.Subject{}
	for _, c := range rec.Calls() {
		asked[c.Action] = c.Subject
	}
	for _, act := range []authz.Action{authz.ActionSchemaManage, authz.ActionEntityExport} {
		s, ok := asked[act]
		if !ok {
			t.Errorf("%s was not asked", act)
			continue
		}
		if s.Role != "owner" || s.UserID == "" {
			t.Errorf("%s asked as %+v, want the owner's session subject", act, s)
		}
	}
}
