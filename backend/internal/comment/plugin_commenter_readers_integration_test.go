//go:build integration

package comment

import (
	"testing"

	"github.com/google/uuid"
)

// pluginCommenterWithComment stores one approved comment by a commenter a
// plugin vouches for.
func (f *fixture) pluginCommenterWithComment(account string) (uuid.UUID, Comment) {
	f.t.Helper()
	id, _, err := f.svc.EnsurePluginCommenter(f.ctx, "plug", account)
	if err != nil {
		f.t.Fatal(err)
	}
	d := f.draft("", "from "+account)
	d.CommenterID, d.Status = &id, StatusApproved
	return id, f.store(d)
}

func TestPluginCommenterReadersExportMarkSpam(t *testing.T) {
	f := newFixture(t)
	f.enable()
	id, c := f.pluginCommenterWithComment("acct-1")
	exp, err := f.svc.ExportByID(f.ctx, id)
	if err != nil || exp.Email != "" || len(exp.Comments) != 1 || exp.Commenter.OriginPlugin != "plug" {
		t.Fatalf("ExportByID = %+v, %v", exp, err)
	}
	if _, err := f.svc.ExportByID(f.ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("unknown id = %v, want ErrNotFound", err)
	}
	var before int
	f.scalar(&before, `SELECT count(*) FROM hatamoto_comment_blocks WHERE kind = 'email_domain'`)
	if _, err := f.svc.MarkSpam(f.ctx, c.ID, nil, Learn{EmailDomain: true}); err != nil {
		t.Fatalf("MarkSpam: %v", err)
	}
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_blocks WHERE kind = 'email_domain'`)
	if n != before {
		t.Errorf("email_domain blocks = %d, want %d", n, before)
	}
}

func TestPluginCommenterReadersReplyClaim(t *testing.T) {
	f := newFixture(t)
	f.enable()
	_, parent := f.pluginCommenterWithComment("acct-1")
	f.exec(`UPDATE hatamoto_comments SET notify_replies = true WHERE id = $1`, parent.ID)
	f.queueReply(f.replyTo(parent, "other@example.test"))
	mail := f.optedInParent()
	f.queueReply(f.replyTo(mail, "other@example.test"))
	got := f.claimBatches()
	if len(got) != 1 || got[0].Email != "parent@example.test" {
		t.Fatalf("claimed %+v, want only the email commenter's batch", got)
	}
}

func TestPluginCommenterReadersEraseByID(t *testing.T) {
	f := newFixture(t)
	f.enable()
	id, c := f.pluginCommenterWithComment("acct-1")
	rec := &deletedRecorder{}
	f.svc.Register(rec)
	res, err := f.svc.EraseByID(f.ctx, id)
	if err != nil || res != (EraseResult{Deleted: 1}) {
		t.Fatalf("EraseByID = %+v, %v", res, err)
	}
	if rec.has(c.ID) != 1 {
		t.Errorf("OnDeleted for the comment fired %d times, want 1", rec.has(c.ID))
	}
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_commenters WHERE id = $1`, id)
	if n != 0 {
		t.Errorf("commenter rows = %d, want 0", n)
	}
	if _, err := f.svc.EraseByID(f.ctx, id); err != ErrNotFound {
		t.Errorf("second erase = %v, want ErrNotFound", err)
	}
}
