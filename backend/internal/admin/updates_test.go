package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakeUpdates is an UpdatesService with a fixed state and call records.
type fakeUpdates struct {
	state    UpdatesState
	viewErr  error
	banner   *UpdateBanner
	bannerEr error

	saved     []UpdatesForm
	saveErrs  map[string]string
	checks    int
	checkRes  UpdatesResult
	checkErr  error
	applies   int
	applyRes  UpdatesResult
	applyErr  error
	unpins    int
	noBackup  int
	bannerGet int
	glanceGet int
}

func (f *fakeUpdates) View(context.Context) (UpdatesState, error) { return f.state, f.viewErr }
func (f *fakeUpdates) Save(_ context.Context, in UpdatesForm) (map[string]string, error) {
	f.saved = append(f.saved, in)
	return f.saveErrs, nil
}
func (f *fakeUpdates) Check(context.Context) (UpdatesResult, error) {
	f.checks++
	return f.checkRes, f.checkErr
}
func (f *fakeUpdates) Apply(context.Context) (UpdatesResult, error) {
	f.applies++
	return f.applyRes, f.applyErr
}
func (f *fakeUpdates) ApplyWithoutBackup(context.Context) (UpdatesResult, error) {
	f.noBackup++
	return f.applyRes, f.applyErr
}
func (f *fakeUpdates) Unpin(context.Context) error { f.unpins++; return nil }
func (f *fakeUpdates) Banner(context.Context) (*UpdateBanner, error) {
	f.bannerGet++
	return f.banner, f.bannerEr
}

// Glance mirrors View, as the real service derives both from the same row.
func (f *fakeUpdates) Glance(context.Context) (UpdatesGlance, error) {
	f.glanceGet++
	st := f.state
	return UpdatesGlance{Running: st.Running, Dev: st.Dev, Mode: st.Mode, Channel: st.Channel, Status: st.Status, OffSince: st.OffSince, History: st.History}, f.viewErr
}

var updatesClock = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func ptrInt(n int) *int { return &n }

// baseUpdates is an installed, supervised release on the stable channel that
// was checked 12 minutes ago.
func baseUpdates() *fakeUpdates {
	at := updatesClock.Add(-12 * time.Minute)
	return &fakeUpdates{state: UpdatesState{
		Running: "v1.4.1", Mode: "auto", Channel: "stable", Channels: []string{"stable", "edge", "canary"},
		Status: UpdatesStatus{LastCheckAt: &at, LastResult: "ok", Decision: "up_to_date", Reason: "running the newest release offered"},
	}}
}

type updatesRig struct {
	*ownerRig
	svc    *fakeUpdates
	events []AuditEvent
}

func newUpdatesRig(t *testing.T, svc *fakeUpdates) *updatesRig {
	t.Helper()
	old := updatesNow
	updatesNow = func() time.Time { return updatesClock }
	t.Cleanup(func() { updatesNow = old })
	g := newOwnerRig(t, true)
	g.a.deps.Site.(*fakeSite).cfg.Timezone = "Australia/Sydney"
	if svc != nil {
		g.a.deps.Updates = svc
	}
	u := &updatesRig{ownerRig: g, svc: svc}
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { u.events = append(u.events, e) }
	return u
}

// followFlash loads the Updates page with the flash cookie a redirect set.
func (u *updatesRig) followFlash(rec interface{ Result() *http.Response }) string {
	req := httptestReq("GET", "/admin/settings/updates", "owner-token")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return serve(u.h, req, nil).Body.String()
}

