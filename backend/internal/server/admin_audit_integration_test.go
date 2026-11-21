//go:build integration

package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/admin"
)

// The audit reader's SQL against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run AdminAuditLog
func TestAdminAuditLogRecentIntegration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchMigratedDB(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := adminAuditLog{pool: pool}

	if rows, total, err := log.Recent(ctx, 10, 0); err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("empty = %v %d %v", rows, total, err)
	}
	for _, a := range []string{"first", "second", "third"} {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_admin_audit (actor, action, detail) VALUES ('o@example.com', $1, '{"name":"Home"}')`, a); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := log.Recent(ctx, 2, 0)
	if err != nil || total != 3 || len(rows) != 2 || rows[0].Action != "third" || rows[1].Action != "second" {
		t.Fatalf("page 1 = %+v total=%d err=%v", rows, total, err)
	}
	if rows[0].Detail["name"] != "Home" || rows[0].Target != "" || rows[0].Actor != "o@example.com" {
		t.Errorf("row = %+v", rows[0])
	}
	if rows, _, _ := log.Recent(ctx, 2, 2); len(rows) != 1 || rows[0].Action != "first" {
		t.Errorf("page 2 = %+v", rows)
	}
	if rows, err := log.Latest(ctx, 2); err != nil || len(rows) != 2 || rows[0].Action != "third" {
		t.Errorf("latest = %+v %v", rows, err)
	}
	var _ admin.AuditReader = log
}

// seedAuditSearch inserts the rows the Search tests read. The three rows at tieAt
// span both tables so the keyset order has to break ties by source and id.
func seedAuditSearch(t *testing.T, pool *pgxpool.Pool) (keyID string) {
	t.Helper()
	ctx := context.Background()
	keyID = "0b0c5b7e-5d7e-4a53-8b8c-2f1d6f0b9a11"
	userID := "7a1f3c52-9d1e-4a67-b1c4-3e8d2f5a6b70"
	base := time.Date(2026, 10, 2, 8, 0, 0, 123456000, time.UTC)
	admin := func(at time.Time, actorID any, actor, kind, action, target, detail string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_admin_audit (at, actor_id, actor, actor_kind, action, target, detail) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`,
			at, actorID, actor, kind, action, target, detail); err != nil {
			t.Fatal(err)
		}
	}
	mcp := func(at time.Time, ok bool, errText, tool, args string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_mcp_audit (at, api_key_id, api_key_name, tool, ok, error, args) VALUES ($1,$2,'ci-bot',$3,$4,$5,$6::jsonb)`,
			at, keyID, tool, ok, errText, args); err != nil {
			t.Fatal(err)
		}
	}
	tie := base.Add(-time.Hour)
	admin(base, userID, "jane@example.com", "user", "entity.publish", "pricing", `{"name":"Pricing"}`)
	admin(base.Add(-30*time.Minute), nil, "cli", "", "backup.create", "", `{}`)
	admin(tie, nil, "api", "api_key", "entity.update", "blog-x", `{}`)
	admin(tie, nil, "scheduler", "system", "entity.publish", "launch", `{}`)
	mcp(tie, true, "", "entities_update", `{"slug":"blog-x"}`)
	mcp(base.Add(-2*time.Hour), false, "forbidden: the API key lacks the write scope", "entities_delete", `{"id":"e-9"}`)
	mcp(base.Add(-3*time.Hour), false, "boom", "entities_update", `{"note":"50% off_sale"}`)
	admin(base.Add(-4*time.Hour), userID, "jane@example.com", "user", "settings.save", "", `{"note":"50% off_sale"}`)
	return keyID
}

func TestAdminAuditLogSearchIntegration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchMigratedDB(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := adminAuditLog{pool: pool}
	keyID := seedAuditSearch(t, pool)
	const userID = "7a1f3c52-9d1e-4a67-b1c4-3e8d2f5a6b70"

	search := func(q admin.AuditQuery) admin.AuditPage {
		t.Helper()
		p, err := log.Search(ctx, q)
		if err != nil {
			t.Fatalf("Search(%+v): %v", q, err)
		}
		return p
	}
	actions := func(p admin.AuditPage) string {
		var out []string
		for _, r := range p.Rows {
			out = append(out, r.Src+":"+r.Action)
		}
		return strings.Join(out, ",")
	}

	all := search(admin.AuditQuery{Limit: 50})
	if len(all.Rows) != 8 || all.Next != nil {
		t.Fatalf("all = %d rows next=%v", len(all.Rows), all.Next)
	}
	// Ties at one timestamp: agent (m) before admin (a), higher id first.
	want := "a:entity.publish,a:backup.create,m:entities_update,a:entity.publish,a:entity.update,m:entities_delete,m:entities_update,a:settings.save"
	if got := actions(all); got != want {
		t.Errorf("order = %s\nwant    %s", got, want)
	}

	for name, c := range map[string]struct {
		q    admin.AuditQuery
		want string
	}{
		"source admin":  {admin.AuditQuery{Source: "admin"}, "a:entity.publish,a:settings.save"},
		"source api":    {admin.AuditQuery{Source: "api"}, "a:entity.update"},
		"source cli":    {admin.AuditQuery{Source: "cli"}, "a:backup.create"},
		"source system": {admin.AuditQuery{Source: "system"}, "a:entity.publish"},
		"source agent":  {admin.AuditQuery{Source: "agent"}, "m:entities_update,m:entities_delete,m:entities_update"},
		"action admin":  {admin.AuditQuery{Action: "entity.publish"}, "a:entity.publish,a:entity.publish"},
		"action tool":   {admin.AuditQuery{Action: "entities_delete"}, "m:entities_delete"},
		"actor user":    {admin.AuditQuery{Actor: "u:" + userID}, "a:entity.publish,a:settings.save"},
		"actor name":    {admin.AuditQuery{Actor: "n:cli"}, "a:backup.create"},
		"actor key":     {admin.AuditQuery{Actor: "k:" + keyID}, "m:entities_update,m:entities_delete,m:entities_update"},
		"actor invalid": {admin.AuditQuery{Actor: "u:nope", Source: "api"}, "a:entity.update"},
		"agent + user":  {admin.AuditQuery{Source: "agent", Actor: "u:" + userID}, ""},
		"text actor":    {admin.AuditQuery{Q: "JANE"}, "a:entity.publish,a:settings.save"},
		"text detail":   {admin.AuditQuery{Q: "pricing"}, "a:entity.publish"},
		"text args":     {admin.AuditQuery{Q: "e-9"}, "m:entities_delete"},
		"text error":    {admin.AuditQuery{Q: "lacks the write"}, "m:entities_delete"},
		"text percent":  {admin.AuditQuery{Q: "50% off"}, "m:entities_update,a:settings.save"},
		"text under":    {admin.AuditQuery{Q: "off_sale"}, "m:entities_update,a:settings.save"},
		"text literal":  {admin.AuditQuery{Q: "50%_"}, ""},
		"text wildcard": {admin.AuditQuery{Q: "%"}, "m:entities_update,a:settings.save"},
	} {
		c.q.Limit = 50
		if got := actions(search(c.q)); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}

	day := func(h int) time.Time { return time.Date(2026, 10, 2, h, 0, 0, 0, time.UTC) }
	if p := search(admin.AuditQuery{From: day(6), To: day(7), Limit: 50}); actions(p) != "m:entities_delete" {
		t.Errorf("window = %s", actions(p))
	}
	if p := search(admin.AuditQuery{From: day(5), To: day(6), Limit: 50}); actions(p) != "m:entities_update" {
		t.Errorf("window = %s", actions(p))
	}

	// Agent rows: key name as actor, tool as action, target from args, no args.
	rows := search(admin.AuditQuery{Source: "agent", Limit: 50}).Rows
	if r := rows[0]; r.Actor != "ci-bot" || r.Source != "agent" || r.Result != "ok" || r.Target != "blog-x" || r.Detail != nil && len(r.Detail) != 0 {
		t.Errorf("agent ok row = %+v", r)
	}
	if r := rows[1]; r.Result != "denied" || r.Error == "" || r.Target != "e-9" {
		t.Errorf("refused row = %+v", r)
	}
	if r := rows[2]; r.Result != "error" || r.Error != "boom" {
		t.Errorf("failed row = %+v", r)
	}
	if r := all.Rows[0]; r.Source != "admin" || r.Result != "ok" || r.Detail["name"] != "Pricing" || r.Target != "pricing" {
		t.Errorf("admin row = %+v", r)
	}

	// Walking two rows at a time returns every row once, in order.
	var walked []string
	q := admin.AuditQuery{Limit: 2}
	for i := 0; ; i++ {
		if i > 10 {
			t.Fatal("paging does not end")
		}
		p := search(q)
		for _, r := range p.Rows {
			walked = append(walked, r.Src+":"+r.Action)
		}
		if p.Next == nil {
			break
		}
		q.Before = p.Next
	}
	if got := strings.Join(walked, ","); got != want {
		t.Errorf("walk = %s\nwant   %s", got, want)
	}

	fx, err := log.Facets(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, a := range fx.Actors {
		labels[a.Value] = a.Label
	}
	if labels["u:"+userID] != "jane@example.com" || labels["k:"+keyID] != "ci-bot" || labels["n:cli"] != "cli" {
		t.Errorf("actors = %+v", fx.Actors)
	}
	if got := strings.Join(fx.Actions, ","); !strings.Contains(got, "entities_delete") || !strings.Contains(got, "backup.create") {
		t.Errorf("actions = %v", fx.Actions)
	}
	if fx, err := log.Facets(ctx, day(5), day(6)); err != nil || len(fx.Actions) != 1 || fx.Actions[0] != "entities_update" {
		t.Errorf("window facets = %+v %v", fx, err)
	}
}
