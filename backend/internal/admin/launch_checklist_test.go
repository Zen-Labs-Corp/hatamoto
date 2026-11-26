package admin

import (
	"context"
	"errors"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"strings"
	"testing"
)

// freshLaunch is a fresh install: every source wired, nothing set up, the page
// cache on by default.
func freshLaunch() launchState {
	return launchState{
		SiteKnown: true, SiteName: "Acme",
		HomeKnown: true, HomeID: fid(9),
		PostsKnown: true, ThemeKnown: true,
		MailKnown: true, FormsKnown: true,
		CacheKnown: true, CacheOn: true,
		BackupsKnown: true, OffsiteKnown: true,
		TwoFAKnown: true, SEOKnown: true,
	}
}

func launchByTitle(items []launchItem) map[string]launchItem {
	m := map[string]launchItem{}
	for _, it := range items {
		m[it.Title] = it
	}
	return m
}

func TestLaunchItemsFreshInstall(t *testing.T) {
	t.Parallel()
	items := launchItems("/admin", freshLaunch())
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	want := "Site name and logo|Edit the home page|Write your first post|Theme|Email delivery|Contact form|Page cache|Backups|Two-factor sign-in|Search engines"
	if got := strings.Join(titles, "|"); got != want {
		t.Fatalf("order\n got %s\nwant %s", got, want)
	}
	if done, total := launchProgress(items); done != 4 || total != 10 {
		t.Errorf("progress %d of %d, want 4 of 10", done, total)
	}
	wantState := map[string]string{
		"Site name and logo": launchDone, "Edit the home page": launchTodo, "Write your first post": launchTodo,
		"Theme": launchDone, "Email delivery": launchWarn, "Contact form": launchTodo, "Page cache": launchDone,
		"Backups": launchWarn, "Two-factor sign-in": launchTodo, "Search engines": launchDone,
	}
	for _, it := range items {
		if it.State != wantState[it.Title] {
			t.Errorf("%s: state %q, want %q", it.Title, it.State, wantState[it.Title])
		}
	}
	by := launchByTitle(items)
	if got := by["Edit the home page"].Action.Href; got != "/admin/entities/"+fid(9).String()+"/edit" {
		t.Errorf("home link %q", got)
	}
	if got := by["Two-factor sign-in"].Action.Href; got != "/admin"+signInPath {
		t.Errorf("2FA link %q", got)
	}
}

func TestLaunchItemsFlipToDone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		title string
		flip  func(*launchState)
	}{
		{"Edit the home page", func(s *launchState) { s.HomeEdited = true }},
		{"Write your first post", func(s *launchState) { s.Posts = 1 }},
		{"Email delivery", func(s *launchState) { s.MailOK = true }},
		{"Contact form", func(s *launchState) { s.Forms = 1 }},
		{"Page cache", func(s *launchState) { s.CacheOn = false }}, // flips the other way, below
		{"Backups", func(s *launchState) { s.BackupOK, s.OffsiteOn = true, true }},
		{"Two-factor sign-in", func(s *launchState) { s.TwoFARequired = true }},
	} {
		s := freshLaunch()
		if tc.title == "Page cache" {
			s.CacheOn = false
			if it := launchByTitle(launchItems("/admin", s))[tc.title]; it.State != launchTodo || it.Action == nil {
				t.Errorf("%s off: %+v", tc.title, it)
			}
			continue
		}
		if it := launchByTitle(launchItems("/admin", s))[tc.title]; it.State == launchDone {
			t.Fatalf("%s starts done", tc.title)
		}
		tc.flip(&s)
		if it := launchByTitle(launchItems("/admin", s))[tc.title]; it.State != launchDone || it.Action != nil {
			t.Errorf("%s after flip: %+v", tc.title, it)
		}
	}
}

func TestLaunchItemsBackupsStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		ok, off      bool
		offsiteKnown bool
		state, href  string
	}{
		{"nothing", false, false, true, launchWarn, "/admin/settings/backups"},
		{"backup only", true, false, true, launchWarn, "/admin/settings/backups#offsite"},
		{"offsite only", false, true, true, launchWarn, "/admin/settings/backups"},
		{"both", true, true, true, launchDone, ""},
		{"backup, no offsite service", true, false, false, launchDone, ""},
	} {
		s := freshLaunch()
		s.BackupOK, s.OffsiteOn, s.OffsiteKnown = tc.ok, tc.off, tc.offsiteKnown
		it := launchByTitle(launchItems("/admin", s))["Backups"]
		if it.State != tc.state || (tc.href == "") != (it.Action == nil) || (it.Action != nil && it.Action.Href != tc.href) {
			t.Errorf("%s: %+v", tc.name, it)
		}
	}
}

func TestLaunchItemsOptionalActionsDrop(t *testing.T) {
	t.Parallel()
	s := freshLaunch()
	by := launchByTitle(launchItems("/admin", s))
	if it := by["Site name and logo"]; it.Action == nil || !strings.Contains(it.Detail, "No logo yet.") {
		t.Errorf("no logo: %+v", it)
	}
	if it := by["Search engines"]; it.Action == nil || it.Note == "" || it.Action.Href != "/admin"+seoVerificationPath {
		t.Errorf("unverified: %+v", it)
	}
	s.HasLogo, s.Verified = true, true
	by = launchByTitle(launchItems("/admin", s))
	for _, title := range []string{"Site name and logo", "Search engines"} {
		it := by[title]
		if it.State != launchDone || it.Action != nil || it.Note != "" || strings.Contains(it.Detail, "No logo") {
			t.Errorf("%s: %+v", title, it)
		}
	}
}

