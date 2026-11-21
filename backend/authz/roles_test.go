package authz

import (
	"context"
	"slices"
	"testing"
)

// wantReadOnly is what every built-in role other than owner and editor may
// do, written out independently of readRights.
var wantReadOnly = []string{
	"analytics.read", "apikey.creator.act", "dashboard.read", "entity.read", "form.read",
	"media.read", "menu.read", "profile.update", "redirect.read", "schema.read", "term.read",
}

var otherRoles = []string{"author", "contributor", "reviewer", "read-only"}

// wantContent is what each content role may do with the zero Object, on top of wantReadOnly, written out
// independently of contentRights. Read-only has none.
var wantContent = map[string][]string{
	"author": {
		"content.write", "entity.create", "entity.update", "entity.publish", "entity.unpublish", "entity.delete",
		"entity.version.restore", "entity.review.request", "media.upload", "media.update", "media.delete",
	},
	"contributor": {
		"content.write", "entity.create", "entity.update", "entity.delete", "entity.version.restore",
		"entity.review.request", "media.upload", "media.update", "media.delete",
	},
	"reviewer": {
		"content.write", "entity.update", "entity.publish", "entity.unpublish", "entity.version.restore",
		"entity.review.bypass", "entity.review.decide", "entity.review.request",
		"media.upload", "media.update", "media.delete",
	},
	"read-only": nil,
}

// wantContentKey is what a key made by each content role may do with a "*" scope: the actions above that
// have a key scope (entity.delete, media.update, media.delete and content.write have none).
var wantContentKey = map[string][]string{
	"author":      {"entity.create", "entity.update", "entity.publish", "entity.unpublish", "entity.version.restore", "entity.review.request", "media.upload"},
	"contributor": {"entity.create", "entity.update", "entity.version.restore", "entity.review.request", "media.upload"},
	"reviewer":    {"entity.update", "entity.publish", "entity.unpublish", "entity.version.restore", "entity.review.bypass", "entity.review.request", "media.upload"},
	"read-only":   nil,
}

func can(t *testing.T, s Subject, a Action) bool {
	t.Helper()
	ok, err := Default().Can(context.Background(), s, a, Object{})
	if err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	return ok
}

func TestBuiltinRoleRights(t *testing.T) {
	for _, role := range otherRoles {
		for _, a := range Actions() {
			got := can(t, Subject{UserID: "u", Role: role}, a)
			want := slices.Contains(wantReadOnly, string(a)) || slices.Contains(wantContent[role], string(a))
			if got != want {
				t.Errorf("%s %s: got %v, want %v", role, a, got, want)
			}
		}
	}
	// No read-only action is a write: none has a write scope, none is site.write or a plugin action. No
	// content role may do site.write or a plugin action either.
	for _, role := range otherRoles {
		for _, a := range []Action{ActionSiteWrite, ActionMenuManage, ActionSettingsManage, ActionUserManage, ActionPluginManage, ActionThemeManage} {
			if can(t, Subject{UserID: "u", Role: role}, a) {
				t.Errorf("%s may %s", role, a)
			}
		}
	}
	for _, s := range wantReadOnly {
		a := Action(s)
		if r := table[a]; len(r.scope) >= 6 && r.scope[:6] == "write:" {
			t.Errorf("%s has write scope %q", a, r.scope)
		}
		if a == ActionSiteWrite || len(s) >= 7 && s[:7] == "plugin." {
			t.Errorf("%s must not be allowed", a)
		}
	}
}

// oldKey is the key predicate before the built-in roles: only owner and editor creators.
func oldKey(s Subject, a Action) bool {
	r := table[a]
	// An editor-made key needs the editor right (entity.review.bypass has a scope but is not an editor right).
	creatorOK := s.UserID != "" && (s.CreatorRole == "owner" || s.CreatorRole == "editor" && r.editor)
	return creatorOK && r.scope != "" && hasScope(s.Scopes, s.CreatorRole, r.scope)
}

func TestKeyCreatorRoleCeiling(t *testing.T) {
	scopeSets := [][]string{nil, {"*"}, {"write:entities", "read:media"}}
	for _, sc := range knownScopes {
		scopeSets = append(scopeSets, []string{sc})
	}
	for _, creator := range []string{"owner", "editor"} {
		for _, a := range Actions() {
			for _, sc := range scopeSets {
				s := Subject{UserID: "u", KeyID: "k", Scopes: sc, CreatorRole: creator}
				if got, want := can(t, s, a), oldKey(s, a); got != want {
					t.Errorf("creator=%s %s scopes=%v: got %v, want %v", creator, a, sc, got, want)
				}
			}
		}
	}

	scoped := []string{"entity.read", "media.read", "form.read", "analytics.read", "menu.read", "redirect.read", "schema.read", "term.read"}
	for _, role := range otherRoles {
		for _, a := range Actions() {
			s := Subject{UserID: "u", KeyID: "k", Scopes: []string{"*"}, CreatorRole: role}
			want := slices.Contains(scoped, string(a)) || slices.Contains(wantContentKey[role], string(a))
			if got := can(t, s, a); got != want {
				t.Errorf("%s creator, * key, %s: got %v, want %v", role, a, got, want)
			}
		}
		// The write list is checked for read-only only; the others act within their rights (wantContentKey).
		if role != "read-only" {
			continue
		}
		for _, a := range []Action{ActionEntityUpdate, ActionMediaUpload, ActionMenuManage, ActionCommentModerate, ActionEntityPublish} {
			if can(t, Subject{UserID: "u", KeyID: "k", Scopes: []string{"*"}, CreatorRole: role}, a) {
				t.Errorf("%s creator key may %s", role, a)
			}
		}
	}
	for _, creator := range []string{"", "viewer"} {
		for _, a := range Actions() {
			if can(t, Subject{UserID: "u", KeyID: "k", Scopes: []string{"*"}, CreatorRole: creator}, a) {
				t.Errorf("creator %q key may %s", creator, a)
			}
		}
	}
}

func TestOwnerEditorUnchanged(t *testing.T) {
	for _, a := range Actions() {
		if !can(t, Subject{UserID: "u", Role: "owner"}, a) {
			t.Errorf("owner denied %s", a)
		}
		if got := can(t, Subject{UserID: "u", Role: "editor"}, a); got != table[a].editor {
			t.Errorf("editor %s: got %v, want %v", a, got, table[a].editor)
		}
	}
}
