package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// The launch checklist groups and item states.
const (
	launchGroupYours = "Make it yours"
	launchGroupRun   = "Run it properly"

	launchDone = "done"
	launchTodo = "todo"
	launchWarn = "warn"
)

// launchState is everything the launch checklist reads, gathered once per
// Overview. A false ...Known flag means the source is not wired or could not
// be read: its item is left out.
type launchState struct {
	SiteKnown bool
	SiteName  string
	HasLogo   bool

	HomeKnown  bool
	HomeEdited bool
	HomeID     uuid.UUID

	PostsKnown bool
	Posts      int64

	ThemeKnown bool

	MailKnown, MailOK bool

	FormsKnown bool
	Forms      int

	CacheKnown, CacheOn bool

	BackupsKnown, BackupOK  bool
	OffsiteKnown, OffsiteOn bool

	TwoFAKnown, TwoFARequired bool

	SEOKnown bool
	Verified bool
}

// launchItem is one row of the launch checklist.
type launchItem struct {
	Group  string
	Title  string
	Href   string // the title links here when set
	Detail string
	Note   string // an optional second line
	State  string // launchDone, launchTodo or launchWarn
	// Action is the button that fixes the item; nil when there is nothing to do.
	// Only a launchWarn row gets a primary button.
	Action *ui.Button
}

// launchButton is the row button: primary for a row that needs attention,
// secondary otherwise.
func launchButton(state, label, href string) *ui.Button {
	v := "secondary"
	if state == launchWarn {
		v = "primary"
	}
	return &ui.Button{Label: label, Href: href, Variant: v, Size: "sm"}
}

// launchItems turns the state into the checklist rows, in display order. It is
// a pure function of s, so the table tests drive it directly.
func launchItems(adminPath string, s launchState) []launchItem {
	var out []launchItem
	add := func(it launchItem) { out = append(out, it) }

	if s.SiteKnown {
		it := launchItem{Group: launchGroupYours, Title: "Site name and logo", State: launchDone,
			Detail: fmt.Sprintf("Set to %q.", s.SiteName)}
		if !s.HasLogo {
			it.Detail += " No logo yet."
			it.Action = launchButton(it.State, "Add logo", adminPath+"/settings/general")
		}
		add(it)
	}
	if s.HomeKnown {
		it := launchItem{Group: launchGroupYours, Title: "Edit the home page"}
		if s.HomeEdited {
			it.State, it.Detail = launchDone, "Home page replaced."
		} else {
			it.State, it.Detail = launchTodo, "Still the welcome page from first boot."
			it.Action = launchButton(it.State, "Edit page", adminPath+"/entities/"+s.HomeID.String()+"/edit")
		}
		add(it)
	}
	if s.PostsKnown {
		it := launchItem{Group: launchGroupYours, Title: "Write your first post"}
		if s.Posts > 0 {
			it.State, it.Detail = launchDone, "A post is published."
		} else {
			it.State, it.Detail = launchTodo, "The blog is empty."
			it.Action = launchButton(it.State, "New post", adminPath+"/entities/new?type=post")
		}
		add(it)
	}
	if s.ThemeKnown {
		add(launchItem{Group: launchGroupYours, Title: "Theme", Href: adminPath + "/theme/themes",
			State: launchDone, Detail: "A theme is active."})
	}
	if s.MailKnown {
		it := launchItem{Group: launchGroupRun, Title: "Email delivery"}
		if s.MailOK {
			it.State, it.Detail = launchDone, "Email is set up."
		} else {
			it.State, it.Detail = launchWarn, "Not set up. Form notifications and password resets won't be sent."
			it.Action = launchButton(it.State, "Set up email", adminPath+emailPath)
		}
		add(it)
	}
	if s.FormsKnown {
		it := launchItem{Group: launchGroupRun, Title: "Contact form"}
		if s.Forms > 0 {
			it.State, it.Detail = launchDone, "A form is ready."
		} else {
			it.State, it.Detail = launchTodo, "No forms yet."
			it.Action = launchButton(it.State, "Add a form", adminPath+"/forms/new")
		}
		add(it)
	}
	if s.CacheKnown {
		it := launchItem{Group: launchGroupRun, Title: "Page cache"}
		if s.CacheOn {
			it.State, it.Detail = launchDone, "On. Pages are served from the cache."
		} else {
			it.State, it.Detail = launchTodo, "Off. Every page is rendered on each visit."
			it.Action = launchButton(it.State, "Turn on", adminPath+"/settings/performance")
		}
		add(it)
	}
	if s.BackupsKnown {
		add(launchBackups(adminPath, s))
	}
	if s.TwoFAKnown {
		it := launchItem{Group: launchGroupRun, Title: "Two-factor sign-in"}
		if s.TwoFARequired {
			it.State, it.Detail = launchDone, "Required at sign-in."
		} else {
			it.State, it.Detail = launchTodo, "Not required for admins."
			it.Action = launchButton(it.State, "Require it", adminPath+signInPath)
		}
		add(it)
	}
	if s.SEOKnown {
		it := launchItem{Group: launchGroupRun, Title: "Search engines", State: launchDone,
			Detail: "Sitemap and robots.txt are live."}
		if !s.Verified {
			it.Note = "Search Console not verified."
			it.Action = launchButton(it.State, "Verify", adminPath+seoVerificationPath)
		}
		add(it)
	}
	return out
}

