//go:build integration

package form_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/form"
)

func seedSubmission(t *testing.T, db *database.DB, formID uuid.UUID, age time.Duration, status, ip string) {
	t.Helper()
	ctx := database.WithSuperadmin(context.Background())
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `INSERT INTO hatamoto_form_submissions (form_id, data, metadata, status, created_at)
		VALUES ($1, '{}', jsonb_build_object('ip_address', $2::text), $3, now() - make_interval(secs => $4))`,
		formID, ip, status, age.Seconds())
	if err != nil {
		t.Fatal(err)
	}
}

func TestPurgeExpiredSubmissions(t *testing.T) {
	ctx := database.WithSuperadmin(context.Background())
	db := testDB(t)
	repo := form.NewPostgresRepository(db)
	day := 24 * time.Hour

	mk := func(name string, days int) *form.Form {
		f, err := repo.CreateForm(ctx, &form.CreateFormInput{Name: name, Slug: name, Fields: []form.FormField{}, Settings: form.FormSettings{RetentionDays: days}})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	kept, purged := mk("never", 0), mk("thirty", 30)
	for _, f := range []*form.Form{kept, purged} {
		seedSubmission(t, db, f.ID, 40*day, "new", "")
		seedSubmission(t, db, f.ID, 40*day, "spam", "")
		seedSubmission(t, db, f.ID, 10*day, "new", "")
	}

	n, err := repo.PurgeExpiredSubmissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want 2 (old new + old spam)", n)
	}
	if c, _ := repo.GetSubmissionCount(ctx, purged.ID); c != 1 {
		t.Errorf("30-day form has %d submissions, want 1", c)
	}
	if c, _ := repo.GetSubmissionCount(ctx, kept.ID); c != 3 {
		t.Errorf("form without retention has %d submissions, want 3", c)
	}
}

func TestCountRecentSubmissionsByIP(t *testing.T) {
	ctx := database.WithSuperadmin(context.Background())
	db := testDB(t)
	repo := form.NewPostgresRepository(db)
	f, err := repo.CreateForm(ctx, &form.CreateFormInput{Name: "Contact", Fields: []form.FormField{}})
	if err != nil {
		t.Fatal(err)
	}
	seedSubmission(t, db, f.ID, 10*time.Minute, "new", "203.0.113.5")
	seedSubmission(t, db, f.ID, 20*time.Minute, "spam", "203.0.113.5")
	seedSubmission(t, db, f.ID, 2*time.Hour, "new", "203.0.113.5")
	seedSubmission(t, db, f.ID, 5*time.Minute, "new", "203.0.113.9")
	seedSubmission(t, db, f.ID, 5*time.Minute, "new", "")

	since := time.Now().Add(-time.Hour)
	for ip, want := range map[string]int{"203.0.113.5": 2, "203.0.113.9": 1, "198.51.100.1": 0, "": 0} {
		got, err := repo.CountRecentSubmissionsByIP(ctx, f.ID, ip, since)
		if err != nil || got != want {
			t.Errorf("count(%q) = %d, %v; want %d", ip, got, err, want)
		}
	}
}

func TestListSubmissionsSearch(t *testing.T) {
	ctx := database.WithSuperadmin(context.Background())
	db := testDB(t)
	repo := form.NewPostgresRepository(db)

	mk := func(slug string) *form.Form {
		f, err := repo.CreateForm(ctx, &form.CreateFormInput{Name: slug, Slug: slug, Fields: []form.FormField{}})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	seed := func(f *form.Form, status, data string) {
		t.Helper()
		conn, err := db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `INSERT INTO hatamoto_form_submissions (form_id, data, metadata, status)
			VALUES ($1, $2::jsonb, '{}', $3)`, f.ID, data, status); err != nil {
			t.Fatal(err)
		}
	}
	a, other := mk("search-a"), mk("search-b")
	seed(a, "new", `{"name":"Ada Lovelace","msg":"50%_off today"}`)
	seed(a, "read", `{"name":"Grace Hopper","msg":"500 off"}`)
	seed(a, "spam", `{"name":"Alan Turing","msg":"back\\slash"}`)
	seed(other, "new", `{"name":"Ada Other","msg":"50%_off"}`)

	list := func(p form.ListSubmissionsParams) ([]*form.FormSubmission, int) {
		t.Helper()
		rows, total, err := repo.ListSubmissions(ctx, a.ID, &p)
		if err != nil {
			t.Fatal(err)
		}
		return rows, total
	}
	for _, tc := range []struct {
		name string
		p    form.ListSubmissionsParams
		want int
	}{
		{"no search", form.ListSubmissionsParams{}, 3},
		{"blank search", form.ListSubmissionsParams{Search: "   "}, 3},
		{"case-insensitive", form.ListSubmissionsParams{Search: "LOVELACE"}, 1},
		{"percent and underscore are literal", form.ListSubmissionsParams{Search: "50%_off"}, 1},
		{"lone percent is literal", form.ListSubmissionsParams{Search: "%"}, 1},
		{"lone underscore is literal", form.ListSubmissionsParams{Search: "_"}, 1},
		{"backslash is literal", form.ListSubmissionsParams{Search: `\`}, 1},
		{"search and status", form.ListSubmissionsParams{Search: "a", Status: "read"}, 1},
		{"search excludes other status", form.ListSubmissionsParams{Search: "Lovelace", Status: "spam"}, 0},
		{"other form never matches", form.ListSubmissionsParams{Search: "Other"}, 0},
		{"quote is data, not SQL", form.ListSubmissionsParams{Search: `'); DROP TABLE x;--`}, 0},
	} {
		rows, total := list(tc.p)
		if len(rows) != tc.want || total != tc.want {
			t.Errorf("%s: got %d rows, total %d; want %d", tc.name, len(rows), total, tc.want)
		}
	}
}
