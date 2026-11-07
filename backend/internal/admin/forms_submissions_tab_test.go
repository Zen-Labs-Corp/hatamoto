package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

// landingTraffic answers the landing-page ranking with fixed rows.
type landingTraffic struct {
	fakeTraffic
	rows []analytics.TrafficTop
}

func (l *landingTraffic) TrafficTop(context.Context, analytics.Dim, time.Time, time.Time, int) ([]analytics.TrafficTop, error) {
	return l.rows, nil
}

func TestRelTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{59 * time.Minute, "59m ago"},
		{7 * time.Hour, "7h ago"},
		{23*time.Hour + 59*time.Minute, "23h ago"},
		{24 * time.Hour, "yesterday"},
		{47 * time.Hour, "yesterday"},
		{48 * time.Hour, "2 days ago"},
		{3 * 24 * time.Hour, "3 days ago"},
		{29 * 24 * time.Hour, "29 days ago"},
		{40 * 24 * time.Hour, "28 Sep 2026"},
	} {
		if got := relTime(now, now.Add(-c.ago)); got != c.want {
			t.Errorf("relTime(-%s) = %q, want %q", c.ago, got, c.want)
		}
	}
}

func TestChoiceColumnsTab(t *testing.T) {
	t.Parallel()
	sel := func(n string) form.FormField { return form.FormField{Name: n, Type: form.FieldTypeSelect} }
	txt := form.FormField{Name: "t", Type: form.FieldTypeText}
	for name, c := range map[string]struct {
		fields []form.FormField
		want   []string
	}{
		"none":     {[]form.FormField{txt}, nil},
		"one":      {[]form.FormField{txt, sel("a")}, []string{"a"}},
		"radio":    {[]form.FormField{{Name: "r", Type: form.FieldTypeRadio}, sel("b")}, []string{"r", "b"}},
		"capped":   {[]form.FormField{sel("a"), sel("b"), sel("c")}, []string{"a", "b"}},
		"no field": {nil, nil},
	} {
		got := choiceColumns(&form.Form{Fields: c.fields})
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %d columns, want %d", name, len(got), len(c.want))
		}
		for i, ff := range got {
			if ff.Name != c.want[i] {
				t.Errorf("%s: column %d = %s, want %s", name, i, ff.Name, c.want[i])
			}
		}
	}
}

func TestFormConversionTab(t *testing.T) {
	t.Parallel()
	f := &form.Form{ID: uuid.New(), Slug: "contact", Status: form.StatusActive}
	sub := func(page string) *form.FormSubmission {
		return &form.FormSubmission{Metadata: form.SubmissionMetadata{PageURL: page}}
	}
	subs := []*form.FormSubmission{sub("https://qa.test/contact?x=1"), sub("https://qa.test/contact"), sub("")}
	from, to := time.Now().AddDate(0, 0, -30), time.Now()
	for name, c := range map[string]struct {
		traffic         Traffic
		value, note     string
		wantUnavailable bool
	}{
		"ratio":       {&landingTraffic{rows: []analytics.TrafficTop{{K1: "/contact", Counts: analytics.Counts{Visits: 10}}, {K1: "/other", Counts: analytics.Counts{Visits: 500}}}}, "20.0%", "10 page visits", false},
		"nil traffic": {nil, "", "", true},
		"zero visits": {&landingTraffic{rows: []analytics.TrafficTop{{K1: "/other", Counts: analytics.Counts{Visits: 9}}}}, "", "", true},
	} {
		a := &Admin{}
		a.deps.Growth.Traffic = c.traffic
		a.deps.Growth.FormUsage = pageUsage("contact", "contact")
		a.deps.Content.PublicPath = func(_ context.Context, e *entity.Entity) string { return "/" + e.Slug }
		st := a.formConversion(context.Background(), f, subs, from, to)
		if c.wantUnavailable {
			if st.Value != notAvailable || st.Note != "" {
				t.Errorf("%s: %+v, want not available", name, st)
			}
			continue
		}
		if st.Value != c.value || st.Note != c.note {
			t.Errorf("%s: value %q note %q, want %q %q", name, st.Value, st.Note, c.value, c.note)
		}
	}
}

// pageUsage places the form on one published page.
func pageUsage(formSlug, pageSlug string) func(context.Context) (map[string][]entity.FormPlacement, error) {
	return func(context.Context) (map[string][]entity.FormPlacement, error) {
		return map[string][]entity.FormPlacement{formSlug: {
			{EntityID: uuid.New(), Kind: entity.PlacementPage, Name: "Page", Slug: pageSlug, SchemaSlug: "page", Status: string(entity.StatusPublished)},
			{Kind: entity.PlacementArea, Name: "Footer", Area: "Footer"},
		}}, nil
	}
}

