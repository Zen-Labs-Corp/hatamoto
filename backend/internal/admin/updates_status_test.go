package admin

import (
	"strings"
	"testing"
	"time"
)

func TestUpdatesStatusBadge(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name        string
		st          UpdatesStatus
		unavailable bool
		label, kind string
	}{
		{"up to date", UpdatesStatus{Decision: "up_to_date"}, false, "Up to date", "success"},
		{"scheduled", UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}, false, "Scheduled in rollout: v1.4.2", "info"},
		{"waiting window", UpdatesStatus{Decision: "waiting_window", Target: "v1.4.2"}, false, "Waiting for the maintenance window: v1.4.2", "info"},
		{"waiting defer", UpdatesStatus{Decision: "waiting_defer"}, false, "Deferred", "info"},
		{"applying", UpdatesStatus{Decision: "applying", Target: "v1.4.2"}, false, "Updating: v1.4.2", "info"},
		{"staged", UpdatesStatus{Decision: "staged", Target: "v1.4.2"}, false, "Staged: v1.4.2", "info"},
		{"blocked", UpdatesStatus{Decision: "blocked", Reason: "plugin hello needs SDK ^0.8"}, false, "Blocked", "warning"},
		{"check failed", UpdatesStatus{Decision: "check_failed", Reason: "dial tcp: timeout"}, false, "Update check failed", "danger"},
		{"rollback pending", UpdatesStatus{Decision: "rollback_pending", Reason: "v1.4.1 was withdrawn"}, false, "Release withdrawn, rollback pending", "warning"},
		{"busy without a target", UpdatesStatus{Decision: "busy", Reason: "the running release is still on probation"}, false, "Checking again later", "info"},
		{"busy with a target", UpdatesStatus{Decision: "busy", Target: "v1.5.0"}, false, "Waiting to update", "info"},
		{"yanked", UpdatesStatus{Decision: "rolled_back_yanked", Reason: "v1.4.1 was withdrawn"}, false, "Release withdrawn", "warning"},
		{"rolled back", UpdatesStatus{Decision: "rolled_back", Target: "v1.4.2", Reason: "smoke failed"}, false, "Rolled back v1.4.2", "warning"},
		{"scheduled and ready", UpdatesStatus{Decision: "scheduled", Target: "v1.4.2", Reason: "v1.4.2 is ready to install"}, false, "Ready to install: v1.4.2", "info"},
		{"update failed", UpdatesStatus{Decision: "update_failed", Target: "v1.4.2", Reason: "download: reset"}, false, "Update failed: v1.4.2", "danger"},
		{"rolled back no reason", UpdatesStatus{Decision: "rolled_back"}, false, "Rolled back", "warning"},
		{"off", UpdatesStatus{Decision: "off"}, false, "Updates are off", "neutral"},
		{"notify", UpdatesStatus{Decision: "notify", Target: "v1.5.0"}, false, "Update available, notify only: v1.5.0", "neutral"},
		{"dev", UpdatesStatus{Decision: "dev"}, false, "Dev build: updates are disabled", "neutral"},
		{"empty decision", UpdatesStatus{}, false, "No check yet", "neutral"},
		{"unknown decision", UpdatesStatus{Decision: "mystery"}, false, "No check yet", "neutral"},
		{"unavailable wins", UpdatesStatus{Decision: "up_to_date"}, true, "Updates are unavailable", "warning"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := updatesStatusBadge(c.st, c.unavailable)
			if b.Label != c.label || b.Kind != c.kind {
				t.Errorf("got %+v, want %q/%s", b, c.label, c.kind)
			}
		})
	}
}

