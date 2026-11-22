package admin

import (
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/user"
)

// adminWriteAllow lists the non-read routes that may answer something other
// than 403 for the restricted roles, with the reason.
var adminWriteAllow = map[string]string{
	"POST /admin/logout": "own account", "POST /admin/profile": "own account", "POST /admin/profile/density": "own account",
	"POST /admin/profile/password": "own account", "POST /admin/profile/complete": "own account", "POST /admin/sudo": "own account",
	"POST /admin/profile/security/codes": "own 2FA", "POST /admin/profile/security/passkeys/begin": "own passkey",
	"POST /admin/profile/security/passkeys/finish": "own passkey", "POST /admin/profile/security/passkeys/{id}/remove": "own passkey",
	"POST /admin/profile/security/totp": "own 2FA", "POST /admin/profile/security/totp/begin": "own 2FA", "POST /admin/profile/security/totp/remove": "own 2FA",
	"POST /admin/login/2fa":   "public sign-in step, redirects any caller without a pending 2FA login",
	"POST /admin/theme":       "colour-scheme cookie, mounted before the session chain",
	"DELETE /admin/{rest...}": "fallback 404 page", "PATCH /admin/{rest...}": "fallback 404 page", "POST /admin/{rest...}": "fallback 404 page", "PUT /admin/{rest...}": "fallback 404 page",
}

// contentWriteAllow lists the content screens' non-read routes the author, contributor and reviewer columns may reach:
// they sit behind content.write, and the entity service and the media handlers decide per object (and the route's own
// action, for the few that have one). Nothing else may be reachable.
var contentWriteAllow = map[string]string{
	"POST /admin/entities":                                              "create (entity.create is checked in the service; a reviewer is refused there)",
	"POST /admin/entities/bulk":                                         "bulk status and delete: each row is checked in the service",
	"POST /admin/entities/drafts":                                       "new draft (entity.create route gate)",
	"POST /admin/entities/{id}":                                         "entity save: checked in the service",
	"POST /admin/entities/{id}/analyze":                                 "editor analysis of the posted content, stores nothing",
	"POST /admin/entities/{id}/delete":                                  "delete: checked in the service",
	"POST /admin/entities/{id}/edit":                                    "page and blog editor save: checked in the service",
	"POST /admin/entities/{id}/edit/analyze":                            "editor analysis of the posted content, stores nothing",
	"POST /admin/entities/{id}/edit/autosave":                           "editor autosave of a draft: checked in the service",
	"POST /admin/entities/{id}/edit/links/insert":                       "wraps a phrase in the posted content and returns it; stores nothing",
	"POST /admin/entities/{id}/edit/links/suggest":                      "lists link rules matching the posted content; stores nothing",
	"POST /admin/entities/{id}/edit/preview":                            "preview of the posted content, stores nothing",
	"POST /admin/entities/{id}/edit/recovery":                           "the editor's own unsaved-changes copy",
	"POST /admin/entities/{id}/edit/recovery/discard":                   "the editor's own unsaved-changes copy",
	"POST /admin/entities/{id}/edit/render":                             "builder render of the posted document, stores nothing",
	"POST /admin/entities/{id}/builder/analyze":                         "builder analysis of the posted document, stores nothing",
	"POST /admin/entities/{id}/builder/component":                       "builder component render, stores nothing",
	"POST /admin/entities/{id}/builder/recovery":                        "the builder's own unsaved-changes copy",
	"POST /admin/entities/{id}/builder/recovery/discard":                "the builder's own unsaved-changes copy",
	"POST /admin/entities/{id}/builder/save":                            "builder save: checked in the service",
	"POST /admin/entities/{id}/builder/stage":                           "builder staging of the posted document, stores nothing",
	"POST /admin/entities/{id}/seo":                                     "inline SEO save: checked in the service",
	"POST /admin/entities/{id}/status":                                  "status change: checked in the service",
	"POST /admin/entities/{id}/versions/{version}/nodes/{node}/restore": "node restore: checked in the service",
	"POST /admin/entities/{id}/versions/{version}/restore":              "version restore: checked in the service",
	"POST /admin/review/{id}/approve":                                   "review decision: the handler asks entity.review.decide first (a reviewer holds it), then refuses the requester",
	"POST /admin/review/{id}/discard":                                   "review decision: entity.review.decide, as approve",
	"POST /admin/review/{id}/publish":                                   "review decision: entity.review.decide, as approve",
	"POST /admin/review/{id}/send-back":                                 "review decision: entity.review.decide, as approve",
	"POST /admin/review/{id}/withdraw":                                  "review withdraw: the handler lets only the requester through",
	"POST /admin/media/bulk":                                            "bulk media: each item is checked against its uploader",
	"POST /admin/media/upload":                                          "upload (media.upload)",
	"POST /admin/media/{id}":                                            "media update: checked against the uploader",
	"POST /admin/media/{id}/delete":                                     "media delete: checked against the uploader",
	"POST /admin/media/{id}/replace":                                    "media replace: checked against the uploader",
}

