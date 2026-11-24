//go:build integration

package comment

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// age back-dates a comment's last status change.
func (f *fixture) age(id uuid.UUID, days int) {
	f.t.Helper()
	f.exec(`UPDATE hatamoto_comments SET updated_at = now() - make_interval(days => $2) WHERE id = $1`, id, days)
}

// published marks a seeded comment as once approved (approved_at set).
func (f *fixture) published(s seeded) seeded {
	f.t.Helper()
	f.exec(`UPDATE hatamoto_comments SET approved_at = now() WHERE id = $1`, s.id)
	return s
}

func (f *fixture) exists(id uuid.UUID) bool {
	f.t.Helper()
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comments WHERE id = $1`, id)
	return n == 1
}

func (f *fixture) seedAged(parent *seeded, status Status, days int) seeded {
	f.t.Helper()
	s := f.seedComment(parent, time.Now().UTC(), status)
	f.age(s.id, days)
	return s
}

func TestPurgeSpamTrash(t *testing.T) {
	f := newFixture(t)
	oldSpam := f.seedAged(nil, StatusSpam, 31)
	oldTrash := f.seedAged(nil, StatusTrash, 31)
	youngSpam := f.seedAged(nil, StatusSpam, 5)
	oldApproved := f.seedAged(nil, StatusApproved, 100)
	oldPending := f.seedAged(nil, StatusPending, 100)

	n, err := f.svc.PurgeSpamTrash(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want 2", n)
	}
	for name, c := range map[string]seeded{"old spam": oldSpam, "old trash": oldTrash} {
		if f.exists(c.id) {
			t.Errorf("%s survived", name)
		}
	}
	for name, c := range map[string]seeded{"young spam": youngSpam, "old approved": oldApproved, "old pending": oldPending} {
		if !f.exists(c.id) {
			t.Errorf("%s was purged", name)
		}
	}
	if n, err := f.svc.PurgeSpamTrash(f.ctx); err != nil || n != 0 {
		t.Errorf("second run = %d, %v, want 0", n, err)
	}
}

func TestPurgeSpamTrashBlanksParentWithReplies(t *testing.T) {
	f := newFixture(t)
	d := f.draft("old@example.test", "spammy")
	d.Status, d.NotifyReplies = StatusSpam, true
	parent := f.store(d)
	f.exec(`UPDATE hatamoto_comments SET ip_hash = 'iph', net_hash = 'nh', user_agent = 'ua', body_hash = 'bh' WHERE id = $1`, parent.ID)
	f.age(parent.ID, 40)
	reply := f.seedAged(&seeded{id: parent.ID, path: parent.Path}, StatusApproved, 1)

	n, err := f.svc.PurgeSpamTrash(f.ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v, want 1 blanked", n, err)
	}
	if !f.exists(parent.ID) || !f.exists(reply.id) {
		t.Fatal("parent or reply was deleted")
	}
	var name, body, hashes string
	var notify bool
	f.scalar(&name, `SELECT author_name FROM hatamoto_comments WHERE id = $1`, parent.ID)
	f.scalar(&body, `SELECT body_raw FROM hatamoto_comments WHERE id = $1`, parent.ID)
	f.scalar(&hashes, `SELECT ip_hash || net_hash || user_agent || body_hash FROM hatamoto_comments WHERE id = $1`, parent.ID)
	f.scalar(&notify, `SELECT notify_replies FROM hatamoto_comments WHERE id = $1`, parent.ID)
	if name != "" || body != deletedBody || hashes != "" || notify {
		t.Errorf("blanked row: name %q body %q hashes %q notify %v", name, body, hashes, notify)
	}
	var stamp time.Time
	f.scalar(&stamp, `SELECT updated_at FROM hatamoto_comments WHERE id = $1`, parent.ID)
	if time.Since(stamp) < 39*24*time.Hour {
		t.Errorf("blanking bumped updated_at to %v", stamp)
	}
	if n, err := f.svc.PurgeSpamTrash(f.ctx); err != nil || n != 0 {
		t.Errorf("second run = %d, %v, want 0 (idempotent)", n, err)
	}
}

func TestPurgeSpamTrashLeafBeforeParent(t *testing.T) {
	f := newFixture(t)
	parent := f.seedAged(nil, StatusSpam, 60)
	child := f.seedAged(&parent, StatusSpam, 59)
	grandchild := f.seedAged(&child, StatusTrash, 45)

	n, err := f.svc.PurgeSpamTrash(f.ctx)
	if err != nil || n != 3 {
		t.Fatalf("purge = %d, %v, want 3", n, err)
	}
	for _, c := range []seeded{parent, child, grandchild} {
		if f.exists(c.id) {
			t.Errorf("%s survived", c.id)
		}
	}
}

func TestPurgeSpamTrashHonoursPurgeDays(t *testing.T) {
	f := newFixture(t)
	set := Defaults()
	set.PurgeDays = 7
	if err := f.svc.SaveSettings(f.ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	eight := f.seedAged(nil, StatusSpam, 8)
	six := f.seedAged(nil, StatusTrash, 6)
	if _, err := f.svc.PurgeSpamTrash(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.exists(eight.id) || !f.exists(six.id) {
		t.Errorf("8 d old exists = %v (want false), 6 d old exists = %v (want true)", f.exists(eight.id), f.exists(six.id))
	}
}

// A comment restored while the purge waits on its row lock must survive: the
// delete re-checks status and age on the row itself, not only in the subquery.
func TestPurgeSpamTrashSparesCommentRestoredMidPurge(t *testing.T) {
	f := newFixture(t)
	c := f.seedAged(nil, StatusTrash, 31)

	tx, err := f.db.Pool().Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(f.ctx, `UPDATE hatamoto_comments SET status = 'approved', updated_at = now() WHERE id = $1`, c.id); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := f.svc.PurgeSpamTrash(f.ctx)
		done <- err
	}()
	// Wait until the purge is blocked on the restored row's lock.
	for i := 0; ; i++ {
		var waiting int
		f.scalar(&waiting, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE 'DELETE FROM hatamoto_comments%'`)
		if waiting > 0 {
			break
		}
		if i > 200 {
			t.Fatal("purge never blocked on the row")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !f.exists(c.id) {
		t.Error("a comment restored mid-purge was deleted")
	}
}
