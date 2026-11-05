package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRow struct{ id, raw string }

type fakeJSONSource struct {
	cols []jsonColumn
	rows map[string][]fakeRow
	err  error
}

func (f fakeJSONSource) Columns(context.Context) ([]jsonColumn, error) { return f.cols, nil }

func (f fakeJSONSource) Rows(_ context.Context, c jsonColumn, fn func(string, []byte) error) error {
	if f.err != nil {
		return f.err
	}
	for _, r := range f.rows[c.key()] {
		if err := fn(r.id, []byte(r.raw)); err != nil {
			return err
		}
	}
	return nil
}

func TestDoctorRegistryKeysAreTableDotColumn(t *testing.T) {
	for k := range doctorRegistry {
		table, column, ok := strings.Cut(k, ".")
		if !ok || table == "" || column == "" || strings.Contains(column, ".") {
			t.Errorf("registry key %q is not table.column", k)
		}
	}
}

func TestDoctorJSONReport(t *testing.T) {
	theme := jsonColumn{Table: "hatamoto_site_config", Column: "theme_extra", HasID: true}
	forms := jsonColumn{Table: "hatamoto_forms", Column: "settings", HasID: true}
	extra := jsonColumn{Table: "hatamoto_unknown", Column: "doc"}
	src := fakeJSONSource{
		cols: []jsonColumn{theme, forms, extra},
		rows: map[string][]fakeRow{
			theme.key(): {
				{"1", `{"typography":{"fontFamily":"x"}}`},
				{"2", `{"headercta":{"text":"a","url":"/"}}`},
				{"3", `{"typography":"not an object"}`},
			},
			forms.key(): {{"f1", `{"submit_button_text":"Go"}`}},
			extra.key(): {{"(0,1)", `{"a":1}`}},
		},
	}
	rep, err := checkStoredJSON(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	rep.render(&out)
	got := out.String()
	for _, want := range []string{
		"hatamoto_site_config.theme_extra",
		"3 rows   FAIL",
		"hatamoto_forms.settings",
		"ok (unregistered, valid JSON only)",
		"hatamoto_site_config.theme_extra id=2: case-variant keys: /headercta -> headerCta",
		"hatamoto_site_config.theme_extra id=3: decode:",
		"3 columns, 1 rows fail strict decode, 1 case-variant keys",
		"\nFAILED\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "id=1:") {
		t.Errorf("clean row reported:\n%s", got)
	}
	if rep.FailedRows != 2 {
		t.Errorf("FailedRows = %d, want 2", rep.FailedRows)
	}
}

func TestDoctorJSONReportsDroppedValues(t *testing.T) {
	forms := jsonColumn{Table: "hatamoto_forms", Column: "settings", HasID: true}
	src := fakeJSONSource{
		cols: []jsonColumn{forms},
		rows: map[string][]fakeRow{forms.key(): {{"f1", `{"Submit_Button_Text":"old","submit_button_text":"new"}`}}},
	}
	rep, err := checkStoredJSON(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	rep.render(&out)
	if want := `/Submit_Button_Text dropped (collides with submit_button_text, value "old")`; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestDoctorJSONCleanIsOK(t *testing.T) {
	c := jsonColumn{Table: "hatamoto_forms", Column: "fields", HasID: true}
	rep, err := checkStoredJSON(context.Background(), fakeJSONSource{
		cols: []jsonColumn{c},
		rows: map[string][]fakeRow{c.key(): {{"1", `[{"id":"a","default_value":"x"}]`}, {"2", `[]`}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	rep.render(&out)
	if !strings.HasSuffix(out.String(), "0 case-variant keys\nOK\n") || rep.FailedRows != 0 {
		t.Fatalf("report:\n%s", out.String())
	}
}

func TestDoctorJSONSourceErrorIsReturned(t *testing.T) {
	c := jsonColumn{Table: "hatamoto_forms", Column: "fields"}
	boom := errors.New("boom")
	_, err := checkStoredJSON(context.Background(), fakeJSONSource{cols: []jsonColumn{c}, err: boom})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "hatamoto_forms.fields") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoctorJSONNeedsDSN(t *testing.T) {
	err := runDoctorJSON(context.Background(), "", &bytes.Buffer{})
	var oe operationalError
	if !errors.As(err, &oe) {
		t.Fatalf("empty DSN: err = %v, want an operationalError (exit 2)", err)
	}
}

func TestDoctorJSONUnreachableDatabaseIsOperational(t *testing.T) {
	err := runDoctorJSON(context.Background(), "postgres://u@127.0.0.1:1/x?connect_timeout=1", &bytes.Buffer{})
	var oe operationalError
	var ee exitError
	if !errors.As(err, &oe) || errors.As(err, &ee) {
		t.Fatalf("err = %v, want operationalError and not exitError{1}", err)
	}
}

func TestDoctorJSONStoredTypedColumns(t *testing.T) {
	for _, key := range []string{
		"hatamoto_seo_settings.settings", "hatamoto_privacy_settings.settings", "hatamoto_tracking_settings.settings",
		"hatamoto_site_config.business_profile", "hatamoto_site_config.postal_address",
		"hatamoto_site_config.locale_profiles", "hatamoto_form_submissions.metadata",
	} {
		if doctorRegistry[key].decode == nil {
			t.Errorf("%s is typed but has no decoder", key)
		}
	}
	for _, key := range []string{"hatamoto_site_config.locales", "hatamoto_media.variants"} {
		if doctorRegistry[key].shape == nil {
			t.Errorf("%s has no shape, case variants go unreported", key)
		}
	}
	// A wrongly typed value fails the decode instead of printing ok.
	c := jsonColumn{Table: "hatamoto_form_submissions", Column: "metadata", HasID: true}
	rep, err := checkStoredJSON(context.Background(), fakeJSONSource{
		cols: []jsonColumn{c},
		rows: map[string][]fakeRow{c.key(): {{"1", `{"ip_address":5}`}}},
	})
	if err != nil || rep.DecodeFails != 1 {
		t.Fatalf("decode fails = %v, err %v", rep, err)
	}
	// And a case variant in locales is reported.
	c = jsonColumn{Table: "hatamoto_site_config", Column: "locales", HasID: true}
	rep, err = checkStoredJSON(context.Background(), fakeJSONSource{
		cols: []jsonColumn{c},
		rows: map[string][]fakeRow{c.key(): {{"1", `[{"Tag":"en","segment":"en"}]`}}},
	})
	if err != nil || rep.VariantKeys != 1 {
		t.Fatalf("variant keys = %+v, err %v", rep, err)
	}
}

func TestThousands(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 4118: "4,118", 1234567: "1,234,567"} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDoctorUsage(t *testing.T) {
	if !strings.Contains(usage, "doctor json") {
		t.Error("usage lacks doctor json")
	}
	if strings.ContainsAny(doctorUsage, "\u2013\u2014") {
		t.Error("doctor usage contains an em or en dash")
	}
}