// assertNoWrites fails when a non-read route answers a listed column with
// anything but 403. Public rows (anon cell is not a 303 to sign-in) are
// skipped; every allow key must still match a line, so the list cannot go stale.
func assertNoWrites(t *testing.T, lines, cols []string, allow map[string]string) {
	t.Helper()
	used := map[string]bool{}
	for _, l := range lines {
		f := strings.Fields(l)
		pattern := f[0] + " " + f[1]
		if _, ok := allow[pattern]; ok {
			used[pattern] = true
		}
		if f[0] == "GET" || f[0] == "HEAD" || f[0] == "OPTIONS" || !strings.Contains(l, " anon=303") {
			continue
		}
		if _, ok := allow[pattern]; ok {
			continue
		}
		for _, c := range cols {
			if !strings.Contains(l, " "+c+"=403") {
				t.Errorf("%s is not refused for %s: %s", pattern, c, l)
			}
		}
	}
	for k := range allow {
		if !used[k] {
			t.Errorf("stale allow entry %q", k)
		}
	}
}

// TestAdminRolesRouteMatrix pins every admin route for the four restricted
// built-in roles and proves none of them can write outside their own account.
func TestAdminRolesRouteMatrix(t *testing.T) {
	roles := []user.Role{user.RoleReadOnly, user.RoleAuthor, user.RoleContributor, user.RoleReviewer}
	build := func() []string {
		ids := map[user.Role]uuid.UUID{}
		g := newRig(t, func(d *Deps) {
			fu := d.Users.(fakeUsers)
			for _, r := range roles {
				u := &user.User{ID: uuid.New(), Email: string(r) + "@example.com", Role: r}
				fu[u.ID] = u
				ids[r] = u.ID
			}
		})
		var ps []principal
		for _, r := range roles {
			ps = append(ps, principal{string(r), ids[r]})
		}
		ps = append([]principal{{"anon", uuid.Nil}}, ps...)
		return adminAuthzMatrixFor(g, ps)
	}
	lines := build()
	got := strings.Join(lines, "\n") + "\n"
	if again := strings.Join(build(), "\n") + "\n"; again != got {
		t.Fatal("matrix is not deterministic across two runs")
	}

	const golden = "testdata/route_authz_roles.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("matrix differs from %s; regenerate with UPDATE_GOLDEN=1 if deliberate", golden)
		}
	}

	assertNoWrites(t, lines, []string{"read-only"}, adminWriteAllow)
	content := map[string]string{}
	maps.Copy(content, adminWriteAllow)
	maps.Copy(content, contentWriteAllow)
	assertNoWrites(t, lines, []string{"author", "contributor", "reviewer"}, content)
	var dash string
	for _, l := range lines {
		if strings.HasPrefix(l, "GET /admin ") {
			dash = l
		}
	}
	if !strings.Contains(dash, " read-only=200") {
		t.Errorf("read-only must reach the dashboard: %q", dash)
	}
}
