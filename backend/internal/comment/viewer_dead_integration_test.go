//go:build integration

package comment

import (
	"testing"
	"time"
)

func TestViewerDeadComment(t *testing.T) {
	f := newFixture(t)
	f.enable()
	at := time.Now().UTC().AddDate(0, 0, -1).Truncate(time.Microsecond)
	st, err := f.importer().Import(f.ctx, []ImportedComment{
		importRow(f, "1", "", "author@example.test", StatusApproved, at),
		importRow(f, "2", "1", "author@example.test", StatusDead, at.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("the importer must accept a dead row: %v", err)
	}
	author, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("author@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail("nobody-else@example.test"))
	if err == nil {
		t.Fatalf("unexpected commenter %v", other)
	}

	ok, err := f.svc.HasNonPublic(f.ctx, author.ID)
	if err != nil || !ok {
		t.Fatalf("HasNonPublic = %v, %v; want true for an author with a dead comment", ok, err)
	}
	own, err := f.svc.ViewerComments(f.ctx, f.entity, author.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || own[0].ID != st.IDs["2"] || own[0].Status != StatusDead {
		t.Fatalf("ViewerComments = %+v, want the dead comment only", own)
	}

	var status string
	f.scalar(&status, `SELECT status FROM hatamoto_comments WHERE id = $1`, st.IDs["2"])
	if status != "dead" {
		t.Errorf("stored status = %q", status)
	}
	if ok, _ := f.svc.ReplyParent(f.ctx, f.entity, st.IDs["2"]); ok {
		t.Error("a dead comment accepted a reply")
	}
}
