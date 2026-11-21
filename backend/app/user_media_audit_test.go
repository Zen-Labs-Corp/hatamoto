//go:build integration

package app

import (
	"bytes"
	"context"
	"hatamoto.dev/core/internal/jsonx"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

type cliUserMediaAuditRow struct {
	Actor, Kind, Target string
	Detail              map[string]any
}

func cliUserMediaAuditRows(t *testing.T, db *database.DB, action string) []cliUserMediaAuditRow {
	t.Helper()
	rows, err := db.Pool().Query(context.Background(),
		`SELECT actor, actor_kind, target, detail FROM hatamoto_admin_audit WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []cliUserMediaAuditRow
	for rows.Next() {
		var r cliUserMediaAuditRow
		var raw []byte
		if err := rows.Scan(&r.Actor, &r.Kind, &r.Target, &raw); err != nil {
			t.Fatal(err)
		}
		if err := jsonx.Unmarshal(raw, &r.Detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestCliUserMediaAuditUserCreateAndResetPassword(t *testing.T) {
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: scratchDSN(t), MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	const pw = "correct horse battery staple"
	var out bytes.Buffer
	if err := runUserCreate(ctx, db, &userFlags{email: "ana@example.test", role: "editor", password: pw}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	rows := cliUserMediaAuditRows(t, db, "user.created")
	if len(rows) != 1 || rows[0].Kind != "cli" || rows[0].Target != "ana@example.test" ||
		rows[0].Detail["role"] != "editor" || rows[0].Detail["via"] != "cli" || len(rows[0].Detail) != 2 {
		t.Fatalf("user.created rows = %+v", rows)
	}

	const pw2 = "another long password here"
	if err := runUserResetPassword(ctx, db, &userFlags{email: "ana@example.test", passwordStdin: true}, strings.NewReader(pw2+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	rows = cliUserMediaAuditRows(t, db, "user.password_reset")
	if len(rows) != 1 || rows[0].Target != "ana@example.test" || rows[0].Detail["via"] != "cli" || len(rows[0].Detail) != 1 {
		t.Fatalf("user.password_reset rows = %+v", rows)
	}
	var all string
	if err := db.Pool().QueryRow(ctx, `SELECT coalesce(string_agg(detail::text || target, ' '), '') FROM hatamoto_admin_audit`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(all, pw) || strings.Contains(all, pw2) {
		t.Fatalf("a password reached the audit log: %s", all)
	}
}

func TestCliUserMediaAuditMediaAppliedOnly(t *testing.T) {
	ctx := context.Background()
	var got []string
	var details []map[string]any
	audit := func(_ context.Context, action, target string, detail map[string]any) {
		got = append(got, action+" "+target)
		details = append(details, detail)
	}

	store, err := media.NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, "logo.png", strings.NewReader("123")); err != nil {
		t.Fatal(err)
	}
	plan := mediaAdoptPlan{repo: &adoptRepoStub{keys: map[string]struct{}{}}, lister: store.(media.Lister), provider: "local", audit: audit}
	if err := mediaAdopt(ctx, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a dry run audited %v", got)
	}
	plan.apply = true
	if err := mediaAdopt(ctx, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "media.adopted media" || details[0]["count"] != 1 || len(details[0]) != 1 {
		t.Fatalf("adopt audit = %v %v", got, details)
	}
}

func TestCliUserMediaAuditSweepAppliedOnly(t *testing.T) {
	fake := s3test.NewServer(t)
	st, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "sites/a/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fake.PutAt("sites/a/2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3/orphan.png", []byte("123456"), nil, now.Add(-72*time.Hour))
	var got []string
	var detail map[string]any
	plan := mediaSweepPlan{
		mediaSweepFlags: mediaSweepFlags{olderThan: 24 * time.Hour},
		store:           st, bucket: "b", now: now,
		loadKeys: func(context.Context) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		audit: func(_ context.Context, action, target string, d map[string]any) {
			got = append(got, action+" "+target)
			detail = d
		},
	}
	if err := mediaSweep(context.Background(), plan, &bytes.Buffer{}); err != nil || len(got) != 0 {
		t.Fatalf("dry run: err=%v audited %v", err, got)
	}
	plan.apply = true
	if err := mediaSweep(context.Background(), plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "media.swept media" || detail["count"] != 1 || detail["bytes"] != int64(6) || len(detail) != 2 {
		t.Fatalf("sweep audit = %v %v", got, detail)
	}
}
