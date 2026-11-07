package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

func listRows(t *testing.T, d formsListData) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, r := range d.Table.Rows {
		var cells []string
		for _, c := range r.Cells {
			text := c.Text
			if c.Sub != "" {
				text += "|" + c.Sub
			}
			if c.Spark != nil {
				text += "~spark"
			}
			cells = append(cells, text)
		}
		out[r.Label] = cells
	}
	return out
}

func TestBuildFormsList(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	contact := ff.addForm("Contact", contactFields...)
	quiet := ff.addForm("Quiet")
	quiet.Status = form.StatusInactive
	now := time.Now()
	day := 24 * time.Hour
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-time.Hour), map[string]any{"name": "Ada", "email": "ada@example.com", "message": "Hello\n there"})
	ff.addSub(contact, form.SubmissionStatusRead, now.Add(-3*day), map[string]any{"name": "Bo"})
	ff.addSub(contact, form.SubmissionStatusSpam, now.Add(-2*day), map[string]any{"name": "Bot"})
	ff.addSub(contact, form.SubmissionStatusRead, now.Add(-40*day), map[string]any{"name": "Old"})

	a := &Admin{adminPath: "/admin"}
	a.deps.Growth.FormUsage = func(context.Context) (map[string][]entity.FormPlacement, error) {
		return map[string][]entity.FormPlacement{"contact": {
			{Kind: entity.PlacementArea, Area: "Footer", Name: "Footer"},
			{Kind: entity.PlacementPage, Name: "Contact page", Slug: "contact"},
		}}, nil
	}
	forms, _ := ff.ListForms(context.Background(), "")
	d := a.buildFormsList(context.Background(), ff, forms)

	rows := listRows(t, d)
	c := rows["Contact"]
	if c[0] != "Contact|contact" || c[3] != "2~spark" || c[4] != "1 unread" || c[6] != "2 places|footer" {
		t.Errorf("Contact row = %q", c)
	}
	q := rows["Quiet"]
	if q[3] != "0" || q[4] != "0" || q[5] != "Never" || q[6] != notPlaced {
		t.Errorf("Quiet row = %q", q)
	}

	stat := func(label string) (v, note string, changed bool) {
		for _, s := range d.Stats {
			if s.Label == label {
				return s.Value, s.Note, s.Change != nil
			}
		}
		t.Fatalf("no %q stat", label)
		return
	}
	if v, _, ch := stat("Submissions, 30 days"); v != "2" || !ch {
		t.Errorf("submissions stat = %q change %v", v, ch)
	}
	if v, n, _ := stat("Unread"); v != "1" || n != "across 1 form" {
		t.Errorf("unread stat = %q %q", v, n)
	}
	if v, _, _ := stat("Spam caught"); v != "1" {
		t.Errorf("spam stat = %q", v)
	}
	if v, n, _ := stat("Active forms"); v != "1" || n != "of 2" {
		t.Errorf("active stat = %q %q", v, n)
	}
	if last := d.Stats[len(d.Stats)-1]; last.Label != "Last 14 days" || last.Spark == nil {
		t.Errorf("trend card = %+v", last)
	}

	// Latest: newest first, spam left out; the newest row names its form,
	// links to the open drawer and carries the excerpt.
	if len(d.Latest.Rows) != 3 {
		t.Fatalf("latest rows = %d, want 3 (spam left out)", len(d.Latest.Rows))
	}
	for _, r := range d.Latest.Rows {
		if r.Cells[0].Text == "Bot" {
			t.Error("a spam row is listed as latest")
		}
	}
	first := d.Latest.Rows[0]
	if first.Cells[0].Text != "Ada" || first.Cells[0].Sub != "ada@example.com" || first.Cells[1].Text != "Contact" ||
		first.Cells[2].Text != "Hello there" || !strings.Contains(first.Cells[0].Link, "/forms/design/"+contact.ID.String()+"/submissions?s=") ||
		first.Cells[0].Tag == nil {
		t.Errorf("latest first row = %+v", first)
	}
}

func TestBuildFormsListCappedAndUnavailable(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	busy := ff.addForm("Busy", contactFields...)
	now := time.Now()
	for i := range submissionTrackerCap + 1 {
		ff.addSub(busy, form.SubmissionStatusRead, now.Add(-time.Duration(i)*time.Second), nil)
	}
	a := &Admin{adminPath: "/admin"}
	a.deps.Growth.FormUsage = func(context.Context) (map[string][]entity.FormPlacement, error) {
		return nil, errors.New("scan timed out")
	}
	forms, _ := ff.ListForms(context.Background(), "")
	d := a.buildFormsList(context.Background(), ff, forms)

	row := listRows(t, d)["Busy"]
	if !strings.HasSuffix(row[3], "+") || strings.Contains(row[3], "spark") || row[6] != "—" {
		t.Errorf("capped row = %q", row)
	}
	if v := d.Stats[0]; !strings.HasSuffix(v.Value, "+") || v.Change != nil {
		t.Errorf("capped stat = %+v", v)
	}
}