func (u *updatesRig) get(t *testing.T) string {
	t.Helper()
	rec := u.do("GET", "/admin/settings/updates", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestUpdatesPageStates(t *testing.T) {
	history := []UpdatesHistoryRow{
		{When: time.Date(2026, 11, 5, 3, 12, 0, 0, time.UTC), From: "v1.4.0", To: "v1.4.1", Phase: "success"},
		{When: time.Date(2026, 11, 2, 3, 10, 0, 0, time.UTC), From: "v1.4.0", To: "v1.4.1", Phase: "rolled_back", Error: "smoke failed: GET / returned 500"},
		{When: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), From: "v1.3.9", To: "v1.4.0", Phase: "failed", Error: "disk full"},
	}
	for _, c := range []struct {
		name    string
		mut     func(f *fakeUpdates)
		wants   []string
		absents []string
	}{
		{"up_to_date", func(f *fakeUpdates) {}, []string{"Up to date", "12 minutes ago · ok", "Check now", "Australia/Sydney", "v1.4.1</dd>", "Version · stable channel"}, []string{"Update now", "Unpin"}},
		{"scheduled", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target = "scheduled", "v1.4.2"
			f.state.Status.Reason = "v1.4.2 is rolling out (bucket 37, ramp 20%)"
		}, []string{"Scheduled in rollout: v1.4.2", "turn has not come yet", "Update now to v1.4.2"}, []string{"bucket", "ramp 20%"}},
		{"blocked", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target = "blocked", "v1.5.0"
			f.state.Status.Reason = "plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0)"
		}, []string{`hm-statcard__status"><span class="hm-badge hm-badge--warning"><span class="hm-badge__dot" aria-hidden="true"></span>Blocked</span></dd>`, `<span id="updates-status-note">plugin hello needs SDK ^0.8 (v1.5.0 has 0.10.0)</span>`}, []string{"Update now", "Blocked: plugin"}},
		{"major_available", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.AvailableMajor = "available_major", "v2.0.0"
		}, []string{"Major v2.0.0 available", "Update now to v2.0.0"}, nil},
		{"rolled_back", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target = "rolled_back", "v1.4.2"
			f.state.Status.Reason = "smoke failed: GET / returned 500"
		}, []string{"Rolled back v1.4.2", `<span id="updates-status-note">smoke failed: GET / returned 500</span>`}, []string{"Rolled back v1.4.2:"}},
		{"rolled_back_no_reason", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target, f.state.Status.Reason = "rolled_back", "v1.4.2", ""
		}, []string{`id="updates-status"`, `hm-callout--danger`, `<p class="hm-callout__title">Rolled back v1.4.2</p>`}, []string{"updates-status-note"}},
		{"off", func(f *fakeUpdates) { f.state.Mode = "off" }, []string{"Updates are off"}, nil},
		{"notify", func(f *fakeUpdates) {
			f.state.Mode = "notify"
			f.state.Status.Decision, f.state.Status.Target = "notify", "v1.5.0"
			f.state.Status.Reason = "v1.5.0 is available; mode notify, not installing"
		}, []string{"Update available, notify only: v1.5.0", "You chose Notify only", "Update now to v1.5.0"}, []string{"mode notify"}},
		{"update_failed", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target, f.state.Status.LastResult = "update_failed", "v1.5.0", "error"
			f.state.Status.Reason = "download v1.5.0: connection reset"
		}, []string{"Update failed: v1.5.0", "download v1.5.0: connection reset", "Update now to v1.5.0"}, []string{"Update check failed"}},
		{"backup_failed", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target, f.state.Status.LastResult = "backup_failed", "v1.5.0", "error"
			f.state.Status.Reason = "pg_dump: error: connection to server failed <script>"
		}, []string{"Backup failed", "v1.5.0 was not installed: the backup before the update failed: pg_dump: error: connection to server failed &lt;script&gt;", "Update now to v1.5.0", "Update to v1.5.0 without a backup", `id="nobackup-form"`}, []string{"Update check failed", "<script>"}},
		{"rollback_pending", func(f *fakeUpdates) {
			f.state.Status.Decision = "rollback_pending"
			f.state.Status.Reason = "v1.4.1 was withdrawn (bad build); rolling back to v1.4.0; check only, not switching"
		}, []string{"Release withdrawn, rollback pending", "v1.4.1 was withdrawn (bad build); rolling back to v1.4.0. This check changed nothing; the next scheduled check rolls the site back."}, []string{"Update available", "notify only", "check only", "Update now"}},
		{"update_busy", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target, f.state.Status.LastResult = "busy", "v1.5.0", "skipped"
			f.state.Status.Reason = "a content import is running; will retry next check"
		}, []string{"Waiting to update", "a content import is running", "Update now to v1.5.0"}, nil},
		{"switched", func(f *fakeUpdates) {
			f.state.Status.Decision, f.state.Status.Target = "applying", "v1.4.1"
			f.state.Status.Reason = "switching to v1.4.1"
		}, []string{"Up to date"}, []string{"Updating", "switching to", "Update now"}},
		{"check_failed", func(f *fakeUpdates) {
			f.state.Status.LastResult, f.state.Status.Reason = "error", "fetch channel manifest: timeout"
		}, []string{"Update check failed", `<span id="updates-status-note">fetch channel manifest: timeout</span>`, "12 minutes ago · failed"}, []string{"Update check failed:"}},
		{"dev", func(f *fakeUpdates) { f.state.Dev, f.state.Running = true, "dev" }, []string{"Dev build: updates are disabled", "Dev build</dd>"}, []string{"Check now", "Update now"}},
		{"no_check_yet", func(f *fakeUpdates) { f.state.Status = UpdatesStatus{} }, []string{"No check yet", "Never"}, nil},
		{"pinned", func(f *fakeUpdates) { f.state.Pin = "v1.4" }, []string{"v1.4 (security fixes inside v1.4 still install)", "Unpin"}, nil},
		{"env_locked", func(f *fakeUpdates) {
			f.state.Locks = UpdatesLocks{Mode: true, Channel: true, ModeVar: "HATAMOTO_UPDATES", ChannelVar: "HATAMOTO_UPDATE_CHANNEL"}
		}, []string{">Automatic</span>", "Set by HATAMOTO_UPDATES on the server.", ">Stable (recommended for live sites)</span>", "Set by HATAMOTO_UPDATE_CHANNEL on the server."}, []string{`name="mode"`, `name="channel"`, "Automatic ·", "stable ·"}},
		{"canary", func(f *fakeUpdates) { f.state.Channel = "canary" }, []string{`<option value="canary" selected>Canary (bleeding edge, may break)</option>`, "Stable is for live sites: it gets each release from day 7 (5%, then 25% on day 9, 100% on day 11). Edge gets it on day 2. Canary is bleeding edge for testing and may break: it gets each release first, before anyone else has run it."}, nil},
		{"env_locked_canary", func(f *fakeUpdates) {
			f.state.Channel = "canary"
			f.state.Locks = UpdatesLocks{Channel: true, ChannelVar: "HATAMOTO_UPDATE_CHANNEL"}
		}, []string{">Canary (bleeding edge, may break)</span>", "has run it. Set by HATAMOTO_UPDATE_CHANNEL on the server."}, []string{`name="channel"`, "canary ·"}},
		{"window_and_defer", func(f *fakeUpdates) {
			f.state.WindowStart, f.state.WindowEnd, f.state.DeferDays = ptrInt(2), ptrInt(5), 3
		}, []string{`<option value="2" selected>02:00</option>`, `<option value="5" selected>05:00</option>`, `<option value="3" selected>3 days</option>`}, nil},
		{"applying", func(f *fakeUpdates) {
			f.state.Applying = true
			f.state.Status.Decision, f.state.Status.Target = "scheduled", "v1.4.2"
		}, []string{"An update is running."}, []string{"Update now"}},
		{"history_empty", func(f *fakeUpdates) {}, []string{"No updates yet."}, nil},
		{"history_filled", func(f *fakeUpdates) { f.state.History = history }, []string{"5 Nov 2026 14:12", "Updated", "v1.4.0 → v1.4.1", "v1.3.9 → v1.4.0", ">smoke failed: GET / returned 500<", ">disk full<"}, []string{"No updates yet.", "Rolled back: smoke", "Failed: disk"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := baseUpdates()
			c.mut(f)
			body := newUpdatesRig(t, f).get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, b := range c.absents {
				if strings.Contains(body, b) {
					t.Errorf("page has %q", b)
				}
			}
			compareGolden(t, "pages/settings-updates-"+c.name+".html", normalizePage(body))
		})
	}
}

