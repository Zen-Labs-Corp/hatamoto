//go:build integration

package comment

import "testing"

const policyOn = `{"policy":{"enabled":true,"max_depth":3}}`

func TestStoreDepthFollowsPolicy(t *testing.T) {
	f := newFixture(t)
	set := Defaults()
	set.MaxDepth = 2
	if err := f.svc.SaveSettings(f.ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	f.setComments(policyOn)
	r := f.store(Draft{EntityID: f.entity, BodyRaw: "r", BodyHTML: "<p>r</p>", Status: StatusApproved})
	parent := r
	for want := 2; want <= 3; want++ {
		parent = f.store(Draft{EntityID: f.entity, ParentID: &parent.ID, BodyRaw: "x", BodyHTML: "<p>x</p>", Status: StatusApproved})
		if parent.Depth != want {
			t.Fatalf("reply depth = %d, want %d (type allows 3, site 2)", parent.Depth, want)
		}
	}
	c := f.store(Draft{EntityID: f.entity, ParentID: &parent.ID, BodyRaw: "x", BodyHTML: "<p>x</p>", Status: StatusApproved})
	if c.Depth != 3 || c.ParentID == nil || *c.ParentID != *parent.ParentID {
		t.Errorf("reply past the type's depth: depth %d parent %v, want clamped to depth 3", c.Depth, c.ParentID)
	}
}

func TestConfirmOnPolicyTypeApprovesNewCommenter(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.setComments(policyOn)
	d := f.draft("new@example.test", "see https://example.test")
	d.HasLinks = true
	c, err := f.svc.Confirm(f.ctx, f.store(d).ID)
	if err != nil || c.Status != StatusApproved {
		t.Fatalf("policy type confirm = %s, %v; want approved", c.Status, err)
	}
	// A hold verdict still holds on a policy type.
	d = f.draft("new2@example.test", "hello")
	d.SpamVerdict = "hold"
	if c, err = f.svc.Confirm(f.ctx, f.store(d).ID); err != nil || c.Status != StatusPending {
		t.Errorf("hold verdict confirm = %s, %v; want pending", c.Status, err)
	}
	// Policy off: the new commenter is held, as before.
	f.setComments(`{"policy":{"enabled":false}}`)
	if c, err = f.svc.Confirm(f.ctx, f.store(f.draft("new3@example.test", "hi")).ID); err != nil || c.Status != StatusPending {
		t.Errorf("policy off confirm = %s, %v; want pending", c.Status, err)
	}
}

func TestEditLinkReholdFollowsPolicy(t *testing.T) {
	f := newFixture(t)
	approved := func(email string) Comment {
		d := f.draft(email, "hello")
		d.Status = StatusApproved
		return f.store(d)
	}
	linked := Edited{BodyRaw: "https://example.test", BodyHTML: "<p>l</p>", HasLinks: true}

	c, err := f.svc.Edit(f.ctx, approved("e1@example.test").ID, linked)
	if err != nil || c.Status != StatusPending {
		t.Fatalf("no policy: link edit = %s, %v; want pending", c.Status, err)
	}
	f.setComments(policyOn)
	c, err = f.svc.Edit(f.ctx, approved("e2@example.test").ID, linked)
	if err != nil || c.Status != StatusApproved || !c.HasLinks {
		t.Errorf("policy: link edit = %s has_links %v, %v; want approved with has_links kept", c.Status, c.HasLinks, err)
	}
	held := linked
	held.Hold = true
	c, err = f.svc.Edit(f.ctx, approved("e3@example.test").ID, held)
	if err != nil || c.Status != StatusPending {
		t.Errorf("policy: held edit = %s, %v; want pending", c.Status, err)
	}
}
