//go:build integration

package user

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func accessRig(t *testing.T) (*PostgresRepository, *AccessStore) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewPostgresRepository(db), NewAccessStore(db)
}

func accessUser(t *testing.T, r *PostgresRepository, role Role, end *time.Time) *User {
	t.Helper()
	ctx := database.WithSuperadmin(context.Background())
	email := "acc-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	u, err := r.Create(ctx, &CreateUserInput{Email: email, Role: role, Name: "A", AccessEndsAt: end}, "x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Direct delete: the owner guard would refuse the last owner of a test run.
		c, _ := r.db.GetConn(ctx)
		defer c.Release()
		_, _ = c.Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, u.ID)
	})
	return u
}

func TestAccessUpdateSetClearJSON(t *testing.T) {
	r, _ := accessRig(t)
	ctx := database.WithSuperadmin(context.Background())
	u := accessUser(t, r, RoleEditor, nil)
	end := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Microsecond)

	got, err := r.Update(ctx, u.ID, &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true, At: &end}})
	if err != nil || got.AccessEndsAt == nil || !got.AccessEndsAt.Equal(end) {
		t.Fatalf("set: %+v %v", got, err)
	}
	// Absent leaves it alone.
	name := "B"
	if got, err = r.Update(ctx, u.ID, &UpdateUserInput{Name: &name}); err != nil || got.AccessEndsAt == nil {
		t.Fatalf("absent: %+v %v", got, err)
	}
	back, err := r.GetByID(ctx, u.ID)
	if err != nil || back.AccessEndsAt == nil || !back.AccessEndsAt.Equal(end) {
		t.Fatalf("GetByID: %+v %v", back, err)
	}
	// Explicit clear.
	if got, err = r.Update(ctx, u.ID, &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true}}); err != nil || got.AccessEndsAt != nil {
		t.Fatalf("clear: %+v %v", got, err)
	}
}

func TestAccessOwnerGuard(t *testing.T) {
	r, _ := accessRig(t)
	ctx := database.WithSuperadmin(context.Background())
	// Park every existing owner behind an end date for this test, then restore.
	c, err := r.db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var restore []uuid.UUID
	rows, _ := c.Query(ctx, `SELECT id FROM hatamoto_users WHERE role = 'owner'`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		restore = append(restore, id)
	}
	rows.Close()
	_, _ = c.Exec(ctx, `UPDATE hatamoto_users SET role = 'editor' WHERE id = ANY($1)`, restore)
	c.Release()
	t.Cleanup(func() {
		c, _ := r.db.GetConn(ctx)
		defer c.Release()
		_, _ = c.Exec(ctx, `UPDATE hatamoto_users SET role = 'owner' WHERE id = ANY($1)`, restore)
	})

	a := accessUser(t, r, RoleOwner, nil)
	b := accessUser(t, r, RoleOwner, nil)
	end := time.Now().Add(48 * time.Hour)
	in := &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true, At: &end}}

	if _, err := r.Update(ctx, a.ID, in); err != nil {
		t.Fatalf("first owner dated: %v", err)
	}
	if _, err := r.Update(ctx, b.ID, in); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("second owner dated: %v", err)
	}
	// b is the last open-ended owner: neither demote nor delete.
	editor := RoleEditor
	if _, err := r.Update(ctx, b.ID, &UpdateUserInput{Role: &editor}); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote: %v", err)
	}
	if err := r.Delete(ctx, b.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("delete: %v", err)
	}
	// Creating an owner with an end date needs an open-ended owner too.
	made, err := r.Create(ctx, &CreateUserInput{Email: "dated-" + uuid.NewString()[:8] + "@example.test", Role: RoleOwner, AccessEndsAt: &end}, "x")
	if err != nil {
		t.Fatalf("create dated owner with an open owner: %v", err)
	}
	t.Cleanup(func() { _, _ = r.db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, made.ID) })
	// The dated owner can still be deleted.
	if err := r.Delete(ctx, a.ID); err != nil {
		t.Fatalf("delete dated owner: %v", err)
	}
}

func TestAccessClaims(t *testing.T) {
	r, s := accessRig(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	past := now.Add(-time.Hour)
	soon := now.Add(24 * time.Hour)
	ended := accessUser(t, r, RoleEditor, &past)
	ending := accessUser(t, r, RoleEditor, &soon)

	has := func(us []AccessUser, id uuid.UUID) bool {
		for _, u := range us {
			if u.ID == id {
				return true
			}
		}
		return false
	}
	first, err := s.ClaimEnded(ctx, now)
	if err != nil || !has(first, ended.ID) || has(first, ending.ID) {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	second, _ := s.ClaimEnded(ctx, now)
	if has(second, ended.ID) {
		t.Fatal("second claim returned the user again")
	}
	if err := s.ReleaseEnded(ctx, ended.ID, past); err != nil {
		t.Fatal(err)
	}
	if third, _ := s.ClaimEnded(ctx, now); !has(third, ended.ID) {
		t.Fatal("released claim not retried")
	}

	rem, err := s.ClaimReminders(ctx, now, 72*time.Hour)
	if err != nil || !has(rem, ending.ID) || has(rem, ended.ID) {
		t.Fatalf("reminder claim: %+v %v", rem, err)
	}
	if again, _ := s.ClaimReminders(ctx, now, 72*time.Hour); has(again, ending.ID) {
		t.Fatal("reminder claimed twice")
	}
	// A changed date claims again.
	later := now.Add(30 * time.Hour).Truncate(time.Microsecond)
	if _, err := r.Update(database.WithSuperadmin(ctx), ending.ID, &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true, At: &later}}); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.ClaimReminders(ctx, now, 72*time.Hour); !has(again, ending.ID) {
		t.Fatal("changed date not claimed again")
	}
}