// launchBackups is the Backups row: done with an OK backup and, when off-site
// copies are wired, an off-site copy. Anything less is a warning.
func launchBackups(adminPath string, s launchState) launchItem {
	it := launchItem{Group: launchGroupRun, Title: "Backups"}
	missingOffsite := s.OffsiteKnown && !s.OffsiteOn
	switch {
	case s.BackupOK && !missingOffsite:
		it.State, it.Detail = launchDone, "Backed up."
		if s.OffsiteKnown {
			it.Detail = "Backed up and copied off-site."
		}
		return it
	case s.BackupOK:
		it.Detail = "Backed up, but no off-site copy."
		it.Action = launchButton(launchWarn, "Set up backups", adminPath+"/settings/backups#offsite")
	case missingOffsite:
		it.Detail = "No backup yet, and no off-site copy."
		it.Action = launchButton(launchWarn, "Set up backups", adminPath+"/settings/backups")
	default:
		it.Detail = "No completed backup yet."
		it.Action = launchButton(launchWarn, "Set up backups", adminPath+"/settings/backups")
	}
	it.State = launchWarn
	return it
}

// launchProgress counts the finished items and all of them.
func launchProgress(items []launchItem) (done, total int) {
	for _, it := range items {
		if it.State == launchDone {
			done++
		}
	}
	return done, len(items)
}

// launchRead is one source read: it returns what to set on the state, or an
// error that leaves the source unknown.
type launchRead struct {
	name string
	run  func(ctx context.Context) (func(*launchState), error)
}

// launchReads are the reads whose service is wired. Each is independent.
func (a *Admin) launchReads() []launchRead {
	d := a.deps
	var rs []launchRead
	if d.Site != nil {
		rs = append(rs, launchRead{"site", func(ctx context.Context) (func(*launchState), error) {
			c, err := d.Site.Get(ctx)
			if err != nil {
				return nil, err
			}
			return func(s *launchState) { s.SiteKnown, s.SiteName, s.HasLogo = true, c.CompanyName, c.LogoURL != "" }, nil
		}})
	}
	if d.Content.Entities != nil {
		rs = append(rs, launchRead{"home page", a.launchHome}, launchRead{"posts", a.launchPosts})
	}
	if d.Growth.Packages != nil {
		rs = append(rs, launchRead{"theme", func(context.Context) (func(*launchState), error) {
			return func(s *launchState) { s.ThemeKnown = true }, nil
		}})
	}
	if d.EmailSettings != nil {
		rs = append(rs, launchRead{"email", func(context.Context) (func(*launchState), error) {
			ok := a.mailConfigured()
			return func(s *launchState) { s.MailKnown, s.MailOK = true, ok }, nil
		}})
	}
	if d.Growth.Forms != nil {
		rs = append(rs, launchRead{"forms", func(ctx context.Context) (func(*launchState), error) {
			forms, err := d.Growth.Forms.ListForms(ctx, "")
			if err != nil {
				return nil, err
			}
			return func(s *launchState) { s.FormsKnown, s.Forms = true, len(forms) }, nil
		}})
	}
	if d.PageCache != nil {
		rs = append(rs, launchRead{"page cache", func(ctx context.Context) (func(*launchState), error) {
			c, err := d.PageCache.Settings(ctx)
			if err != nil {
				return nil, err
			}
			return func(s *launchState) { s.CacheKnown, s.CacheOn = true, c.Enabled }, nil
		}})
	}
	if d.Backups != nil {
		rs = append(rs, launchRead{"backups", a.launchBackupsRead})
	}
	if d.TwoFactor != nil && d.Site != nil {
		rs = append(rs, launchRead{"two-factor policy", func(ctx context.Context) (func(*launchState), error) {
			p, err := a.twoFactorPolicy(ctx)
			if err != nil {
				return nil, err
			}
			return func(s *launchState) { s.TwoFAKnown, s.TwoFARequired = true, p != siteconfig.TwoFactorOptional }, nil
		}})
	}
	if d.SEO != nil {
		rs = append(rs, launchRead{"seo", func(ctx context.Context) (func(*launchState), error) {
			c, err := d.SEO.Get(ctx)
			if err != nil {
				return nil, err
			}
			return func(s *launchState) { s.SEOKnown, s.Verified = true, !c.Verification.IsZero() }, nil
		}})
	}
	return rs
}

