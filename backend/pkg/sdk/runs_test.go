package sdk

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func validRun() Run {
	return Run{Name: "import", Title: "Import", Stages: []string{RunPhaseFetch, RunPhaseTransform}}
}

func validStanding() *StandingApproval {
	return &StandingApproval{Fields: []string{"price"}, Schemas: []string{"product"}}
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
		{"standing valid", func(r *Run) { r.Cron = "tick"; r.StandingApproval = validStanding() }, ""},
		{"standing at the cap", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.StandingApproval.MaxPerHour = DefaultStandingPerHour
		}, ""},
		{"standing unknown cron", func(r *Run) { r.Cron = "nope"; r.StandingApproval = validStanding() }, "declared cron job"},
		{"standing without cron", func(r *Run) { r.StandingApproval = validStanding() }, "declared cron job"},
		{"cron without standing", func(r *Run) { r.Cron = "tick" }, "needs a standing_approval"},
		{"standing empty fields", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.StandingApproval.Fields = nil
		}, "fields needs"},
		{"standing empty schemas", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.StandingApproval.Schemas = nil
		}, "schemas needs"},
		{"standing bad field", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.StandingApproval.Fields = []string{"Price"}
		}, "bad or duplicate"},
		{"standing cap 201", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.StandingApproval.MaxPerHour = DefaultStandingPerHour + 1
		}, "max_per_hour"},
		{"standing with inputs", func(r *Run) {
			r.Cron = "tick"
			r.StandingApproval = validStanding()
			r.Inputs = []RunInput{{Name: "a", Label: "A"}}
		}, "takes no inputs"},
		{"too many inputs", func(r *Run) {
			for i := 0; i <= MaxRunInputs; i++ {
				r.Inputs = append(r.Inputs, RunInput{Name: "i" + strings.Repeat("x", i), Label: "L"})
			}
		}, "inputs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17", Cron: []CronJob{{Name: "tick", Every: "1h"}}}
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

func TestRemovedPermissionsListsTheFiveSorted(t *testing.T) {
	want := []Permission{"entities.write", "media.write", "menus.write", "redirects.write", "terms.write"}
	if got := RemovedPermissions(); !slices.Equal(got, want) {
		t.Fatalf("RemovedPermissions() = %v, want %v", got, want)
	}
	for _, p := range RemovedPermissions() {
		if !RemovedPermission(p) || ValidPermission(p) {
			t.Errorf("%q: removed=%v valid=%v", p, RemovedPermission(p), ValidPermission(p))
		}
	}
}

func TestOnlyOneStandingApprovalRun(t *testing.T) {
	bound := func(name, job string) Run {
		r := validRun()
		r.Name, r.Cron, r.StandingApproval = name, job, validStanding()
		return r
	}
	m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17",
		Cron: []CronJob{{Name: "tick", Every: "1h"}, {Name: "tock", Every: "1h"}},
		Runs: []Run{bound("one", "tick")}}
	if err := m.Validate(); err != nil {
		t.Fatalf("one standing run: %v", err)
	}
	m.Runs = append(m.Runs, bound("two", "tock"))
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "only one run may have a standing_approval") {
		t.Fatalf("two standing runs: %v", err)
	}
}

// A cron-started run fetches from allowed_hosts, so more than the run limit
// is refused when the manifest declares the rule (the "*" wildcard is not
// handed to a run and does not count).
func TestStandingApprovalLimitsAllowedHosts(t *testing.T) {
	hosts := func(n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("h%d.example.test", i))
		}
		return out
	}
	r := validRun()
	r.Cron, r.StandingApproval = "tick", validStanding()
	m := Manifest{Name: "wp", Version: "1.0.0", SDK: "^0.17",
		Cron: []CronJob{{Name: "tick", Every: "1h"}}, Runs: []Run{r},
		Permissions: []Permission{PermHTTPFetch, PermHTTPFetchAny}}
	m.AllowedHosts = append(hosts(MaxRunHosts), "*")
	if err := m.Validate(); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	m.AllowedHosts = hosts(MaxRunHosts + 1)
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Fatalf("over the limit: %v", err)
	}
	m.Runs[0].Cron, m.Runs[0].StandingApproval = "", nil
	m.Cron = nil
	if err := m.Validate(); err != nil {
		t.Fatalf("no rule, no limit: %v", err)
	}
}
