package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/localeconv"
)

type fakeLocaleSvc struct {
	report   *localeconv.Report
	applyEr  error
	revEr    error
	applied  int
	revID    int64
	revForce bool
	history  []localeconv.HistoryItem
}

func (f *fakeLocaleSvc) Suggest(context.Context) (*localeconv.Plan, []localeconv.Issue, error) {
	return &localeconv.Plan{Default: "en-XX"}, []localeconv.Issue{{Code: "no_tag", Message: "root yy has no tag"}}, nil
}

func (f *fakeLocaleSvc) DryRun(context.Context, *localeconv.Plan) (*localeconv.Report, error) {
	return f.report, nil
}

func (f *fakeLocaleSvc) Apply(context.Context, *localeconv.Plan, localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error) {
	f.applied++
	if f.applyEr != nil {
		return nil, nil, f.applyEr
	}
	return &localeconv.Record{ID: 7, Rows: 12, Status: "applied"}, f.report, nil
}

func (f *fakeLocaleSvc) Revert(_ context.Context, id int64, force bool, _ string) (*localeconv.RevertResult, error) {
	f.revID, f.revForce = id, force
	if f.revEr != nil {
		return nil, f.revEr
	}
	return &localeconv.RevertResult{ConversionID: id, Restored: 12}, nil
}

func (f *fakeLocaleSvc) History(context.Context) ([]localeconv.HistoryItem, error) {
	return f.history, nil
}

func okReport() *localeconv.Report {
	return &localeconv.Report{
		PlanSHA256: "abc", OK: true,
		Locales: []localeconv.LocaleReport{{Tag: "en-XX", Segment: "xx", Home: localeconv.HomeReport{OldPath: "/xx", NewPath: "/xx"}, CountsBySchema: map[string]int{"page": 3}, Total: 3}},
	}
}

func writePlanFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, []byte(`{"default":"en-XX"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runLocales(t *testing.T, svc localeconv.Service, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	o, perr := parseLocalesOpts(args)
	if perr != nil {
		t.Fatalf("parse %v: %v", args, perr)
	}
	var out, errOut bytes.Buffer
	err = localesConvert(context.Background(), svc, o, &out, &errOut)
	return out.String(), errOut.String(), err
}

func exitCode(err error) int {
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestParseLocalesOpts(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"--suggest"}, ""},
		{[]string{"--plan", "p.json"}, ""},
		{[]string{"--plan", "p.json", "--apply", "--text"}, ""},
		{[]string{"--revert", "4"}, ""},
		{[]string{"--revert", "4", "--force"}, ""},
		{[]string{"--history", "--json"}, ""},
		{nil, "exactly one"},
		{[]string{"--suggest", "--history"}, "exactly one"},
		{[]string{"--apply"}, "exactly one"},
		{[]string{"--suggest", "--apply"}, "--apply needs --plan"},
		{[]string{"--plan", "p.json", "--force"}, "only valid with --revert"},
		{[]string{"--history", "--json", "--text"}, "exclude each other"},
		{[]string{"--revert", "-2"}, "positive"},
		{[]string{"--revert", "abc"}, "invalid"},
		{[]string{"--history", "extra"}, "usage"},
	}
	for _, c := range cases {
		_, err := parseLocalesOpts(c.args)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%v: unexpected error %v", c.args, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%v: error %v, want %q", c.args, err, c.wantErr)
		}
	}
}

func TestLocalesSuggestPrintsPlanAndWarnings(t *testing.T) {
	out, errOut, err := runLocales(t, &fakeLocaleSvc{}, "--suggest")
	if err != nil {
		t.Fatal(err)
	}
	var p localeconv.Plan
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.Default != "en-XX" {
		t.Fatalf("stdout is not the plan: %v %q", err, out)
	}
	if !strings.Contains(errOut, "no_tag") {
		t.Fatalf("warning not on stderr: %q", errOut)
	}
}

func TestLocalesDryRunExitCodes(t *testing.T) {
	plan := writePlanFile(t)
	svc := &fakeLocaleSvc{report: okReport()}
	out, _, err := runLocales(t, svc, "--plan", plan)
	if exitCode(err) != 0 || !strings.Contains(out, `"ok": true`) || svc.applied != 0 {
		t.Fatalf("ok dry run: err=%v applied=%d out=%s", err, svc.applied, out)
	}

	bad := okReport()
	bad.OK = false
	bad.Errors = []localeconv.Issue{{Code: "path_changes", Message: "3 paths change"}}
	svc.report = bad
	out, _, err = runLocales(t, svc, "--plan", plan, "--text")
	if exitCode(err) != 2 || !strings.Contains(out, "blocked") || !strings.Contains(out, "path_changes") {
		t.Fatalf("blocked dry run: err=%v out=%s", err, out)
	}
}

func TestLocalesApply(t *testing.T) {
	plan := writePlanFile(t)
	svc := &fakeLocaleSvc{report: okReport()}
	out, _, err := runLocales(t, svc, "--plan", plan, "--apply", "--text")
	if err != nil || svc.applied != 1 {
		t.Fatalf("apply: err=%v applied=%d", err, svc.applied)
	}
	for _, want := range []string{"applied conversion 7", "--revert 7", "content poll interval"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output lacks %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\u2013\u2014") {
		t.Errorf("output has a dash character:\n%s", out)
	}

	out, _, err = runLocales(t, svc, "--plan", plan, "--apply")
	var got struct {
		Record *localeconv.Record `json:"record"`
		Note   string             `json:"note"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Record == nil || got.Record.ID != 7 || got.Note == "" {
		t.Fatalf("json apply: err=%v out=%s", err, out)
	}
}

