package admin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/analytics"
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
	f := &form.Form{ID: uuid.New(), Slug: "contact"}
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

func TestFormPagesTab(t *testing.T) {
	t.Parallel()
	a := &Admin{}
	f := &form.Form{Slug: "contact"}
	subs := []*form.FormSubmission{
		{Metadata: form.SubmissionMetadata{PageURL: "https://qa.test/b?x=1"}},
		{Metadata: form.SubmissionMetadata{PageURL: "https://qa.test/b"}},
		{Metadata: form.SubmissionMetadata{PageURL: "/a"}},
		{},
	}
	got := a.formPages(context.Background(), f, subs)
	want := []string{"/a", "/b", "/f/contact"}
	if len(got) != len(want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pages = %v, want %v", got, want)
		}
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