// launchHome reads whether the first-boot home page was replaced. A site
// without a home page counts as done.
func (a *Admin) launchHome(ctx context.Context) (func(*launchState), error) {
	e, err := a.deps.Content.Entities.GetEntityBySlug(ctx, "home")
	switch {
	case errors.Is(err, entity.ErrEntityNotFound):
		return func(s *launchState) { s.HomeKnown, s.HomeEdited = true, true }, nil
	case err != nil:
		return nil, err
	}
	return func(s *launchState) { s.HomeKnown, s.HomeEdited, s.HomeID = true, !starter.IsSeed(e), e.ID }, nil
}

// launchPosts counts the published posts. A site without a post type leaves
// the item out, as there is nowhere to write one.
func (a *Admin) launchPosts(ctx context.Context) (func(*launchState), error) {
	c, err := a.dashCounts(ctx)
	if err != nil {
		return nil, err
	}
	var post *entity.EntitySchema
	for _, sc := range c.schemas {
		if sc.Slug == "post" {
			post = sc
			break
		}
	}
	if post == nil {
		return func(*launchState) {}, nil
	}
	var n int64
	for _, r := range c.rows {
		if r.SchemaID == post.ID && r.Status == entity.StatusPublished {
			n += int64(r.Count)
		}
	}
	return func(s *launchState) { s.PostsKnown, s.Posts = true, n }, nil
}

// launchBackupsRead reads the backups and, when wired, the off-site copy.
func (a *Admin) launchBackupsRead(ctx context.Context) (func(*launchState), error) {
	st, err := a.dashBackups(ctx)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, b := range st.Rows {
		if b.State == BackupOK {
			ok = true
			break
		}
	}
	offKnown, offOn := false, false
	if a.deps.Offsite != nil {
		v, err := a.dashOffsite(ctx)
		if err != nil {
			return nil, err
		}
		offKnown, offOn = true, v.Enabled
	}
	return func(s *launchState) {
		s.BackupsKnown, s.BackupOK, s.OffsiteKnown, s.OffsiteOn = true, ok, offKnown, offOn
	}, nil
}

// readLaunchState runs one small read per source in parallel. A source that
// is not wired or fails is left unknown (a failure is logged), never an error.
func (a *Admin) readLaunchState(ctx context.Context) launchState {
	reads := a.launchReads()
	applies := make([]func(*launchState), len(reads))
	fns := make([]func(context.Context) error, len(reads))
	for i, r := range reads {
		fns[i] = func(ctx context.Context) error {
			f, err := r.run(ctx)
			applies[i] = f
			return err
		}
	}
	errs := a.runDash(ctx, fns)
	var s launchState
	for i, r := range reads {
		// applies[i] is only safe to read when the read finished without error.
		if errs[i] != nil {
			a.logger().Warn("admin: launch checklist read failed", "item", r.name, "err", errs[i])
			continue
		}
		applies[i](&s)
	}
	return s
}

// launchGroup is the items of one group, in display order.
type launchGroup struct {
	Name  string
	Items []launchItem
}

// launchCard is the model of the Overview's launch checklist card.
type launchCard struct {
	Groups   []launchGroup
	Progress ui.Progress
	HideHref string // the POST that hides the card; empty when the site store is not wired
}

// launchJob is the launch checklist card, for users who manage settings.
func (a *Admin) launchJob() *dashJob {
	return &dashJob{Name: "launch checklist", SecPos: 10, Run: a.dashLaunch,
		Fallback: dashSection{ID: "dash-launch", Title: "Launch checklist", ErrMsg: "The launch checklist could not be built."}}
}

// dashLaunch builds the card. It returns no card when the owner hid it, when no
// item can be shown, or when every shown item is done.
func (a *Admin) dashLaunch(ctx context.Context) (dashResult, error) {
	if a.deps.Site != nil {
		cfg, err := a.deps.Site.Get(ctx)
		if err != nil {
			return dashResult{}, err
		}
		if launchHidden(cfg) {
			return dashResult{}, nil
		}
	}
	items := launchItems(a.adminPath, a.readLaunchState(ctx))
	done, total := launchProgress(items)
	if total == 0 || done == total {
		return dashResult{}, nil
	}
	card := &launchCard{Progress: ui.Progress{Label: "Launch checklist progress", Value: fmt.Sprintf("%d of %d", done, total),
		Pct: float64(done) / float64(total) * 100, Kind: "info", Thin: true}}
	if a.deps.Site != nil {
		card.HideHref = a.adminPath + launchHidePath
	}
	for _, it := range items {
		if n := len(card.Groups); n == 0 || card.Groups[n-1].Name != it.Group {
			card.Groups = append(card.Groups, launchGroup{Name: it.Group})
		}
		g := &card.Groups[len(card.Groups)-1]
		g.Items = append(g.Items, it)
	}
	return dashResult{Sec: &dashSection{ID: "dash-launch", Title: "Launch checklist", Description: "Finish these before you share the site.",
		State: dashOK, Launch: card}}, nil
}