func TestLocalesApplyBlockingIsExit2(t *testing.T) {
	bad := okReport()
	bad.OK = false
	svc := &fakeLocaleSvc{report: bad, applyEr: &localeconv.BlockingError{
		Issues: []localeconv.Issue{{Code: "stale_report", Message: "data changed"}},
	}}
	out, _, err := runLocales(t, svc, "--plan", writePlanFile(t), "--apply")
	if exitCode(err) != 2 || !strings.Contains(out, "stale_report") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestLocalesIOErrorsAreExit1(t *testing.T) {
	_, _, err := runLocales(t, &fakeLocaleSvc{}, "--plan", filepath.Join(t.TempDir(), "missing.json"))
	if exitCode(err) != 1 {
		t.Fatalf("missing plan file: %v", err)
	}
	svc := &fakeLocaleSvc{revEr: errors.New("connection lost")}
	_, _, err = runLocales(t, svc, "--revert", "3")
	if exitCode(err) != 1 {
		t.Fatalf("service failure: %v", err)
	}
}

func TestLocalesRevert(t *testing.T) {
	svc := &fakeLocaleSvc{}
	out, _, err := runLocales(t, svc, "--revert", "5", "--force", "--text")
	if err != nil || svc.revID != 5 || !svc.revForce || !strings.Contains(out, "reverted conversion 5") {
		t.Fatalf("err=%v id=%d force=%v out=%s", err, svc.revID, svc.revForce, out)
	}

	svc = &fakeLocaleSvc{revEr: &localeconv.BlockingError{Issues: []localeconv.Issue{{Code: "newer_conversion", Message: "conversion 6 is newer"}}}}
	out, _, err = runLocales(t, svc, "--revert", "5")
	if exitCode(err) != 2 || !strings.Contains(out, "newer_conversion") {
		t.Fatalf("refused revert: err=%v out=%s", err, out)
	}
}

func TestLocalesHistory(t *testing.T) {
	svc := &fakeLocaleSvc{}
	out, _, err := runLocales(t, svc, "--history")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty json history: err=%v out=%q", err, out)
	}
	svc.history = []localeconv.HistoryItem{{ID: 2, AppliedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), AppliedBy: "cli", Status: "applied", Rows: 9, Locales: []string{"en-XX", "fr-YY"}}}
	out, _, err = runLocales(t, svc, "--history", "--text")
	if err != nil || !strings.Contains(out, "2  2026-01-02 03:04:05  applied  by cli  rows 9  locales en-XX,fr-YY") {
		t.Fatalf("text history: err=%v out=%q", err, out)
	}
}
