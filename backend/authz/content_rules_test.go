package authz

import (
	"context"
	"testing"
)

type contentObj struct {
	name string
	obj  Object
}

// TestContentRulesTable checks the author, contributor, reviewer and read-only answers for one object
// at a time, against the rights table written out by hand.
func TestContentRulesTable(t *testing.T) {
	const me, other = "u-me", "u-other"
	entity := func(creator, status string) Object {
		return Object{Kind: "entity", ID: "e1", CreatedBy: creator, Status: status}
	}
	objs := []contentObj{
		{"own draft", entity(me, "draft")},
		{"own published", entity(me, "published")},
		{"own scheduled", entity(me, "scheduled")},
		{"other draft", entity(other, "draft")},
		{"other published", entity(other, "published")},
		{"null-owner draft", entity("", "draft")},
	}
	update, publish, unpublish := ActionEntityUpdate, ActionEntityPublish, ActionEntityUnpublish
	del, restore := ActionEntityDelete, ActionEntityVersionRestore
	all := []Action{update, publish, unpublish, del, restore}

	// allowed[role][object name] lists the entity actions the role may do on that object.
	allowed := map[string]map[string][]Action{
		"author": {
			"own draft": all, "own published": all, "own scheduled": all,
			"other draft": nil, "other published": nil, "null-owner draft": nil,
		},
		"contributor": {
			"own draft": {update, del, restore}, "own published": nil, "own scheduled": nil,
			"other draft": nil, "other published": nil, "null-owner draft": nil,
		},
		"reviewer": {
			"own draft": {update, publish, unpublish, restore}, "own published": {update, publish, unpublish, restore},
			"own scheduled": {update, publish, unpublish, restore}, "other draft": {update, publish, unpublish, restore},
			"other published": {update, publish, unpublish, restore}, "null-owner draft": {update, publish, unpublish, restore},
		},
		"read-only": {},
	}
	has := func(as []Action, a Action) bool {
		for _, x := range as {
			if x == a {
				return true
			}
		}
		return false
	}
	for role, byObj := range allowed {
		for _, o := range objs {
			for _, a := range all {
				s := Subject{UserID: me, Role: role}
				if got, want := can2(t, s, a, o.obj), has(byObj[o.name], a); got != want {
					t.Errorf("%s %s on %s: got %v, want %v", role, a, o.name, got, want)
				}
				// A key made by the role: entity rows as the session, except entity.delete (no key scope).
				k := Subject{UserID: me, KeyID: "k", Scopes: []string{"*"}, CreatorRole: role}
				wantKey := has(byObj[o.name], a) && a != del
				if got := can2(t, k, a, o.obj); got != wantKey {
					t.Errorf("%s key %s on %s: got %v, want %v", role, a, o.name, got, wantKey)
				}
			}
		}
	}

	// Media: own uploads only, for the three content roles; never for a key; Status is not part of the rule.
	for _, role := range []string{"author", "contributor", "reviewer"} {
		for _, a := range []Action{ActionMediaUpdate, ActionMediaDelete} {
			s := Subject{UserID: me, Role: role}
			if !can2(t, s, a, Object{Kind: "media", ID: "m1", CreatedBy: me}) {
				t.Errorf("%s %s own media: denied", role, a)
			}
			for _, cb := range []string{other, ""} {
				if can2(t, s, a, Object{Kind: "media", ID: "m1", CreatedBy: cb}) {
					t.Errorf("%s %s media of %q: allowed", role, a, cb)
				}
			}
			k := Subject{UserID: me, KeyID: "k", Scopes: []string{"*"}, CreatorRole: role}
			if can2(t, k, a, Object{Kind: "media", ID: "m1", CreatedBy: me}) {
				t.Errorf("%s key %s: allowed", role, a)
			}
		}
	}

	// A contributor never publishes, whatever the object.
	for _, o := range append(objs, contentObj{"zero", Object{}}) {
		if can2(t, Subject{UserID: me, Role: "contributor"}, publish, o.obj) {
			t.Errorf("contributor publish on %s: allowed", o.name)
		}
	}
}

// TestContentRulesOwnerEditorIgnoreObject: the object never changes an owner's or an editor's answer.
func TestContentRulesOwnerEditorIgnoreObject(t *testing.T) {
	objs := []Object{
		{}, {Kind: "entity", ID: "e1", CreatedBy: "u-other", Status: "published"},
		{Kind: "entity", ID: "e1", CreatedBy: "", Status: "draft"}, {Kind: "media", ID: "m1", CreatedBy: "u-me"},
	}
	for _, role := range []string{"owner", "editor"} {
		for _, a := range Actions() {
			s := Subject{UserID: "u-me", Role: role}
			base := can2(t, s, a, Object{})
			for _, o := range objs {
				if got := can2(t, s, a, o); got != base {
					t.Errorf("%s %s: object %+v changes the answer to %v", role, a, o, got)
				}
			}
		}
	}
}

func can2(t *testing.T, s Subject, a Action, o Object) bool {
	t.Helper()
	ok, err := Default().Can(context.Background(), s, a, o)
	if err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	return ok
}
