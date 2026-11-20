//go:build integration

package comment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *fixture) storeAs(email, body, hash string, st Status) Comment {
	f.t.Helper()
	d := f.draft(email, body)
	d.Status, d.BodyHash = st, hash
	if st == StatusTrash { // Store refuses trash; trash it straight in the table
		d.Status = StatusPending
	}
	c := f.store(d)
	if st == StatusTrash {
		f.exec(`UPDATE hatamoto_comments SET status = 'trash' WHERE id = $1`, c.ID)
	}
	return c
}

func TestGet(t *testing.T) {
	f := newFixture(t)
	c := f.storeAs("a@example.test", "hello", "", StatusPending)
	got, err := f.svc.Get(f.ctx, c.ID)
	if err != nil || got.ID != c.ID || got.Status != StatusPending {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := f.svc.Get(f.ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestHasNonPublic(t *testing.T) {
	f := newFixture(t)
	ok := f.storeAs("a@example.test", "one", "", StatusApproved)
	f.storeAs("a@example.test", "two", "", StatusTrash)
	if has, err := f.svc.HasNonPublic(f.ctx, *ok.CommenterID); err != nil || has {
		t.Fatalf("approved+trash only: has = %v, %v", has, err)
	}
	f.storeAs("a@example.test", "three", "", StatusPending)
	if has, err := f.svc.HasNonPublic(f.ctx, *ok.CommenterID); err != nil || !has {
		t.Fatalf("with pending: has = %v, %v", has, err)
	}
	other := f.storeAs("b@example.test", "four", "", StatusApproved)
	if has, _ := f.svc.HasNonPublic(f.ctx, *other.CommenterID); has {
		t.Error("another commenter's pending leaked")
	}
}

func TestViewerComments(t *testing.T) {
	f := newFixture(t)
	mine := []Comment{
		f.storeAs("a@example.test", "draft", "", StatusDraft),
		f.storeAs("a@example.test", "pending", "", StatusPending),
		f.storeAs("a@example.test", "spam", "", StatusSpam),
	}
	f.storeAs("a@example.test", "approved", "", StatusApproved)
	f.storeAs("a@example.test", "trash", "", StatusTrash)
	f.storeAs("b@example.test", "theirs", "", StatusPending)
	d := f.draft("a@example.test", "elsewhere")
	d.EntityID, d.Status = f.newEntity("other"), StatusPending
	f.store(d)

	got, err := f.svc.ViewerComments(f.ctx, f.entity, *mine[0].CommenterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d comments, want 3: %+v", len(got), got)
	}
	for i, tc := range got {
		if tc.ID != mine[i].ID {
			t.Errorf("comment %d = %v, want %v (oldest first)", i, tc.ID, mine[i].ID)
		}
	}
}

func TestDuplicateSince(t *testing.T) {
	f := newFixture(t)
	f.storeAs("a@example.test", "same", "h1", StatusPending)
	f.storeAs("b@example.test", "same", "h1", StatusApproved)
	f.storeAs("c@example.test", "same", "h1", StatusTrash)
	hour := time.Hour
	if n, err := f.svc.DuplicateSince(f.ctx, "h1", time.Now().Add(-hour)); err != nil || n != 2 {
		t.Errorf("recent = %d, %v; want 2", n, err)
	}
	if n, _ := f.svc.DuplicateSince(f.ctx, "h1", time.Now().Add(hour)); n != 0 {
		t.Errorf("since after them = %d, want 0", n)
	}
	if n, _ := f.svc.DuplicateSince(f.ctx, "", time.Now().Add(-hour)); n != 0 {
		t.Errorf("empty hash = %d, want 0", n)
	}
}
