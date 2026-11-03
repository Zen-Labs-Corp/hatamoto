package sdk

import (
	"strings"
	"testing"
)

func validRun() Run {
	return Run{Name: "import", Title: "Import", Stages: []string{RunPhaseFetch, RunPhaseTransform}}
}

func TestRunsValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Run)
		want   string
	}{
		{"valid", func(*Run) {}, ""},
		{"with inputs and proposals", func(r *Run) {
			r.Inputs = []RunInput{{Name: "site_url", Label: "Site", Type: "url", Required: true}}
			r.Proposes = []string{ProposeSiteSettings, ProposeURLPatterns, ProposeHomePage, ProposeMenuLocation}
		}, ""},
		{"bad name", func(r *Run) { r.Name = "Import" }, "run name"},
		{"no title", func(r *Run) { r.Title = "" }, "title is required"},
		{"stages reversed", func(r *Run) { r.Stages = []string{RunPhaseTransform, RunPhaseFetch} }, "stages"},
		{"stages missing", func(r *Run) { r.Stages = nil }, "stages"},
		{"unknown proposal", func(r *Run) { r.Proposes = []string{"theme"} }, "proposal kind"},
		{"duplicate input", func(r *Run) { r.Inputs = []RunInput{{Name: "a", Label: "A"}, {Name: "a", Label: "B"}} }, "duplicate input"},
		{"input without label", func(r *Run) { r.Inputs = []RunInput{{Name: "a"}} }, "needs a label"},
		{"too many inputs", func(r *Run) {
			for i := 0; i <= MaxRunInputs; i++ {
				r.Inputs = append(r.Inputs, RunInput{Name: "i" + strings.Repeat("x", i), Label: "L"})
			}
		}, "inputs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17"}
			r := validRun()
			tc.mutate(&r)
			m.Runs = []Run{r}
			err := m.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestRunsLimits(t *testing.T) {
	m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17", Runs: []Run{validRun(), validRun()}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate run") {
		t.Fatalf("duplicate run: %v", err)
	}
	m.Runs = nil
	for i := 0; i <= MaxRuns; i++ {
		r := validRun()
		r.Name = "r" + strings.Repeat("x", i)
		m.Runs = append(m.Runs, r)
	}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("too many runs: %v", err)
	}
}

func TestRunStepHookTimeout(t *testing.T) {
	m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17", Limits: Limits{HookTimeoutsMS: map[string]uint64{RunStepExport: 5000}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("on_run_step timeout key refused: %v", err)
	}
	if got := m.HookTimeout(RunStepExport).Milliseconds(); got != 5000 {
		t.Fatalf("timeout = %d", got)
	}
	if got := (Manifest{}).HookTimeout(RunStepExport).Milliseconds(); got != DefaultActionTimeoutMS {
		t.Fatalf("default = %d, want %d", got, DefaultActionTimeoutMS)
	}
}

func TestRemovedPermissionsAreNotValid(t *testing.T) {
	for p := range removedPermissions {
		if ValidPermission(p) {
			t.Errorf("%q is removed but still valid", p)
		}
	}
}

func TestRemovedPermissionIsOnlyTheFiveRemoved(t *testing.T) {
	for p := range removedPermissions {
		if !RemovedPermission(p) {
			t.Errorf("%q is removed but not reported", p)
		}
	}
	for _, p := range []Permission{"entities.writ", "kv", ""} {
		if RemovedPermission(p) {
			t.Errorf("%q reported as removed", p)
		}
	}
}
