//go:build integration

package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// The repository refuses to delete or demote the last owner, also when two
// owners try to remove each other at the same moment.
func TestLastOwnerGuard_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	repo := user.NewPostgresRepository(db)
	mk := func(email string, role user.Role) *user.User {
		t.Helper()
		u, err := repo.Create(ctx, &user.CreateUserInput{Email: email, Role: role}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	editor := user.RoleEditor

	a := mk("a@example.test", user.RoleOwner)
	if err := repo.Delete(ctx, a.ID); !errors.Is(err, user.ErrLastOwner) {
		t.Fatalf("deleting the only owner: %v", err)
	}
	if _, err := repo.Update(ctx, a.ID, &user.UpdateUserInput{Role: &editor}); !errors.Is(err, user.ErrLastOwner) {
		t.Fatalf("demoting the only owner: %v", err)
	}
	// Renaming the only owner is fine, and so is deleting an editor.
	name := "Ada"
	if u, err := repo.Update(ctx, a.ID, &user.UpdateUserInput{Name: &name}); err != nil || u.Role != user.RoleOwner {
		t.Fatalf("rename: %v", err)
	}
	e := mk("e@example.test", user.RoleEditor)
	if err := repo.Delete(ctx, e.ID); err != nil {
		t.Fatalf("deleting an editor: %v", err)
	}
	if err := repo.Delete(ctx, e.ID); !errors.Is(err, user.ErrUserNotFound) {
		t.Fatalf("deleting a missing user: %v", err)
	}

	// Two owners: each tries to demote the other at once; exactly one wins.
	b := mk("b@example.test", user.RoleOwner)
	var wins, refused atomic.Int32
	var wg sync.WaitGroup
	for _, id := range []user.User{*a, *b} {
		wg.Add(1)
		go func(id user.User) {
			defer wg.Done()
			_, err := repo.Update(ctx, id.ID, &user.UpdateUserInput{Role: &editor})
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, user.ErrLastOwner):
				refused.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(id)
	}
	wg.Wait()
	if wins.Load() != 1 || refused.Load() != 1 {
		t.Fatalf("mutual demotion: %d succeeded, %d refused; want 1 and 1", wins.Load(), refused.Load())
	}
	var owners int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_users WHERE role = 'owner'`).Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("%d owners left (%v), want exactly 1", owners, err)
	}
}