func TestUpdatesCurrentOverrides(t *testing.T) {
	t.Parallel()

	at := time.Now()
	for _, c := range []struct {
		name string
		s    UpdatesState
		want string
	}{
		{"dev wins", UpdatesState{Dev: true, Mode: "off", Status: UpdatesStatus{Decision: "blocked"}}, "dev"},
		{"off mode beats stale status", UpdatesState{Mode: "off", Status: UpdatesStatus{Decision: "up_to_date"}}, "off"},
		{"error result", UpdatesState{Mode: "auto", Status: UpdatesStatus{LastResult: "error", Decision: "up_to_date", LastCheckAt: &at}}, "check_failed"},
		{"never checked", UpdatesState{Mode: "auto"}, "no_check"},
		{"plain", UpdatesState{Mode: "auto", Status: UpdatesStatus{Decision: "scheduled", LastCheckAt: &at}}, "scheduled"},
	} {
		if got := c.s.Current().Decision; got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUpdatesApplyTarget(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		st      UpdatesStatus
		running string
		want    string
	}{
		{UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0", Target: "v1.4.2"}, "v1.4.1", "v2.0.0"},
		{UpdatesStatus{Decision: "scheduled", Target: "v1.4.2", AvailableMajor: "v2.0.0"}, "v1.4.1", "v1.4.2"},
		{UpdatesStatus{Decision: "waiting_window", Target: "v1.4.2"}, "v1.4.1", "v1.4.2"},
		{UpdatesStatus{Decision: "waiting_defer", Target: "v1.4.2"}, "v1.4.1", "v1.4.2"},
		{UpdatesStatus{Decision: "up_to_date", Target: "v1.4.2"}, "v1.4.1", ""},
		{UpdatesStatus{Decision: "blocked", Target: "v1.4.2"}, "v1.4.1", ""},
		{UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}, "v2.0.0", ""},
		{UpdatesStatus{}, "v1.4.1", ""},
	} {
		if got := c.st.ApplyTarget(c.running); got != c.want {
			t.Errorf("%+v running %s: %q, want %q", c.st, c.running, got, c.want)
		}
	}
}

func TestPickBanner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		in       UpdatesBannerInput
		kind     string
		contains string
	}{
		{"nothing", UpdatesBannerInput{Mode: "auto", Status: UpdatesStatus{Decision: "up_to_date"}}, "", ""},
		{"rolled back", UpdatesBannerInput{Running: "v1.4.1", Status: UpdatesStatus{Decision: "rolled_back", Target: "v1.4.2", Reason: "health check failed"}},
			"rolled_back", "Update rolled back: v1.4.2 failed its health check and the site went back to v1.4.1."},
		{"rolled back other reason", UpdatesBannerInput{Running: "v1.4.1", Status: UpdatesStatus{Decision: "rolled_back", Target: "v1.4.2", Reason: "disk full"}},
			"rolled_back", "Update rolled back: v1.4.2: disk full."},
		{"blocked", UpdatesBannerInput{Mode: "auto", Status: UpdatesStatus{Decision: "blocked", Reason: "plugin hello needs SDK ^0.8"}},
			"blocked", "An update is blocked: plugin hello needs SDK ^0.8."},
		{"backup failed", UpdatesBannerInput{Mode: "auto", Status: UpdatesStatus{Decision: "backup_failed", Target: "v1.5.0", Reason: "not enough free disk space"}},
			"backup_failed", "The update was not installed because the backup before it failed: not enough free disk space."},
		{"backup failed while off", UpdatesBannerInput{Mode: "off", Status: UpdatesStatus{Decision: "backup_failed", Reason: "x"}}, "", ""},
		{"blocked while off", UpdatesBannerInput{Mode: "off", Status: UpdatesStatus{Decision: "blocked", Reason: "x"}}, "", ""},
		{"long reason is clipped", UpdatesBannerInput{Mode: "auto", Status: UpdatesStatus{Decision: "blocked", Reason: strings.Repeat("plugin hello needs SDK ^0.8; ", 20)}}, "blocked", "…"},
		// A major release and long-off updates are the chip's, not a banner.
		{"major available", UpdatesBannerInput{Mode: "auto", Running: "v1.4.1", Status: UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}}, "", ""},
		{"updates off", UpdatesBannerInput{Mode: "off"}, "", ""},
		{"dev build", UpdatesBannerInput{Dev: true, Status: UpdatesStatus{Decision: "rolled_back"}}, "", ""},
		{"rolled back beats all", UpdatesBannerInput{Mode: "off", Status: UpdatesStatus{Decision: "rolled_back", Target: "v1.4.2", AvailableMajor: "v2.0.0"}}, "rolled_back", ""},
		{"blocked beats a major", UpdatesBannerInput{Mode: "auto", Status: UpdatesStatus{Decision: "blocked", AvailableMajor: "v2.0.0"}}, "blocked", ""},
		{"pending rollback has a banner", UpdatesBannerInput{Mode: "auto", Running: "v1.4.1", Status: UpdatesStatus{Decision: "rollback_pending", Reason: "v1.4.1 was withdrawn; check only"}},
			"rolled_back", "The running release was withdrawn."},
		{"yanked rollback has a banner", UpdatesBannerInput{Mode: "auto", Running: "v1.4.0", Status: UpdatesStatus{Decision: "rolled_back_yanked", Reason: "v1.4.1 was withdrawn (bad build)"}},
			"rolled_back", "A withdrawn release was rolled back: v1.4.1 was withdrawn (bad build)."},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := PickBanner(c.in, now)
			if c.kind == "" {
				if b != nil {
					t.Fatalf("want no banner, got %+v", b)
				}
				return
			}
			if b == nil || b.Kind != c.kind {
				t.Fatalf("got %+v, want kind %s", b, c.kind)
			}
			if !strings.Contains(b.Message, c.contains) || b.Href != "/admin/settings/updates" || b.Label != "View updates" {
				t.Errorf("banner %+v, want message containing %q", b, c.contains)
			}
		})
	}
}