func TestLaunchItemsUnknownSourcesAreOmitted(t *testing.T) {
	t.Parallel()
	if items := launchItems("/admin", launchState{}); len(items) != 0 {
		t.Fatalf("%d items for nothing known", len(items))
	}
	s := freshLaunch()
	s.FormsKnown = false
	items := launchItems("/admin", s)
	if _, total := launchProgress(items); total != 9 {
		t.Errorf("total %d, want 9", total)
	}
	if _, ok := launchByTitle(items)["Contact form"]; ok {
		t.Error("forms item shown without its source")
	}
}

func TestLaunchItemsButtonsAndLinks(t *testing.T) {
	t.Parallel()
	for _, s := range []launchState{freshLaunch(), {}} {
		for _, it := range launchItems("/cp", s) {
			if it.Href != "" && !strings.HasPrefix(it.Href, "/cp/") {
				t.Errorf("%s: title link %q", it.Title, it.Href)
			}
			if it.Action == nil {
				continue
			}
			if !strings.HasPrefix(it.Action.Href, "/cp/") {
				t.Errorf("%s: link %q", it.Title, it.Action.Href)
			}
			if primary := it.Action.Variant == "primary"; primary != (it.State == launchWarn) {
				t.Errorf("%s: variant %q on a %s row", it.Title, it.Action.Variant, it.State)
			}
			if it.State == launchWarn && it.Title != "Email delivery" && it.Title != "Backups" {
				t.Errorf("%s must not warn", it.Title)
			}
		}
	}
}

// launchFailingForms is a FormStore whose list read fails.
type launchFailingForms struct{ FormStore }

func (launchFailingForms) ListForms(context.Context, string) ([]*form.Form, error) {
	return nil, errors.New("boom")
}

func TestReadLaunchStateOneFailingReadIsLeftOut(t *testing.T) {
	t.Parallel()
	ents := newFakeEntities()
	ents.addSchema(1, "Post", "post", `{"type":"object"}`, `{}`, true)
	g := newRig(t, func(d *Deps) {
		d.Content.Entities = ents
		d.Growth.Forms = launchFailingForms{}
		d.Backups = &fakeBackups{state: BackupsState{Rows: []BackupInfo{{State: BackupOK}}}}
	})
	s := g.a.readLaunchState(context.Background())
	if s.FormsKnown {
		t.Error("a failed forms read must leave forms unknown")
	}
	if !s.SiteKnown || s.SiteName != "Acme Site" {
		t.Errorf("site %+v", s)
	}
	if !s.BackupsKnown || !s.BackupOK || s.OffsiteKnown {
		t.Errorf("backups %+v", s)
	}
	if !s.HomeKnown || !s.HomeEdited {
		t.Errorf("a missing home page counts as done: %+v", s)
	}
	if !s.PostsKnown || s.Posts != 0 {
		t.Errorf("posts %+v", s)
	}
	if s.MailKnown || s.CacheKnown || s.SEOKnown || s.TwoFAKnown || s.ThemeKnown {
		t.Errorf("unwired sources must stay unknown: %+v", s)
	}
}

// launchBlockedForms is a FormStore whose list read hangs until released.
type launchBlockedForms struct {
	FormStore
	release chan struct{}
}

func (f launchBlockedForms) ListForms(context.Context, string) ([]*form.Form, error) {
	<-f.release
	return nil, nil
}

// A source that hangs past the page deadline is left out; the card still renders.
func TestDashLaunchSlowReadIsLeftOutNotAnError(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	g := newRig(t, func(d *Deps) {
		d.Growth.Forms = launchBlockedForms{release: release}
		d.Backups = &fakeBackups{} // no backup yet: an open item, so the card shows
	})
	ctx, cancel := context.WithTimeout(context.Background(), dashTimeout)
	defer cancel()
	v := g.a.buildDash(ctx, []dashJob{*g.a.launchJob()})
	if len(v.Sections) != 1 || v.Sections[0].State == dashError || v.Sections[0].Launch == nil {
		t.Fatalf("a slow read must leave the item out, not turn the card into an error: %+v", v.Sections)
	}
	res := dashResult{Sec: v.Sections[0]}
	for _, grp := range res.Sec.Launch.Groups {
		for _, it := range grp.Items {
			if it.Title == "Contact form" {
				t.Errorf("the slow forms read must leave the Contact form row out")
			}
		}
	}
}

// The home page item follows the homepage picked in Settings, not the slug "home".
func TestLaunchHomeFollowsConfiguredHomepage(t *testing.T) {
	t.Parallel()
	const seed = `{"page_sections":[{"id":"welcome"}]}`
	const edited = `{"page_sections":[{"id":"hero"},{"id":"cta"}]}`
	ents := newFakeEntities()
	page := ents.addSchema(1, "Page", "page", `{"type":"object"}`, `{}`, true)
	ents.addEntity(10, page, "Home", "home", entity.StatusPublished, seed, t0)
	ents.addEntity(11, page, "Landing", "landing", entity.StatusPublished, edited, t0)
	g := newRig(t, func(d *Deps) { d.Content.Entities = ents })
	site := g.a.deps.Site.(*fakeSite)

	run := func() launchState {
		t.Helper()
		apply, err := g.a.launchHome(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var s launchState
		apply(&s)
		return s
	}
	if s := run(); s.HomeEdited || s.HomeID != fid(10) {
		t.Errorf("without a chosen homepage the slug home is read: %+v", s)
	}
	site.cfg.HomepageEntityID = fid(11)
	if s := run(); !s.HomeEdited || s.HomeID != fid(11) {
		t.Errorf("the chosen homepage must be read, not the untouched home page: %+v", s)
	}
	site.cfg.HomepageEntityID = fid(99)
	if s := run(); !s.HomeKnown || !s.HomeEdited {
		t.Errorf("a missing chosen homepage counts as done: %+v", s)
	}
}
