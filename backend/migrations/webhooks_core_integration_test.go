//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

const (
	webhooksToPluginVersion = 202610201100
	webhooksCoreVersion     = 202610251000
)

func seedHandover(t *testing.T, db *sql.DB, id, value string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO hatamoto_plugin_handover (plugin, key, value) VALUES ('webhooks', 'legacy:sub:' || $1::text, $2::jsonb)`, id, value); err != nil {
		t.Fatal(err)
	}
}

func TestWebhooksCore_CopiesHandoverAndKeepsTable(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, webhooksToPluginVersion); err != nil {
		t.Fatalf("up to handover: %v", err)
	}
	seedHandover(t, db, "11111111-1111-4111-8111-111111111111", `{
	  "id":"11111111-1111-4111-8111-111111111111","name":"  old  ","url":"http://example.com/h","secret":"",
	  "events":["entity.created","citation.received","entity.created","form.submitted"],
	  "headers":{"X-Team":"docs","Content-Type":"text/evil","User-Agent":"x","Bad Name":"v","X-Nl":"a\r\nb","host":"h"},
	  "active":true,"max_retries":99,"retry_delay_seconds":999999,"timeout_seconds":120}`)
	seedHandover(t, db, "22222222-2222-4222-8222-222222222222", `{
	  "id":"22222222-2222-4222-8222-222222222222","name":"","url":"https://u:p@example.com/x","secret":"whsec_abc",
	  "events":["entity.created"],"headers":{},"active":true,"max_retries":-3,"retry_delay_seconds":-1,"timeout_seconds":-5}`)
	seedHandover(t, db, "33333333-3333-4333-8333-333333333333", `{
	  "id":"33333333-3333-4333-8333-333333333333","name":"noevents","url":"https://ok.example/h","secret":"whsec_xyz",
	  "events":["lead.captured"],"headers":{},"active":true,"max_retries":3,"retry_delay_seconds":60,"timeout_seconds":5}`)
	seedHandover(t, db, "44444444-4444-4444-8444-444444444444", `{
	  "id":"44444444-4444-4444-8444-444444444444","name":"fine","url":"https://ok.example/h","secret":"whsec_keep",
	  "events":["import.completed","entity.deleted"],"headers":{"X-A":"1"},"active":false,"max_retries":3,"retry_delay_seconds":60,"timeout_seconds":5}`)
	// Another plugin's row and a row without a url must be left alone.
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_plugin_handover (plugin, key, value) VALUES ('other', 'legacy:sub:9', '{"id":"9","url":"https://x"}')`); err != nil {
		t.Fatal(err)
	}
	seedHandover(t, db, "55555555-5555-4555-8555-555555555555", `{"id":"55555555-5555-4555-8555-555555555555","url":""}`)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	type row struct {
		name, url, secret, events, headers string
		active, allowHTTP                  bool
		retries, delay, timeout            int
	}
	get := func(id string) row {
		t.Helper()
		var r row
		err := db.QueryRowContext(ctx, `SELECT name, url, secret, events::text, headers::text, active, allow_http,
			max_retries, retry_delay_seconds, timeout_seconds FROM hatamoto_webhooks WHERE id = $1`, id).
			Scan(&r.name, &r.url, &r.secret, &r.events, &r.headers, &r.active, &r.allowHTTP, &r.retries, &r.delay, &r.timeout)
		if err != nil {
			t.Fatalf("row %s: %v", id, err)
		}
		return r
	}

	a := get("11111111-1111-4111-8111-111111111111")
	if a.name != "old" || a.secret != "" || a.events != `["entity.created", "form.submitted"]` || a.headers != `{"X-Team": "docs"}` ||
		!a.active || !a.allowHTTP || a.retries != 10 || a.delay != 3600 || a.timeout != 5 {
		t.Errorf("clamped row: %+v", a)
	}
	b := get("22222222-2222-4222-8222-222222222222")
	if b.name != "https://u:p@example.com/x" || b.secret != "whsec_abc" || b.active || b.allowHTTP ||
		b.retries != 0 || b.delay != 0 || b.timeout != 1 {
		t.Errorf("unusable url must be paused, negatives clamped: %+v", b)
	}
	c := get("33333333-3333-4333-8333-333333333333")
	if c.events != `[]` || c.active || c.secret != "whsec_xyz" {
		t.Errorf("no known events: paused: %+v", c)
	}
	d := get("44444444-4444-4444-8444-444444444444")
	if d.active || d.secret != "whsec_keep" || d.events != `["import.completed", "entity.deleted"]` || d.headers != `{"X-A": "1"}` {
		t.Errorf("inactive row: %+v", d)
	}
	// The hand-over rows were created by 202610201100 in this same upgrade (a direct
	// jump from the pre-plugin core): they were live, so a keeps its flag. b (unusable
	// url), c (no events) and d (was inactive) stay paused.
	if !a.active {
		t.Errorf("row created in this upgrade lost its active flag: %+v", a)
	}
	var active int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_webhooks WHERE active`).Scan(&active); err != nil || active != 1 {
		t.Errorf("active rows = %d (%v), want 1", active, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_webhooks`).Scan(&n); err != nil || n != 4 {
		t.Errorf("rows = %d (%v), want 4 (other plugin and url-less rows are not copied)", n, err)
	}
	// v0.27.0 still deletes from and reads this table: it must exist, drained of webhooks rows only.
	var left, other int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE plugin = 'webhooks'), count(*) FILTER (WHERE plugin = 'other')
		FROM hatamoto_plugin_handover`).Scan(&left, &other); err != nil {
		t.Fatalf("hatamoto_plugin_handover must still exist: %v", err)
	}
	if left != 0 || other != 1 {
		t.Errorf("hand-over rows left: webhooks=%d other=%d, want 0 and 1", left, other)
	}
}

func TestWebhooksCore_DownRefusesWhileRowsExist(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_webhooks (id, name, url, secret) VALUES ('w1', 'n', 'https://x.example', 's')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, webhooksCoreVersion-1); err == nil || !strings.Contains(err.Error(), "exist nowhere else") {
		t.Fatalf("down with a subscription: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_webhooks`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the row must survive a refused down: %d (%v)", n, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM hatamoto_webhooks`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, webhooksCoreVersion-1); err != nil {
		t.Fatalf("down once empty: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func TestWebhooksCore_DownThenOlderDownWorks(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, webhooksToPluginVersion); err != nil {
		t.Fatalf("down to the handover version: %v", err)
	}
	var reg sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('hatamoto_plugin_handover')::text`).Scan(&reg); err != nil || !reg.Valid {
		t.Fatalf("hand-over table must still exist: %v (%v)", reg, err)
	}
	if _, err := p.DownTo(ctx, webhooksToPluginVersion-1); err != nil {
		t.Fatalf("older down: %v", err)
	}
}

// A hand-over row that has waited since an earlier upgrade was never delivered,
// so it must come in paused however active it was.
func TestWebhooksCore_OldHandoverRowsArePaused(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, webhooksToPluginVersion); err != nil {
		t.Fatalf("up to handover: %v", err)
	}
	const id = "66666666-6666-4666-8666-666666666666"
	seedHandover(t, db, id, `{"id":"`+id+`","name":"waiting","url":"https://ok.example/h","secret":"whsec_old",
	  "events":["entity.created"],"headers":{},"active":true,"max_retries":3,"retry_delay_seconds":60,"timeout_seconds":5}`)
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_plugin_handover SET created_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var active bool
	if err := db.QueryRowContext(ctx, `SELECT active FROM hatamoto_webhooks WHERE id = $1`, id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Error("a hand-over row older than an hour must be imported paused")
	}
}

// The validator caps a name at 120 bytes; left() cuts at 120 characters, so a
// long multi-byte name from a fresh hand-over row must come in paused.
func TestWebhooksCore_OverlongByteNameIsPaused(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDBUTF8(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, webhooksToPluginVersion); err != nil {
		t.Fatalf("up to handover: %v", err)
	}
	const id = "77777777-7777-4777-8777-777777777777"
	name := strings.Repeat("字", 50) // 50 characters, 150 bytes
	seedHandover(t, db, id, `{"id":"`+id+`","name":"`+name+`","url":"https://ok.example/h","secret":"whsec_old",
	  "events":["entity.created"],"headers":{},"active":true,"max_retries":3,"retry_delay_seconds":60,"timeout_seconds":5}`)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var active bool
	if err := db.QueryRowContext(ctx, `SELECT active FROM hatamoto_webhooks WHERE id = $1`, id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Error("a name over 120 bytes would fail validation on resume; the row must be imported paused")
	}
}
