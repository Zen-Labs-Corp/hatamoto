//go:build integration

package comment

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	importIP    = "203.0.113.77"
	importEmail = "  Sentinel.Importer@Example.TEST "
)

type countingObserver struct {
	NopObserver
	calls int
}

func (o *countingObserver) OnCreated(context.Context, Comment)  { o.calls++ }
func (o *countingObserver) OnApproved(context.Context, Comment) { o.calls++ }
func (o *countingObserver) OnSpam(context.Context, Comment)     { o.calls++ }

func (f *fixture) importer() *Importer { return NewImporter(f.db, "integration-test-secret") }

func importRow(f *fixture, key, parent, email string, st Status, at time.Time) ImportedComment {
	return ImportedComment{Key: key, ParentKey: parent, EntityID: f.entity, AuthorName: "Old " + key,
		Email: email, IP: importIP, BodyRaw: "body " + key, BodyHTML: "<p>body " + key + "</p>", Status: st, CreatedAt: at}
}

func (f *fixture) commenterTrust(email string) TrustLevel {
	f.t.Helper()
	c, err := f.svc.Commenter(f.ctx, f.svc.Keys().HashEmail(email))
	if err != nil {
		f.t.Fatal(err)
	}
	return c.Trust
}

func TestImporterTreeSilentAndHashed(t *testing.T) {
	f := newFixture(t)
	obs := &countingObserver{}
	f.svc.Register(obs)
	at := time.Now().UTC().AddDate(-1, 0, 0).Truncate(time.Microsecond)
	batch := []ImportedComment{
		importRow(f, "1", "", importEmail, StatusApproved, at),
		importRow(f, "2", "", "b@example.test", StatusPending, at.Add(time.Hour)),
		importRow(f, "3", "1", importEmail, StatusApproved, at.Add(2*time.Hour)),
		importRow(f, "4", "3", "b@example.test", StatusTrash, at.Add(3*time.Hour)),
		importRow(f, "5", "nope", "b@example.test", StatusSpam, at.Add(4*time.Hour)),
	}
	st, err := f.importer().Import(f.ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if st.CommentersCreated != 2 || st.Reparented != 1 || st.Clamped != 0 || len(st.IDs) != 5 {
		t.Fatalf("stats = %+v", st)
	}
	var parent *uuid.UUID
	f.scalar(&parent, `SELECT parent_id FROM hatamoto_comments WHERE id = $1`, st.IDs["4"])
	if parent == nil || *parent != st.IDs["3"] {
		t.Fatalf("reply-to-reply parent = %v, want %v", parent, st.IDs["3"])
	}
	f.scalar(&parent, `SELECT parent_id FROM hatamoto_comments WHERE id = $1`, st.IDs["5"])
	if parent != nil {
		t.Fatalf("reparented row has parent %v", parent)
	}
	var depth int
	f.scalar(&depth, `SELECT depth FROM hatamoto_comments WHERE id = $1`, st.IDs["4"])
	if depth != 3 {
		t.Fatalf("depth = %d, want 3", depth)
	}
	var created time.Time
	var status string
	f.scalar(&created, `SELECT created_at FROM hatamoto_comments WHERE id = $1`, st.IDs["1"])
	if !created.Equal(at) {
		t.Fatalf("created_at = %v, want %v", created, at)
	}
	f.scalar(&status, `SELECT status FROM hatamoto_comments WHERE id = $1`, st.IDs["4"])
	if status != "trash" {
		t.Fatalf("trash row status = %q", status)
	}

	ip, net := f.svc.Keys().HashIP(importIP)
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comments WHERE ip_hash = $1 AND net_hash = $2`, ip, net)
	if n != 5 {
		t.Fatalf("rows with hashed ip = %d, want 5", n)
	}
	for _, tbl := range []string{"hatamoto_comments", "hatamoto_commenters"} {
		for _, s := range []string{importIP, "sentinel.importer", "Sentinel"} {
			f.scalar(&n, fmt.Sprintf(`SELECT count(*) FROM %s c WHERE to_jsonb(c)::text ILIKE $1`, tbl), "%"+s+"%")
			if n != 0 {
				t.Fatalf("%s leaks %q", tbl, s)
			}
		}
	}

	h := f.svc.Keys().HashEmail(importEmail)
	var sealed []byte
	f.scalar(&sealed, `SELECT email_sealed FROM hatamoto_commenters WHERE email_hash = $1`, h)
	if got, ok := f.svc.Keys().OpenEmail(sealed, h); !ok || got != NormalizeEmail(importEmail) {
		t.Fatalf("OpenEmail = %q, %v", got, ok)
	}

	var enabled bool
	f.scalar(&enabled, `SELECT enabled FROM hatamoto_comment_settings WHERE id`)
	if enabled {
		t.Fatal("import enabled comments")
	}
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_entities WHERE entity_id = $1 AND mode IS NOT NULL`, f.entity)
	if n != 0 {
		t.Fatal("import wrote an entity mode")
	}
	f.wantCounts(2, 1, "after import")
	if obs.calls != 0 {
		t.Fatalf("observer saw %d calls", obs.calls)
	}

	// A re-run attaches to an earlier import through ParentID.
	pid := st.IDs["1"]
	again := importRow(f, "9", "", "b@example.test", StatusApproved, at.Add(5*time.Hour))
	again.ParentID = &pid
	st2, err := f.importer().Import(f.ctx, []ImportedComment{again})
	if err != nil {
		t.Fatal(err)
	}
	if st2.CommentersUpdated != 1 || st2.CommentersCreated != 0 || st2.Reparented != 0 {
		t.Fatalf("rerun stats = %+v", st2)
	}
	f.scalar(&parent, `SELECT parent_id FROM hatamoto_comments WHERE id = $1`, st2.IDs["9"])
	if parent == nil || *parent != pid {
		t.Fatalf("rerun parent = %v", parent)
	}
}

