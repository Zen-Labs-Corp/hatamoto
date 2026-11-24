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

func TestEditableComments(t *testing.T) {
	f := newFixture(t)
	fresh := f.storeAs("a@example.test", "fresh", "", StatusApproved)
	old := f.storeAs("a@example.test", "old", "", StatusApproved)
	f.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '16 minutes' WHERE id = $1`, old.ID)
	f.storeAs("a@example.test", "waiting", "", StatusPending)
	other := f.storeAs("b@example.test", "theirs", "", StatusApproved)

	got, err := f.svc.EditableComments(f.ctx, f.entity, *fresh.CommenterID)
	if err != nil || len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("EditableComments = %+v, %v; want only the fresh approved one", got, err)
	}
	if has, err := f.svc.HasEditable(f.ctx, *fresh.CommenterID); err != nil || !has {
		t.Errorf("HasEditable = %v, %v", has, err)
	}
	f.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '16 minutes' WHERE id = $1`, other.ID)
	if has, _ := f.svc.HasEditable(f.ctx, *other.CommenterID); has {
		t.Error("an expired window still counts as editable")
	}
}

func TestEditableWindowFollowsTypePolicy(t *testing.T) {
	f := newFixture(t)
	f.setComments(`{"policy":{"enabled":true,"edit_window_minutes":120}}`)
	c := f.storeAs("w@example.test", "late", "", StatusApproved)
	f.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '60 minutes' WHERE id = $1`, c.ID)
	got, err := f.svc.EditableComments(f.ctx, f.entity, *c.CommenterID)
	if err != nil || len(got) != 1 {
		t.Fatalf("EditableComments inside the type window = %+v, %v; want 1", got, err)
	}
	if has, err := f.svc.HasEditable(f.ctx, *c.CommenterID); err != nil || !has {
		t.Errorf("HasEditable inside the type window = %v, %v", has, err)
	}
	f.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '121 minutes' WHERE id = $1`, c.ID)
	if has, _ := f.svc.HasEditable(f.ctx, *c.CommenterID); has {
		t.Error("an expired type window still counts as editable")
	}
	f.setComments(`{"policy":{"enabled":false}}`)
	f.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '60 minutes' WHERE id = $1`, c.ID)
	if has, _ := f.svc.HasEditable(f.ctx, *c.CommenterID); has {
		t.Error("policy off must use the default 15 minute window")
	}
}
