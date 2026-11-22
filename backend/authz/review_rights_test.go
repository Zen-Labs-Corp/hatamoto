package authz

import "testing"

// TestReviewRights checks the three review actions for every role, object and
// subject kind against the table written out by hand (plan D4).
func TestReviewRights(t *testing.T) {
	const me, other = "u-me", "u-other"
	entity := func(creator, status string) Object {
		return Object{Kind: "entity", ID: "e1", CreatedBy: creator, Status: status}
	}
	objs := []contentObj{
		{"zero", Object{}},
		{"own draft", entity(me, "draft")},
		{"own published", entity(me, "published")},
		{"other draft", entity(other, "draft")},
		{"other published", entity(other, "published")},
	}
	// ownOnly answers true for the zero Object and for objects the subject created.
	ownOnly := func(o contentObj) bool { return o.name == "zero" || o.name == "own draft" || o.name == "own published" }
	always := func(contentObj) bool { return true }
	never := func(contentObj) bool { return false }

	type rights struct{ bypass, request, decide func(contentObj) bool }
	want := map[string]rights{
		"owner":       {always, always, always},
		"editor":      {never, always, always},
		"author":      {never, ownOnly, never},
		"contributor": {never, ownOnly, never},
		"reviewer":    {always, always, always},
		"read-only":   {never, never, never},
	}
	for role, w := range want {
		for _, o := range objs {
			for _, c := range []struct {
				a    Action
				want bool
				key  bool // a key made by the role: owner and editor follow the key scope, decide has none
			}{
				{ActionEntityReviewBypass, w.bypass(o), true},
				{ActionEntityReviewRequest, w.request(o), true},
				{ActionEntityReviewDecide, w.decide(o), false},
			} {
				s := Subject{UserID: me, Role: role}
				if got := can2(t, s, c.a, o.obj); got != c.want {
					t.Errorf("%s %s on %s: got %v, want %v", role, c.a, o.name, got, c.want)
				}
				k := Subject{UserID: me, KeyID: "k", Scopes: []string{"*"}, CreatorRole: role}
				wantKey := c.want && c.key
				if got := can2(t, k, c.a, o.obj); got != wantKey {
					t.Errorf("%s key %s on %s: got %v, want %v", role, c.a, o.name, got, wantKey)
				}
			}
		}
	}
}