func TestImporterClampsAtMaxDepth(t *testing.T) {
	f := newFixture(t)
	at := time.Now().UTC().AddDate(-1, 0, 0)
	var batch []ImportedComment
	for i := 1; i <= 7; i++ {
		parent := ""
		if i > 1 {
			parent = fmt.Sprint(i - 1)
		}
		batch = append(batch, importRow(f, fmt.Sprint(i), parent, "chain@example.test", StatusApproved, at.Add(time.Duration(i)*time.Minute)))
	}
	st, err := f.importer().Import(f.ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if st.Clamped == 0 {
		t.Fatal("expected clamped replies")
	}
	var maxDepth int
	f.scalar(&maxDepth, `SELECT max(depth) FROM hatamoto_comments WHERE entity_id = $1`, f.entity)
	if maxDepth != 5 {
		t.Fatalf("max depth = %d, want 5", maxDepth)
	}
}

func TestImporterUsesTypeReplyDepth(t *testing.T) {
	f := newFixture(t)
	f.setComments(`{"policy":{"enabled":true,"max_depth":7}}`)
	at := time.Now().UTC().AddDate(-1, 0, 0)
	var batch []ImportedComment
	for i := 1; i <= 9; i++ {
		parent := ""
		if i > 1 {
			parent = fmt.Sprint(i - 1)
		}
		batch = append(batch, importRow(f, fmt.Sprint(i), parent, "chain@example.test", StatusApproved, at.Add(time.Duration(i)*time.Minute)))
	}
	if _, err := f.importer().Import(f.ctx, batch); err != nil {
		t.Fatal(err)
	}
	var maxDepth int
	f.scalar(&maxDepth, `SELECT max(depth) FROM hatamoto_comments WHERE entity_id = $1`, f.entity)
	if maxDepth != 7 {
		t.Fatalf("max depth = %d, want 7 (type policy, site default is 5)", maxDepth)
	}
}

func TestImporterTrust(t *testing.T) {
	f := newFixture(t)
	old := time.Now().UTC().AddDate(0, 0, -400)
	run := func(email string, n int) {
		t.Helper()
		var batch []ImportedComment
		for i := 0; i < n; i++ {
			batch = append(batch, importRow(f, fmt.Sprint(i), "", email, StatusApproved, old.Add(time.Duration(i)*time.Hour)))
		}
		if _, err := f.importer().Import(f.ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	run("four@example.test", 4)
	if got := f.commenterTrust("four@example.test"); got != TrustKnown {
		t.Fatalf("4 old approved = %q, want known", got)
	}
	run("fifteen@example.test", 15)
	if got := f.commenterTrust("fifteen@example.test"); got != TrustKnown {
		t.Fatalf("15 old approved = %q, want known (capped)", got)
	}
	run("few@example.test", 2)
	if got := f.commenterTrust("few@example.test"); got != TrustNew {
		t.Fatalf("2 approved = %q, want new", got)
	}

	for email, level := range map[string]TrustLevel{"blocked@example.test": TrustBlocked, "vouched@example.test": TrustTrusted} {
		h := f.svc.Keys().HashEmail(email)
		sealed, err := f.svc.Keys().SealEmail(email, h)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(`INSERT INTO hatamoto_commenters (email_hash, email_sealed, trust_level) VALUES ($1, $2, $3)`, h, sealed, string(level))
		run(email, 4)
		if got := f.commenterTrust(email); got != level {
			t.Fatalf("%s after import = %q, want %q", level, got, level)
		}
	}
}

func TestImporterRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	at := time.Now().UTC()
	for name, mod := range map[string]func(*ImportedComment){
		"draft":    func(c *ImportedComment) { c.Status = StatusDraft },
		"no email": func(c *ImportedComment) { c.Email = " " },
		"no @":     func(c *ImportedComment) { c.Email = "nobody" },
	} {
		c := importRow(f, "1", "", "x@example.test", StatusApproved, at)
		mod(&c)
		_, err := f.importer().Import(f.ctx, []ImportedComment{c})
		if err == nil {
			t.Fatalf("%s: want an error", name)
		}
		if strings.Contains(err.Error(), "nobody") || strings.Contains(err.Error(), "x@example") {
			t.Fatalf("%s: error leaks the email: %v", name, err)
		}
	}
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comments`)
	if n != 0 {
		t.Fatalf("rejected batches wrote %d rows", n)
	}
}

func TestImporterNoCommentfmtDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range []string{"internal/commentfmt", "internal/spam"} {
		if strings.Contains(string(out), p) {
			t.Fatalf("package depends on %s", p)
		}
	}
}

// A reply to an existing max-depth comment attaches to its ancestor. That
// ancestor must be locked before the counts row, or the importer deadlocks with
// a moderator transition (which locks the comment, then counts).
func TestImporterLocksAttachBeforeCounts(t *testing.T) {
	f := newFixture(t)
	at := time.Now().UTC().AddDate(-1, 0, 0)
	var batch []ImportedComment
	for i := 1; i <= 5; i++ {
		parent := ""
		if i > 1 {
			parent = fmt.Sprint(i - 1)
		}
		batch = append(batch, importRow(f, fmt.Sprint(i), parent, "lock@example.test", StatusApproved, at.Add(time.Duration(i)*time.Minute)))
	}
	st, err := f.importer().Import(f.ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	leaf := st.IDs["5"]
	attach := st.IDs["4"] // depth 4 = MaxDepth-1: where a reply to the leaf attaches

	// Hold the counts row so the import blocks right after its first phase.
	blocker, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err := blocker.Exec(f.ctx, `SELECT 1 FROM hatamoto_comment_entities WHERE entity_id = $1 FOR UPDATE`, f.entity); err != nil {
		t.Fatal(err)
	}
	row := importRow(f, "new", "", "lock@example.test", StatusApproved, time.Now().UTC())
	row.ParentID = &leaf
	done := make(chan error, 1)
	go func() {
		stats, err := f.importer().Import(f.ctx, []ImportedComment{row})
		if err == nil && stats.Clamped != 1 {
			err = fmt.Errorf("clamped = %d, want 1", stats.Clamped)
		}
		done <- err
	}()
	f.waitForLockWait(1)

	// While the import waits for counts it must already hold the attach row:
	// a FOR UPDATE (what a moderator transition takes) cannot get it.
	probe, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, perr := probe.Exec(f.ctx, `SELECT 1 FROM hatamoto_comments WHERE id = $1 FOR UPDATE NOWAIT`, attach)
	probe.Rollback(f.ctx)
	if perr == nil {
		t.Error("attach row was not locked before the counts row")
	}
	blocker.Rollback(f.ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var parent uuid.UUID
	f.scalar(&parent, `SELECT parent_id FROM hatamoto_comments WHERE author_name = 'Old new'`)
	if parent != attach {
		t.Errorf("reply attached to %v, want %v", parent, attach)
	}
}