func TestUpdatesPageUnavailable(t *testing.T) {
	for name, reason := range map[string]string{"unavailable": "the update address is invalid", "unavailable_default": ""} {
		t.Run(name, func(t *testing.T) {
			g := newUpdatesRig(t, nil)
			g.a.deps.UpdatesUnavailable = reason
			body := g.get(t)
			want := reason
			if want == "" {
				want = "the updater did not start, see the server log"
			}
			if !strings.Contains(body, "The update agent did not start: "+want) {
				t.Error("unavailable card missing")
			}
			if strings.Contains(body, ">Save<") || strings.Contains(body, "Check now") {
				t.Error("controls shown without a service")
			}
			if name == "unavailable" {
				compareGolden(t, "pages/settings-updates-unavailable.html", normalizePage(body))
			}
		})
	}
}

func TestUpdatesPageViewError(t *testing.T) {
	f := baseUpdates()
	f.viewErr = errors.New("db down")
	rec := newUpdatesRig(t, f).do("GET", "/admin/settings/updates", "owner-token", nil, nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("view error = %d, body leaks: %v", rec.Code, strings.Contains(rec.Body.String(), "db down"))
	}
}

func TestUpdatesPageIsOwnerOnly(t *testing.T) {
	g := newUpdatesRig(t, baseUpdates())
	if rec := g.do("GET", "/admin/settings/updates", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/updates", "", nil, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous GET = %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

var updatesPosts = []string{"", "/check", "/apply", "/apply-without-backup", "/unpin"}

func TestUpdatesPostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}
	u := newUpdatesRig(t, f)
	for _, p := range updatesPosts {
		target := "/admin/settings/updates" + p
		if rec := u.do("POST", target, "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := u.do("POST", target, "", url.Values{}, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
		if rec := u.do("POST", target, "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
	}
	if len(f.saved) != 0 || f.checks != 0 || f.applies != 0 || f.unpins != 0 || len(u.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestUpdatesPostsWithoutServiceAre503(t *testing.T) {
	u := newUpdatesRig(t, nil)
	for _, p := range updatesPosts {
		if rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", url.Values{}, nil); rec.Code != 503 {
			t.Errorf("POST %s without a service = %d, want 503", p, rec.Code)
		}
	}
}

func goodUpdatesForm() url.Values {
	return url.Values{"mode": {"notify"}, "channel": {"edge"}, "window_start": {"2"}, "window_end": {"5"}, "defer_days": {"3"}}
}

func TestUpdatesSave(t *testing.T) {
	u := newUpdatesRig(t, baseUpdates())
	rec := u.do("POST", "/admin/settings/updates", "owner-token", goodUpdatesForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(u.svc.saved) != 1 {
		t.Fatalf("saved = %+v", u.svc.saved)
	}
	got := u.svc.saved[0]
	if got.Mode != "notify" || got.Channel != "edge" || got.WindowStart == nil || *got.WindowStart != 2 || got.WindowEnd == nil || *got.WindowEnd != 5 || got.DeferDays != 3 {
		t.Errorf("saved = %+v", got)
	}
	if len(u.events) != 1 || u.events[0].Action != "updates.save" || u.events[0].Actor == "" {
		t.Errorf("audit = %+v", u.events)
	}
	if body := u.followFlash(rec); !strings.Contains(body, "Update settings saved.") {
		t.Error("success flash missing")
	}
}

func TestUpdatesSaveAnyTime(t *testing.T) {
	u := newUpdatesRig(t, baseUpdates())
	f := goodUpdatesForm()
	f.Set("window_start", "")
	f.Set("window_end", "")
	if rec := u.do("POST", "/admin/settings/updates", "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := u.svc.saved[0]; got.WindowStart != nil || got.WindowEnd != nil {
		t.Errorf("any time saved as %v-%v", got.WindowStart, got.WindowEnd)
	}
	// Same hour twice is accepted: the updater reads it as any time.
	f.Set("window_start", "4")
	f.Set("window_end", "4")
	if rec := u.do("POST", "/admin/settings/updates", "owner-token", f, nil); rec.Code != 303 {
		t.Errorf("start == end = %d, want 303", rec.Code)
	}
}

func TestUpdatesSaveInvalid(t *testing.T) {
	for _, c := range []struct {
		name   string
		mut    func(url.Values)
		want   string
		keeps  string
		anchor string
	}{
		{"one-sided window", func(v url.Values) { v.Set("window_end", "") }, "Set both hours, or leave both on Any time.", `<option value="2" selected>02:00</option>`, `href="#window-start"`},
		{"bad hour", func(v url.Values) { v.Set("window_start", "25") }, "Choose hours from 00:00 to 23:00, or Any time.", `<option value="3" selected>3 days</option>`, `href="#window-start"`},
		{"defer 15", func(v url.Values) { v.Set("defer_days", "15") }, "Choose from 0 to 14 days.", `<option value="5" selected>05:00</option>`, `href="#defer"`},
		{"defer not a number", func(v url.Values) { v.Set("defer_days", "soon") }, "Choose from 0 to 14 days.", `<option value="2" selected>02:00</option>`, `href="#defer"`},
		{"bad mode", func(v url.Values) { v.Set("mode", "yolo") }, "Choose Automatic, Notify only or Off.", `<option value="3" selected>3 days</option>`, `href="#mode"`},
		{"bad channel", func(v url.Values) { v.Set("channel", "nightly") }, "Choose one of: stable, edge, canary.", `<option value="3" selected>3 days</option>`, `href="#channel"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := newUpdatesRig(t, baseUpdates())
			f := goodUpdatesForm()
			c.mut(f)
			rec := u.do("POST", "/admin/settings/updates", "owner-token", f, nil)
			body := rec.Body.String()
			if rec.Code != 422 || !strings.Contains(body, c.want) || !strings.Contains(body, "There is a problem with this form") || !strings.Contains(body, c.anchor) {
				t.Fatalf("code %d, body lacks %q", rec.Code, c.want)
			}
			if !strings.Contains(body, c.keeps) {
				t.Errorf("entered values not kept: lacks %s", c.keeps)
			}
			if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y: %v", issues)
			}
			if len(u.svc.saved) != 0 || len(u.events) != 0 {
				t.Error("an invalid save reached the service")
			}
		})
	}
}

func TestUpdatesSaveServiceFieldError(t *testing.T) {
	f := baseUpdates()
	f.saveErrs = map[string]string{"channel": "update channel \"edge\" is not offered"}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates", "owner-token", goodUpdatesForm(), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "is not offered") || len(u.events) != 0 {
		t.Errorf("code %d, audit %d", rec.Code, len(u.events))
	}
}

func TestUpdatesSaveIgnoresLockedFields(t *testing.T) {
	f := baseUpdates()
	f.state.Locks = UpdatesLocks{Mode: true, Channel: true, ModeVar: "HATAMOTO_UPDATES", ChannelVar: "HATAMOTO_UPDATE_CHANNEL"}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates", "owner-token", goodUpdatesForm(), nil)
	if rec.Code != 303 || len(f.saved) != 1 {
		t.Fatalf("save = %d %+v", rec.Code, f.saved)
	}
	if got := f.saved[0]; got.Mode != "auto" || got.Channel != "stable" || got.DeferDays != 3 {
		t.Errorf("locked fields were taken from the post: %+v", got)
	}
	body := u.followFlash(rec)
	for _, want := range []string{"Mode is set by HATAMOTO_UPDATES", "Channel is set by HATAMOTO_UPDATE_CHANNEL", "so it was not changed"} {
		if !strings.Contains(body, want) {
			t.Errorf("flash lacks %q", want)
		}
	}
	// A post that matches the locked value changes nothing and needs no warning.
	f.saved = nil
	f2 := url.Values{"window_start": {""}, "window_end": {""}, "defer_days": {"0"}}
	rec = u.do("POST", "/admin/settings/updates", "owner-token", f2, nil)
	if body := u.followFlash(rec); rec.Code != 303 || strings.Contains(body, "not changed") || !strings.Contains(body, "Update settings saved.") {
		t.Errorf("plain save with locks = %d", rec.Code)
	}
}

func TestUpdatesCheck(t *testing.T) {
	for _, c := range []struct {
		name string
		res  UpdatesResult
		err  error
		want string
	}{
		{"up to date", UpdatesResult{Status: UpdatesStatus{Decision: "up_to_date"}}, nil, "Check finished: Up to date"},
		{"scheduled", UpdatesResult{Status: UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}}, nil, "Check finished: Scheduled in rollout: v1.4.2"},
		{"blocked", UpdatesResult{Status: UpdatesStatus{Decision: "blocked", Reason: "plugin hello needs SDK ^0.8"}}, nil, "Check finished: Blocked: plugin hello needs SDK ^0.8"},
		{"busy", UpdatesResult{Busy: true}, nil, "Another update check is running."},
		{"error", UpdatesResult{}, errors.New("fetch channel manifest: timeout"), "Update check failed: fetch channel manifest: timeout"},
		{"pending rollback", UpdatesResult{Status: UpdatesStatus{Decision: "rollback_pending"}}, nil, "Nothing was changed."},
		{"due now", UpdatesResult{Status: UpdatesStatus{Decision: "applying", Target: "v1.4.2"}}, nil, "would update to v1.4.2. Nothing was installed"},
		{"withdrawn", UpdatesResult{Status: UpdatesStatus{Decision: "rolled_back_yanked"}}, nil, "Nothing was changed."},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := baseUpdates()
			f.checkRes, f.checkErr = c.res, c.err
			u := newUpdatesRig(t, f)
			rec := u.do("POST", "/admin/settings/updates/check", "owner-token", url.Values{"version": {"v9.9.9"}}, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" || f.checks != 1 {
				t.Fatalf("check = %d %s, calls %d", rec.Code, rec.Header().Get("Location"), f.checks)
			}
			if body := u.followFlash(rec); !strings.Contains(body, c.want) {
				t.Errorf("flash lacks %q", c.want)
			}
			if len(f.saved) != 0 || f.applies != 0 {
				t.Error("Check saved or applied something")
			}
		})
	}
}

func TestUpdatesCheckHasATimeout(t *testing.T) {
	u := newUpdatesRig(t, baseUpdates())
	dl := &deadlineUpdates{fakeUpdates: u.svc}
	u.a.deps.Updates = dl
	u.do("POST", "/admin/settings/updates/check", "owner-token", url.Values{}, nil)
	if !dl.hadDeadline || dl.left > updatesCheckTimeout {
		t.Errorf("Check ran without a %s deadline: %+v", updatesCheckTimeout, dl.left)
	}
}

// deadlineUpdates records the deadline Check was called with.
type deadlineUpdates struct {
	*fakeUpdates
	hadDeadline bool
	left        time.Duration
}

func (d *deadlineUpdates) Check(ctx context.Context) (UpdatesResult, error) {
	dl, ok := ctx.Deadline()
	d.hadDeadline, d.left = ok, time.Until(dl)
	return d.fakeUpdates.Check(ctx)
}

func TestUpdatesApply(t *testing.T) {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}
	f.applyRes = UpdatesResult{Target: "v2.0.0", Started: true}
	u := newUpdatesRig(t, f)
	// A version in the form is ignored: the service derives the target.
	rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"version": {"v9.9.9"}, "target": {"v9.9.9"}, "confirm": {"1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" || f.applies != 1 {
		t.Fatalf("apply = %d, calls %d", rec.Code, f.applies)
	}
	if body := u.followFlash(rec); !strings.Contains(body, "Update started; this page shows the result when it finishes.") {
		t.Error("started flash missing")
	}
	if len(u.events) != 1 || u.events[0].Action != "updates.apply" || u.events[0].Target != "v2.0.0" {
		t.Errorf("audit = %+v", u.events)
	}
}

func TestUpdatesApplyWithNothingToInstall(t *testing.T) {
	for name, mut := range map[string]func(*fakeUpdates){
		"up to date": func(f *fakeUpdates) {},
		"blocked":    func(f *fakeUpdates) { f.state.Status = UpdatesStatus{Decision: "blocked", Target: "v1.5.0"} },
		"dev build": func(f *fakeUpdates) {
			f.state.Dev, f.state.Status = true, UpdatesStatus{Decision: "scheduled", Target: "v1.5.0"}
		},
		"already here": func(f *fakeUpdates) { f.state.Status = UpdatesStatus{Decision: "scheduled", Target: "v1.4.1"} },
	} {
		t.Run(name, func(t *testing.T) {
			f := baseUpdates()
			mut(f)
			u := newUpdatesRig(t, f)
			rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"version": {"v2.0.0"}}, nil)
			if rec.Code != 303 || f.applies != 0 {
				t.Fatalf("apply = %d, calls %d", rec.Code, f.applies)
			}
			if body := u.followFlash(rec); !strings.Contains(body, "Update not started: there is no update to install.") {
				t.Error("error flash missing")
			}
			if len(u.events) != 0 {
				t.Error("audited an update that did not start")
			}
		})
	}
}

func TestUpdatesApplyWhileRunningDoesNotCallTheService(t *testing.T) {
	f := baseUpdates()
	f.state.Applying = true
	f.state.Status = UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || f.applies != 0 || !strings.Contains(u.followFlash(rec), "An update is already running.") {
		t.Errorf("apply while running = %d, calls %d", rec.Code, f.applies)
	}
}

func TestUpdatesApplyHiddenWhenEnvLocksUpdatesOff(t *testing.T) {
	f := baseUpdates()
	f.state.Mode = "off"
	f.state.Locks = UpdatesLocks{Mode: true, ModeVar: "HATAMOTO_UPDATES"}
	f.state.Status = UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}
	u := newUpdatesRig(t, f)
	if body := u.get(t); strings.Contains(body, "Update now") {
		t.Error("Update now shown while the environment turns updates off")
	}
	rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{}, nil)
	if f.applies != 0 || !strings.Contains(u.followFlash(rec), "Update not started") {
		t.Errorf("apply under an env-off lock called the service %d times", f.applies)
	}
}

func TestUpdatesApplyOutcomes(t *testing.T) {
	for _, c := range []struct {
		name string
		res  UpdatesResult
		err  error
		want string
	}{
		{"already running", UpdatesResult{Busy: true}, nil, "An update is already running."},
		{"error", UpdatesResult{}, errors.New("v2.0.0 was withdrawn (bad build)"), "Update not started: v2.0.0 was withdrawn (bad build)"},
		{"service says nothing to install", UpdatesResult{}, ErrNoUpdate, "Update not started: there is no update to install."},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := baseUpdates()
			f.state.Status = UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}
			f.applyRes, f.applyErr = c.res, c.err
			u := newUpdatesRig(t, f)
			rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"confirm": {"1"}}, nil)
			if rec.Code != 303 || f.applies != 1 {
				t.Fatalf("apply = %d, calls %d", rec.Code, f.applies)
			}
			if body := u.followFlash(rec); !strings.Contains(body, c.want) {
				t.Errorf("flash lacks %q", c.want)
			}
			if len(u.events) != 0 {
				t.Error("audited an update that did not start")
			}
		})
	}
}

func TestUpdatesUnpin(t *testing.T) {
	f := baseUpdates()
	f.state.Pin = "v1.4"
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/unpin", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/updates" || f.unpins != 1 {
		t.Fatalf("unpin = %d, calls %d", rec.Code, f.unpins)
	}
	if body := u.followFlash(rec); !strings.Contains(body, "Unpinned.") {
		t.Error("flash missing")
	}
	if len(u.events) != 1 || u.events[0].Action != "updates.unpin" {
		t.Errorf("audit = %+v", u.events)
	}
}

func TestUpdatesPostsNeverTouchTheServiceOnViewError(t *testing.T) {
	f := baseUpdates()
	f.viewErr = errors.New("db down")
	u := newUpdatesRig(t, f)
	for _, p := range []string{"", "/apply"} {
		if rec := u.do("POST", "/admin/settings/updates"+p, "owner-token", goodUpdatesForm(), nil); rec.Code != 500 {
			t.Errorf("POST %s with a failing view = %d, want 500", p, rec.Code)
		}
	}
	if len(f.saved) != 0 || f.applies != 0 {
		t.Error("service called after a failed view")
	}
}

// The server mounts the admin before the update agent exists, so the page must
// follow SetUpdates.
func TestUpdatesFollowSetUpdates(t *testing.T) {
	g := newUpdatesRig(t, nil)
	if body := g.get(t); !strings.Contains(body, "The update agent did not start") {
		t.Fatal("no unavailable card before SetUpdates")
	}
	g.a.SetUpdates(baseUpdates(), "")
	if body := g.get(t); !strings.Contains(body, ">Save<") || strings.Contains(body, "The update agent did not start") {
		t.Error("the page did not pick up the service")
	}
	g.a.SetUpdates(nil, "the update address is invalid")
	if body := g.get(t); !strings.Contains(body, "The update agent did not start: the update address is invalid") {
		t.Error("the page did not pick up the reason")
	}
}

func TestUpdatesCheckNeverSaysUpdating(t *testing.T) {
	f := baseUpdates()
	f.checkRes = UpdatesResult{Status: UpdatesStatus{Decision: "applying", Target: "v1.4.2", Reason: "updating v1.4.1 to v1.4.2"}}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/check", "owner-token", nil, nil)
	if body := u.followFlash(rec); strings.Contains(body, "Updating") {
		t.Errorf("a check that installs nothing says Updating:\n%s", body)
	}
}

func TestUpdatesCheckFlashKinds(t *testing.T) {
	for name, c := range map[string]struct {
		res  UpdatesResult
		err  error
		kind string
	}{
		"blocked": {UpdatesResult{Status: UpdatesStatus{Decision: "blocked", Reason: "x"}}, nil, "warning"},
		"failed":  {UpdatesResult{}, errors.New("timeout"), "warning"},
		"ok":      {UpdatesResult{Status: UpdatesStatus{Decision: "up_to_date"}}, nil, "success"},
	} {
		t.Run(name, func(t *testing.T) {
			f := baseUpdates()
			f.checkRes, f.checkErr = c.res, c.err
			u := newUpdatesRig(t, f)
			rec := u.do("POST", "/admin/settings/updates/check", "owner-token", nil, nil)
			if body := u.followFlash(rec); !strings.Contains(body, `data-hm-flash="`+c.kind+`"`) {
				t.Errorf("want a %s flash:\n%s", c.kind, body)
			}
		})
	}
}

func TestUpdatesFlashTruncatesLongReasons(t *testing.T) {
	f := baseUpdates()
	f.checkErr = errors.New("fetch channel stable: " + strings.Repeat("deadline exceeded ", 400))
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/check", "owner-token", nil, nil)
	var size int
	for _, c := range rec.Result().Cookies() {
		size += len(c.Value)
	}
	if size > 1500 {
		t.Fatalf("flash cookie is %d bytes; a browser drops it above 4096", size)
	}
	if body := u.followFlash(rec); !strings.Contains(body, "Update check failed: fetch channel stable") || !strings.Contains(body, "…") {
		t.Error("the truncated flash did not render")
	}
}

func TestUpdatesApplyMajorAsksFirst(t *testing.T) {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}
	f.applyRes = UpdatesResult{Target: "v2.0.0", Started: true}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"version": {"v9.9.9"}}, nil)
	body := rec.Body.String()
	if rec.Code != 200 || f.applies != 0 {
		t.Fatalf("first POST = %d, service calls %d: a major must not start without a confirmation", rec.Code, f.applies)
	}
	for _, want := range []string{"v2.0.0 is a major release", "cannot go back to v1.4.1", `name="confirm" value="1"`, `action="/admin/settings/updates/apply"`, `name="_csrf"`, "Cancel"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation page lacks %q", want)
		}
	}
	if strings.Contains(body, "v9.9.9") {
		t.Error("a version from the form reached the page")
	}
	if rec = u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"confirm": {"yes"}}, nil); rec.Code != 200 || f.applies != 0 {
		t.Errorf("a confirm value other than 1 started the update: %d", rec.Code)
	}
	if rec = u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{"confirm": {"1"}}, nil); rec.Code != 303 || f.applies != 1 {
		t.Errorf("confirmed POST = %d, service calls %d", rec.Code, f.applies)
	}
}

func TestUpdatesRetriedMajorStillAsksFirst(t *testing.T) {
	for _, d := range []string{"update_failed", "busy"} {
		f := baseUpdates()
		f.state.Status = UpdatesStatus{Decision: d, Target: "v2.0.0"}
		u := newUpdatesRig(t, f)
		rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{}, nil)
		if rec.Code != 200 || f.applies != 0 || !strings.Contains(rec.Body.String(), "v2.0.0 is a major release") {
			t.Errorf("%s: retry of a major = %d, calls %d; it must ask for confirmation", d, rec.Code, f.applies)
		}
	}
}

func TestUpdatesApplyOfAMinorNeedsNoConfirmation(t *testing.T) {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "scheduled", Target: "v1.4.2"}
	f.applyRes = UpdatesResult{Target: "v1.4.2", Started: true}
	u := newUpdatesRig(t, f)
	if rec := u.do("POST", "/admin/settings/updates/apply", "owner-token", url.Values{}, nil); rec.Code != 303 || f.applies != 1 {
		t.Errorf("minor apply = %d, calls %d", rec.Code, f.applies)
	}
}

func TestUpdatesHistoryLayout(t *testing.T) {
	t.Parallel()

	rows := []UpdatesHistoryRow{
		{When: time.Date(2026, 11, 2, 3, 10, 0, 0, time.UTC), From: "v1.4.0", To: "v1.4.1", Phase: "rolled_back", Error: strings.Repeat("smoke failed: GET / returned 500; ", 12)},
		{When: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), From: "v1.3.9", To: "v1.4.0", Phase: "failed", Error: "disk full"},
	}
	tbl := updatesHistoryTable(rows, time.UTC)
	if len(tbl.Columns) != 2 || tbl.Columns[1].Key != "result" || tbl.Columns[1].Role != "title" {
		t.Fatalf("columns = %+v: the Result column must be the flexible one, and few enough to fit 390 px", tbl.Columns)
	}
	for i, r := range tbl.Rows {
		b := r.Cells[1].Badge
		if b == nil || strings.Contains(b.Label, "smoke") || strings.Contains(b.Label, "disk") {
			t.Errorf("row %d: the badge must be a short label, got %+v", i, b)
		}
		if r.Cells[1].Sub == "" {
			t.Errorf("row %d: the error belongs under the badge", i)
		}
		if b.Kind != "danger" {
			t.Errorf("row %d: %s is %s; a rollback or failure is danger like the status pill", i, b.Label, b.Kind)
		}
	}
}

func TestUpdatesModeHasOneLabel(t *testing.T) {
	body := newUpdatesRig(t, baseUpdates()).get(t)
	if n := strings.Count(body, ">Mode<"); n != 1 {
		t.Errorf("%d elements read Mode, want the radio group's legend only", n)
	}
	if strings.Contains(body, "Update mode") {
		t.Error("a second name for the same field")
	}
	if !strings.Contains(body, `<legend class="hm-field__label">Mode</legend>`) {
		t.Error("the group lost its legend")
	}
}

func TestUpdatesUnavailableDoesNotRepeatThePrefix(t *testing.T) {
	g := newUpdatesRig(t, nil)
	g.a.deps.UpdatesUnavailable = `update agent: update URL "x" must be a plain scheme://host/path`
	body := g.get(t)
	if strings.Contains(body, "did not start: update agent:") || !strings.Contains(body, "did not start: update URL") {
		t.Error("the unavailable card repeats the update agent prefix")
	}
}

// docs/ADMIN_UI_RULES.md names every UI-10 exception; the Updates ones too.
func TestUIRulesListTheUpdatesExceptions(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../docs/ADMIN_UI_RULES.md")
	if err != nil {
		t.Skip("docs not available")
	}
	var rule string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "| UI-10 ") {
			rule = line
		}
	}
	for _, want := range []string{"Settings > Updates", "update policy form", "Check now", "Update now", "Unpin"} {
		if !strings.Contains(rule, want) {
			t.Errorf("UI-10 in ADMIN_UI_RULES.md does not mention %q", want)
		}
	}
}

func backupFailedUpdates() *fakeUpdates {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "backup_failed", Target: "v1.5.0", LastResult: "error", Reason: "pg_dump: error: out of disk"}
	f.applyRes = UpdatesResult{Target: "v1.5.0", Started: true}
	return f
}

func TestUpdatesApplyWithoutBackupAsksFirstThenRuns(t *testing.T) {
	f := backupFailedUpdates()
	u := newUpdatesRig(t, f)
	for _, rec := range []*httptest.ResponseRecorder{
		u.do("GET", "/admin/settings/updates/apply-without-backup", "owner-token", nil, nil),
		u.do("POST", "/admin/settings/updates/apply-without-backup", "owner-token", url.Values{"target": {"v9.9.9"}}, nil),
	} {
		body := rec.Body.String()
		if rec.Code != 200 || f.noBackup != 0 {
			t.Fatalf("before confirming: %d, calls %d", rec.Code, f.noBackup)
		}
		for _, want := range []string{"Update to v1.5.0 without a backup", `name="confirm" value="1"`, `name="target" value="v1.5.0"`, `action="/admin/settings/updates/apply-without-backup"`} {
			if !strings.Contains(body, want) {
				t.Errorf("confirm page lacks %q", want)
			}
		}
		if strings.Contains(body, "v9.9.9") {
			t.Error("a version from the form reached the page")
		}
	}
	rec := u.do("POST", "/admin/settings/updates/apply-without-backup", "owner-token", url.Values{"confirm": {"1"}, "target": {"v1.5.0"}}, nil)
	if rec.Code != 303 || f.noBackup != 1 || f.applies != 0 {
		t.Fatalf("confirmed = %d, override calls %d, plain calls %d", rec.Code, f.noBackup, f.applies)
	}
	if len(u.events) != 1 || u.events[0].Action != "updates.apply_without_backup" || u.events[0].Target != "v1.5.0" {
		t.Errorf("audit = %+v", u.events)
	}
}

func TestUpdatesApplyWithoutBackupRefusesAChangedTarget(t *testing.T) {
	for name, form := range map[string]url.Values{
		"other version": {"confirm": {"1"}, "target": {"v1.4.9"}},
		"no target":     {"confirm": {"1"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := backupFailedUpdates()
			u := newUpdatesRig(t, f)
			rec := u.do("POST", "/admin/settings/updates/apply-without-backup", "owner-token", form, nil)
			if rec.Code != 303 || f.noBackup != 0 || len(u.events) != 0 {
				t.Fatalf("mismatch = %d, override calls %d, audit %+v", rec.Code, f.noBackup, u.events)
			}
			if body := followFlashTo(u.ownerRig, "/admin/settings/updates", rec); !strings.Contains(body, "changed. Review it and try again.") {
				t.Error("no mismatch flash")
			}
		})
	}
}

func TestUpdatesApplyWithoutBackupOnlyInTheBackupFailedState(t *testing.T) {
	f := baseUpdates()
	f.state.Status = UpdatesStatus{Decision: "update_failed", Target: "v1.5.0"}
	u := newUpdatesRig(t, f)
	rec := u.do("POST", "/admin/settings/updates/apply-without-backup", "owner-token", url.Values{"confirm": {"1"}}, nil)
	if rec.Code != 303 || f.noBackup != 0 || len(u.events) != 0 {
		t.Errorf("override outside backup_failed = %d, calls %d", rec.Code, f.noBackup)
	}
	if body := u.get(t); strings.Contains(body, "without a backup") {
		t.Error("the override is offered outside the backup_failed state")
	}
}

func TestUpdatesStatusStripAndTracker(t *testing.T) {
	loc := time.UTC
	if tr := updatesHistoryTracker(nil, loc); len(tr.Blocks) != 0 || tr.Label == "" {
		t.Errorf("empty history tracker %+v", tr)
	}
	rows := []UpdatesHistoryRow{
		{When: time.Date(2026, 11, 2, 3, 10, 0, 0, time.UTC), From: "v1.4.0", To: "v1.4.1", Phase: "rolled_back"},
		{When: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), From: "v1.3.9", To: "v1.4.0", Phase: "success"},
		{When: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), From: "v1.3.8", To: "v1.3.9", Phase: "failed"},
	}
	tr := updatesHistoryTracker(rows, loc)
	var kinds []string
	for _, b := range tr.Blocks {
		kinds = append(kinds, b.Kind)
	}
	if got := strings.Join(kinds, ","); got != "danger,success,warning" {
		t.Errorf("tracker kinds (oldest first) = %s", got)
	}
	// With no history the card holds the table's empty state and no tracker.
	body := newUpdatesRig(t, baseUpdates()).get(t)
	if !strings.Contains(body, `id="updates-history"`) || strings.Contains(body, "hm-tracker") || !strings.Contains(body, "No updates yet.") {
		t.Error("empty history is not an empty state inside its card")
	}
}