func TestUpdatesNoteIsPlainEnglish(t *testing.T) {
	t.Parallel()

	sydney, _ := time.LoadLocation("Australia/Sydney")
	cases := []struct {
		name string
		st   UpdatesState
		cur  UpdatesStatus
		want []string
		not  []string
	}{
		{"rollout", UpdatesState{}, UpdatesStatus{Decision: "scheduled", Target: "v1.4.2", Reason: "v1.4.2 is rolling out (bucket 37, ramp 20%)"},
			[]string{"Releases reach sites gradually"}, []string{"bucket", "ramp 20"}},
		{"ready", UpdatesState{}, UpdatesStatus{Decision: "scheduled", Target: "v1.4.2", Reason: "v1.4.2 is ready to install"},
			[]string{"installs at the next scheduled check"}, []string{"gradually"}},
		{"deferred in site time", UpdatesState{}, UpdatesStatus{Decision: "waiting_defer", Reason: "v1.5.0 is deferred until 2026-10-20T00:00:00Z"},
			[]string{"Held back until 20 Oct 2026 11:00 (Australia/Sydney)."}, []string{"2026-10-20", "T00:00"}},
		{"window", UpdatesState{WindowStart: ptrInt(2), WindowEnd: ptrInt(5)}, UpdatesStatus{Decision: "waiting_window", Reason: "v1.5.0 waits for the maintenance window"},
			[]string{"02:00 to 05:00 (Australia/Sydney)"}, nil},
		{"notify", UpdatesState{}, UpdatesStatus{Decision: "notify", Target: "v1.5.0", Reason: "v1.5.0 is available; mode notify, not installing"},
			[]string{"v1.5.0 is available. You chose Notify only"}, []string{"mode notify"}},
		{"blocked keeps the reason", UpdatesState{}, UpdatesStatus{Decision: "blocked", Reason: "plugin hello needs SDK ^0.8"},
			[]string{"plugin hello needs SDK ^0.8"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := updatesNote(c.st, c.cur, sydney)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("note %q lacks %q", got, w)
				}
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("note %q has %q", got, n)
				}
			}
		})
	}
}
