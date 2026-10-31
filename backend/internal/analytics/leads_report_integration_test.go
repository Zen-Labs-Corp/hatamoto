//go:build integration

package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLeadsReader(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	formA, formB := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_forms (id, name, slug, fields) VALUES ($1, 'Contact', 'contact', '[{"name":"mail","type":"email"}]')`, formA)
	exec(`INSERT INTO hatamoto_forms (id, name, slug, fields) VALUES ($1, 'Plain', 'plain', '[]')`, formB)
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }
	type sub struct {
		form                uuid.UUID
		at                  time.Time
		status              string
		data                string
		fc, fs, fm, fcm, fl *string
		lc, ls, lm, lcm, ll *string
	}
	p := func(s string) *string { return &s }
	subs := []sub{
		{formA, day(1, 10, 0), "new", `{"mail":"a@example.test"}`, p("ai_assistants"), p("perplexity"), nil, nil, p("/pricing"), p("email"), p("newsletter"), p("email"), p("spring"), p("/blog")},
		{formA, day(1, 23, 59), "read", `{"mail":"b@example.test"}`, p("organic"), p("google.com"), nil, nil, p("/"), p("organic"), p("google.com"), nil, nil, p("/")},
		{formB, day(2, 0, 0), "new", `{"email":"c@example.test"}`, p("organic"), p("google.com"), nil, nil, p("/"), p("organic"), p("google.com"), nil, nil, p("/")},
		{formB, day(2, 5, 0), "new", `{"x":"1"}`, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}, // no attribution: unknown
		{formA, day(2, 6, 0), "spam", `{"mail":"spam@example.test"}`, p("paid"), p("g"), nil, nil, nil, p("paid"), p("g"), nil, nil, nil},
		{formA, day(9, 0, 0), "new", `{"mail":"late@example.test"}`, p("direct"), p("(none)"), nil, nil, nil, p("direct"), p("(none)"), nil, nil, nil}, // out of range
	}
	for _, s := range subs {
		exec(`INSERT INTO hatamoto_form_submissions (form_id, data, status, created_at,
				first_channel, first_source, first_medium, first_campaign, first_landing,
				last_channel, last_source, last_medium, last_campaign, last_landing)
			VALUES ($1, $2::jsonb, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			s.form, s.data, s.status, s.at, s.fc, s.fs, s.fm, s.fcm, s.fl, s.lc, s.ls, s.lm, s.lcm, s.ll)
	}
	rd := NewReader(db)
	from, to := day(1, 0, 0), day(3, 0, 0)

	total, unknown, err := rd.LeadsTotals(ctx, TouchFirst, from, to)
	if err != nil || total != 4 || unknown != 1 {
		t.Errorf("totals = %d, %d, %v; want 4 leads (spam and out-of-range excluded), 1 unknown", total, unknown, err)
	}
	if total, _, _ := rd.LeadsTotals(ctx, TouchLast, from, to); total != 4 {
		t.Errorf("last-touch total = %d", total)
	}

	daily, err := rd.LeadsDaily(ctx, TouchFirst, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// 23:59 UTC stays on day 1, 00:00 UTC is day 2.
	want := []LeadDay{{"2026-10-01", "ai_assistants", 1}, {"2026-10-01", "organic", 1}, {"2026-10-02", "organic", 1}, {"2026-10-02", ChannelUnknown, 1}}
	if len(daily) != len(want) {
		t.Fatalf("daily = %+v", daily)
	}
	for i := range want {
		if daily[i] != want[i] {
			t.Errorf("daily[%d] = %+v, want %+v", i, daily[i], want[i])
		}
	}
	if last, _ := rd.LeadsDaily(ctx, TouchLast, from, to); len(last) == 0 || last[0].Channel != "email" {
		t.Errorf("last-touch daily = %+v", last)
	}

	byChannel, err := rd.LeadsTop(ctx, DimChannel, TouchFirst, from, to, 10)
	if err != nil || len(byChannel) != 3 || byChannel[0] != (LeadRow{"organic", "", 2}) {
		t.Errorf("by channel = %+v, %v", byChannel, err)
	}
	bySource, err := rd.LeadsTop(ctx, DimSource, TouchFirst, from, to, 10)
	if err != nil || len(bySource) != 2 || bySource[0] != (LeadRow{"google.com", "organic", 2}) || bySource[1] != (LeadRow{"perplexity", "ai_assistants", 1}) {
		t.Errorf("by source = %+v, %v (NULL attribution must not appear)", bySource, err)
	}
	byCampaign, err := rd.LeadsTop(ctx, DimCampaign, TouchLast, from, to, 10)
	if err != nil || len(byCampaign) != 1 || byCampaign[0] != (LeadRow{"spring", "email", 1}) {
		t.Errorf("by campaign = %+v, %v", byCampaign, err)
	}
	byLanding, err := rd.LeadsTop(ctx, DimLanding, TouchFirst, from, to, 1)
	if err != nil || len(byLanding) != 1 || byLanding[0] != (LeadRow{"/", "", 2}) {
		t.Errorf("by landing = %+v, %v", byLanding, err)
	}
	if _, err := rd.LeadsTop(ctx, DimDevice, TouchFirst, from, to, 10); err == nil {
		t.Error("device dimension accepted")
	}

	forms, err := rd.LeadsByForm(ctx, from, to, 10)
	if err != nil || len(forms) != 2 || forms[0].FormID != formA || forms[0].Leads != 2 || forms[1].Leads != 2 {
		t.Errorf("by form = %+v, %v", forms, err)
	}

	recent, err := rd.RecentLeads(ctx, from, to, 10)
	if err != nil || len(recent) != 4 {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
	emails := map[string]bool{}
	for _, l := range recent {
		emails[l.Email] = true
		if l.Email == "spam@example.test" {
			t.Error("spam submission listed")
		}
	}
	if !emails["a@example.test"] || !emails["c@example.test"] || !emails[""] {
		t.Errorf("emails = %v (typed email field, fallback key, none)", emails)
	}
	if recent[0].Email != "" || recent[0].First != nil || recent[0].Last != nil {
		t.Errorf("newest lead (no attribution) = %+v", recent[0])
	}
	last := recent[3]
	if last.Email != "a@example.test" || last.First == nil || last.First.Source != "perplexity" || last.First.Landing != "/pricing" || last.Last == nil || last.Last.Campaign != "spring" {
		t.Errorf("oldest lead = %+v %+v %+v", last, last.First, last.Last)
	}
	if one, _ := rd.RecentLeads(ctx, from, to, 1); len(one) != 1 {
		t.Errorf("limit ignored: %d", len(one))
	}
}