func TestFormPagesTab(t *testing.T) {
	t.Parallel()
	a := &Admin{}
	a.deps.Growth.FormUsage = pageUsage("contact", "b")
	a.deps.Content.PublicPath = func(_ context.Context, e *entity.Entity) string { return "/" + e.Slug }
	f := &form.Form{Slug: "contact", Status: form.StatusActive}

	// The placements' published pages and the hosted page; an area has no path.
	got := a.formPages(context.Background(), f)
	if want := []string{"/b", "/f/contact"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pages = %v, want %v", got, want)
	}
	// A paused form has no hosted page.
	f.Status = form.StatusInactive
	if got := a.formPages(context.Background(), f); len(got) != 1 || got[0] != "/b" {
		t.Fatalf("paused pages = %v, want [/b]", got)
	}
}

func TestSubmissionDrawerTabEmptyMetadata(t *testing.T) {
	t.Parallel()
	a := &Admin{}
	f := &form.Form{Fields: []form.FormField{
		{Name: "email", Label: "Email", Type: form.FieldTypeEmail},
		{Name: "h", Label: "Heading", Type: form.FieldTypeHeading},
	}}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	// No Data at all (store off) and no metadata: no panic, only the fixed rows.
	d := a.buildSubmissionDrawer(context.Background(), f, &form.FormSubmission{ID: uuid.New(), CreatedAt: now.Add(-time.Hour), Status: form.SubmissionStatusNew}, now, "tok")
	if d.Title != "Anonymous" || d.ReplyURL != "" || len(d.Answers) != 0 {
		t.Errorf("empty drawer: %+v", d)
	}
	labels := map[string]string{}
	for _, x := range d.Details {
		labels[x.Label] = x.Value
	}
	if len(d.Details) != 3 || labels["Received"] != "1h ago" || labels["Location"] != notRecorded || labels["Notifications go to"] != "the site address" {
		t.Errorf("details: %+v", d.Details)
	}
	// An answer for a deleted field still shows, after the form's own.
	s := &form.FormSubmission{ID: uuid.New(), CreatedAt: now, Data: map[string]any{"email": "ada@qa.test", "gone": "x"},
		Metadata: form.SubmissionMetadata{PageURL: "https://qa.test/c", UserAgent: "iPhone Mobile"}}
	d = a.buildSubmissionDrawer(context.Background(), f, s, now, "tok")
	if d.ReplyURL != "mailto:ada@qa.test" || len(d.Answers) != 2 || d.Answers[1].Label != "gone" {
		t.Errorf("drawer: %+v", d)
	}
}

// ISC-6: the row is From (name over company), Email, the choice fields' answers, Page and Received
// with the exact time as its title; a checkbox group counts as a choice, a lone checkbox does not.
func TestSubmissionsTabColumnsAndRow(t *testing.T) {
	t.Parallel()
	opts := []form.FieldOption{{Label: "A", Value: "a"}}
	f := &form.Form{Fields: []form.FormField{
		{Name: "name", Label: "Name", Type: form.FieldTypeText},
		{Name: "company", Label: "Company", Type: form.FieldTypeText},
		{Name: "email", Label: "Email", Type: form.FieldTypeEmail},
		{Name: "topic", Label: "Topic", Type: form.FieldTypeSelect},
		{Name: "agree", Label: "I agree", Type: form.FieldTypeCheckbox},
		{Name: "size", Label: "Team size", Type: form.FieldTypeRadio},
		{Name: "extras", Label: "Extras", Type: form.FieldTypeCheckbox, Options: opts},
	}}
	var labels []string
	for _, c := range submissionsTabColumns(f) {
		labels = append(labels, c.Label)
	}
	if got, want := strings.Join(labels, "|"), "From|Email|Topic|Team size|Page|Received"; got != want {
		t.Fatalf("columns = %s, want %s", got, want)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := &form.FormSubmission{ID: uuid.New(), CreatedAt: now.Add(-3 * time.Hour), Status: form.SubmissionStatusRead,
		Data:     map[string]any{"name": "Ada", "company": "Acme", "email": "ada@qa.test", "topic": "Sales", "size": "10"},
		Metadata: form.SubmissionMetadata{PageURL: "https://qa.test/pricing"}}
	cells := submissionsTabRow(f, s, now, time.UTC, "/open").Cells
	if len(cells) != 6 {
		t.Fatalf("%d cells, want 6", len(cells))
	}
	if cells[0].Text != "Ada" || cells[0].Sub != "Acme" || cells[1].Text != "ada@qa.test" || cells[2].Text != "Sales" || cells[3].Text != "10" || cells[4].Text != "/pricing" {
		t.Errorf("cells: %+v", cells)
	}
	if cells[5].Text != "3h ago" || cells[5].Title != "7 Nov 2026 09:00" {
		t.Errorf("received cell = %q title %q", cells[5].Text, cells[5].Title)
	}
}