func TestAccessClaimEndedConcurrent(t *testing.T) {
	r, s := accessRig(t)
	past := time.Now().Add(-time.Hour)
	u := accessUser(t, r, RoleEditor, &past)
	var wg sync.WaitGroup
	var mu sync.Mutex
	hits := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			us, err := s.ClaimEnded(context.Background(), time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			for _, x := range us {
				if x.ID == u.ID {
					mu.Lock()
					hits++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if hits != 1 {
		t.Fatalf("claimed %d times", hits)
	}
}

func TestAccessActiveOwnerEmails(t *testing.T) {
	r, s := accessRig(t)
	ctx := context.Background()
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(24*time.Hour)
	open := accessUser(t, r, RoleOwner, nil)
	soon := accessUser(t, r, RoleOwner, &future)
	gone := accessUser(t, r, RoleOwner, &past)
	subject := accessUser(t, r, RoleOwner, nil)
	editor := accessUser(t, r, RoleEditor, nil)

	got, err := s.ActiveOwnerEmails(ctx, now, subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	has := func(e string) bool {
		for _, x := range got {
			if x == e {
				return true
			}
		}
		return false
	}
	if !has(open.Email) || !has(soon.Email) {
		t.Errorf("open owners missing: %v", got)
	}
	if has(gone.Email) || has(subject.Email) || has(editor.Email) {
		t.Errorf("ended owner, the user or an editor listed: %v", got)
	}
}

// A user whose end has passed had their sessions and keys cut off, even when
// the hourly job did not run: extending the date must not bring them back.
func TestAccessExtendAfterEndDoesNotRevive(t *testing.T) {
	r, _ := accessRig(t)
	ctx := database.WithSuperadmin(context.Background())
	past := time.Now().Add(-time.Minute)
	u := accessUser(t, r, RoleEditor, &past)

	exec := func(q string, args ...any) {
		t.Helper()
		c, err := r.db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Release()
		if _, err := c.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) (n int) {
		t.Helper()
		c, err := r.db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Release()
		if err := c.QueryRow(ctx, q, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec(`INSERT INTO hatamoto_sessions (user_id, kind, expires_at, session_token_hash) VALUES ($1, 'admin', NOW() + interval '1 day', $2)`, u.ID, strings.Repeat("a", 64))
	exec(`INSERT INTO hatamoto_api_keys (name, key_prefix, key_hash, created_by, scopes) VALUES ('k', 'nxk_test', $2, $1, '{}')`, u.ID, strings.Repeat("b", 64))
	t.Cleanup(func() {
		exec(`DELETE FROM hatamoto_api_keys WHERE created_by = $1`, u.ID)
		exec(`DELETE FROM hatamoto_sessions WHERE user_id = $1`, u.ID)
	})

	end := time.Now().Add(48 * time.Hour)
	if _, err := r.Update(ctx, u.ID, &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true, At: &end}}); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1`); n != 0 {
		t.Errorf("%d sessions survived the extension", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_api_keys WHERE created_by = $1 AND revoked_at IS NULL AND is_active`); n != 0 {
		t.Errorf("%d keys survived the extension", n)
	}

	// A user who has not ended keeps theirs when the date moves.
	live := accessUser(t, r, RoleEditor, &end)
	exec(`INSERT INTO hatamoto_sessions (user_id, kind, expires_at, session_token_hash) VALUES ($1, 'admin', NOW() + interval '1 day', $2)`, live.ID, strings.Repeat("c", 64))
	t.Cleanup(func() { exec(`DELETE FROM hatamoto_sessions WHERE user_id = $1`, live.ID) })
	later := end.Add(24 * time.Hour)
	if _, err := r.Update(ctx, live.ID, &UpdateUserInput{AccessEndsAt: AccessEnd{Set: true, At: &later}}); err != nil {
		t.Fatal(err)
	}
	c, _ := r.db.GetConn(ctx)
	defer c.Release()
	var n int
	_ = c.QueryRow(ctx, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1`, live.ID).Scan(&n)
	if n != 1 {
		t.Errorf("a live user's session was revoked: %d", n)
	}
}
