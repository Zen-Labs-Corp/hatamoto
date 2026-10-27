package admin

import (
	"context"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// Site health thresholds (approved by the owner of the product): a backup is
// fine up to two days old, worth a look up to a week, and a problem beyond
// that or when the latest one failed; free disk below 1 GiB is a problem, below
// 5 GiB worth a look.
const (
	healthBackupOK    = 2 * 24 * time.Hour
	healthBackupAmber = 7 * 24 * time.Hour
	healthDiskRed     = 1 << 30
	healthDiskAmber   = 5 << 30
)

// healthTimeout bounds the whole card a little under dashTimeout, so a slow
// check is left out of the card instead of failing it.
const healthTimeout = dashTimeout - 500*time.Millisecond

// Health levels of one check.
const (
	healthOK   = "ok"
	healthWarn = "warn"
	healthBad  = "bad"
)

// Health groups, in display order.
const (
	healthData     = "Data"
	healthDelivery = "Delivery"
	healthCrawlers = "AI crawlers"
	healthSite     = "Site"
)

var healthGroupOrder = []string{healthData, healthDelivery, healthCrawlers, healthSite}

// healthCheck is one line of the card. Href is where to fix it; the card only
// links a check that is not ok.
type healthCheck struct {
	Group, Label, Value, Level, Href string
}

// healthItem is a check as the template draws it.
type healthItem struct {
	Label string
	Href  string // set for a check that is not ok
	Badge ui.Badge
}

// healthGroup is one named block of checks.
type healthGroup struct {
	Name  string
	Items []healthItem
}

var healthKinds = map[string]string{healthOK: "success", healthWarn: "warning", healthBad: "danger"}

// healthCheckFn is one check. It returns nil when there is nothing to report
// (no data yet) and an error when its store failed; both leave it out.
type healthCheckFn func(ctx context.Context) (*healthCheck, error)

// healthChecks are the checks whose store is wired. Each is independent.
func (a *Admin) healthChecks() []healthCheckFn {
	var fns []healthCheckFn
	if a.deps.Backups != nil {
		fns = append(fns, a.healthBackup, a.healthDisk)
	}
	if a.deps.Webhooks != nil {
		fns = append(fns, a.healthWebhooks)
	}
	if a.deps.Growth.Forms != nil {
		fns = append(fns, a.healthSpam)
	}
	if a.deps.Growth.Agents != nil {
		fns = append(fns, a.healthVerified, a.healthErrors, a.healthRobots)
	}
	if a.deps.Growth.Plugins != nil {
		fns = append(fns, a.healthPlugins)
	}
	if a.deps.Growth.Agents != nil {
		fns = append(fns, a.healthPublished)
	}
	if a.deps.Updates != nil {
		fns = append(fns, a.healthUpdates)
	}
	return fns
}

// healthRange is the last 7 days, the window of every crawler check.
func healthRange() agents.Range {
	end := today()
	return agents.Range{From: end.AddDate(0, 0, -(dashWindowDays - 1)), To: end}
}

// latestBackup is the newest backup that is not still running; ok is false
// when there is none.
func latestBackup(st BackupsState) (BackupInfo, bool) {
	for _, b := range st.Rows {
		if b.State != BackupRunning {
			return b, true
		}
	}
	return BackupInfo{}, false
}

func (a *Admin) healthBackup(ctx context.Context) (*healthCheck, error) {
	st, err := a.deps.Backups.List(ctx)
	if err != nil {
		return nil, err
	}
	c := &healthCheck{Group: healthData, Label: "Last backup", Href: a.adminPath + "/settings/backups"}
	b, ok := latestBackup(st)
	if !ok {
		c.Value, c.Level = "No backups yet", healthWarn
		return c, nil
	}
	return backupCheck(c, b, analyticsNow().Sub(b.Created)), nil
}

// backupCheck grades the latest backup: a failed one or one older than a week
// is bad, older than two days warn.
func backupCheck(c *healthCheck, b BackupInfo, age time.Duration) *healthCheck {
	switch {
	case b.State == BackupFailed:
		c.Value, c.Level = "Failed", healthBad
	case age > healthBackupAmber:
		c.Value, c.Level = agoText(age), healthBad
	case age > healthBackupOK:
		c.Value, c.Level = agoText(age), healthWarn
	default:
		c.Value, c.Level = agoText(age), healthOK
	}
	return c
}

func (a *Admin) healthDisk(ctx context.Context) (*healthCheck, error) {
	st, err := a.deps.Backups.List(ctx)
	if err != nil || st.FreeBytes == 0 { // 0: unknown
		return nil, err
	}
	c := &healthCheck{Group: healthData, Label: "Free disk", Value: backupSize(int64(st.FreeBytes)) + " free", Href: a.adminPath + "/settings/backups"}
	switch {
	case st.FreeBytes < healthDiskRed:
		c.Level = healthBad
	case st.FreeBytes < healthDiskAmber:
		c.Level = healthWarn
	default:
		c.Level = healthOK
	}
	return c, nil
}

func (a *Admin) healthWebhooks(ctx context.Context) (*healthCheck, error) {
	n, err := a.deps.Webhooks.CountFailedSince(ctx, analyticsNow().AddDate(0, 0, -dashWindowDays))
	if err != nil {
		return nil, err
	}
	c := &healthCheck{Group: healthDelivery, Label: "Webhooks", Level: healthOK, Href: a.adminPath + "/settings/webhooks"}
	switch {
	case n > 0:
		c.Value, c.Level, c.Href = fmt.Sprintf("%d failed this week", n), healthWarn, a.adminPath+"/settings/webhooks/deliveries"
	case !a.deps.WebhookDeliveryOn:
		c.Value, c.Level = "Delivery is off", healthWarn
	default:
		c.Value = "None failed this week"
	}
	return c, nil
}

func (a *Admin) healthSpam(ctx context.Context) (*healthCheck, error) {
	sc, err := a.scanSubmissions(ctx)
	if err != nil {
		return nil, err
	}
	v := fmt.Sprintf("%d caught this week", len(sc.spam))
	if sc.partial {
		v = fmt.Sprintf("%d+ caught this week", len(sc.spam))
	}
	return &healthCheck{Group: healthDelivery, Label: "Spam filter", Value: v, Level: healthOK, Href: a.adminPath + spamPath}, nil
}

func (a *Admin) healthVerified(ctx context.Context) (*healthCheck, error) {
	t, err := a.deps.Growth.Agents.Overview(ctx, healthRange())
	if err != nil || t.Verified+t.Spoofed+t.Unverifiable == 0 {
		return nil, err
	}
	return &healthCheck{Group: healthCrawlers, Label: "Verified fetches", Value: pct(t.Verified, t.Spoofed, t.Unverifiable), Level: healthOK, Href: a.adminPath + "/analytics/health#w-h-ver"}, nil
}

func (a *Admin) healthErrors(ctx context.Context) (*healthCheck, error) {
	days, err := a.deps.Growth.Agents.HealthSeries(ctx, healthRange())
	if err != nil {
		return nil, err
	}
	var failed, total int64
	for _, d := range days {
		failed += d.S4xx + d.S5xx
		total += d.Hits
	}
	if total == 0 {
		return nil, nil
	}
	c := &healthCheck{Group: healthCrawlers, Label: "Errors served", Value: fixed(ratio(failed, total), 1), Href: a.adminPath + "/analytics/health#w-h-fail"}
	switch trackerKind(failed, total) {
	case "danger":
		c.Level = healthBad
	case "warning":
		c.Level = healthWarn
	default:
		c.Level = healthOK
	}
	return c, nil
}

func (a *Admin) healthRobots(ctx context.Context) (*healthCheck, error) {
	rows, err := a.deps.Growth.Agents.Agents(ctx, healthRange())
	if err != nil {
		return nil, err
	}
	reg := a.registry()
	fetched, ignoring := 0, 0
	for _, r := range rows {
		if r.Hits == 0 {
			continue
		}
		fetched++
		if ag := reg.ByID(r.AgentID); ag != nil && ag.IgnoresRobots {
			ignoring++
		}
	}
	if fetched == 0 {
		return nil, nil
	}
	c := &healthCheck{Group: healthCrawlers, Label: "robots.txt", Level: healthOK, Value: "All agents honour it", Href: a.adminPath + "/analytics/crawlers#w-cr-list"}
	if ignoring > 0 {
		c.Level, c.Value = healthWarn, countNoun(ignoring, "agent")+" ignore it"
		if ignoring == 1 {
			c.Value = "1 agent ignores it"
		}
	}
	return c, nil
}

func (a *Admin) healthPlugins(context.Context) (*healthCheck, error) {
	list := a.deps.Growth.Plugins.Plugins()
	if len(list) == 0 {
		return nil, nil
	}
	active, broken := 0, 0
	for _, p := range list {
		if p.Error != "" || p.DataError != "" {
			broken++
		} else if p.Loaded {
			active++
		}
	}
	c := &healthCheck{Group: healthSite, Label: "Plugins", Level: healthOK, Value: fmt.Sprintf("%d active", active), Href: a.adminPath + "/plugins"}
	if broken > 0 {
		c.Level, c.Value = healthWarn, c.Value+fmt.Sprintf(", %d in error", broken)
	}
	return c, nil
}

// healthPublished is the count of published URLs: a stand-in for the sitemap
// size (it ignores excludes and plugin URLs), so it is labelled as what it is.
func (a *Admin) healthPublished(ctx context.Context) (*healthCheck, error) {
	n, err := a.deps.Growth.Agents.PublishedPages(ctx)
	if err != nil {
		return nil, err
	}
	return &healthCheck{Group: healthSite, Label: "Published URLs", Value: fmtNum(int64(n)), Level: healthOK, Href: a.adminPath + "/entities?status=published"}, nil
}

func (a *Admin) healthUpdates(ctx context.Context) (*healthCheck, error) {
	g, err := a.deps.Updates.Glance(ctx)
	if err != nil || g.Dev {
		return nil, err
	}
	st := UpdatesState{Running: g.Running, Mode: g.Mode, Status: g.Status}.Current()
	if st.Decision == updNoCheck { // nothing has run yet: nothing to report
		return nil, nil
	}
	b := updatesStatusBadge(st, false)
	c := &healthCheck{Group: healthSite, Label: "Update check", Value: b.Label, Level: healthOK, Href: a.adminPath + updatesPath}
	switch b.Kind {
	case "danger":
		c.Level = healthBad
	case "warning":
		c.Level = healthWarn
	}
	return c, nil
}

// dashHealth is the Site health card. Every check stands alone: one that
// fails or has no data is left out (a failure is logged), and with none left
// there is no card.
func (a *Admin) dashHealth(ctx context.Context) (dashResult, error) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	fns := a.healthChecks()
	checks := make([]*healthCheck, len(fns))
	wrapped := make([]func(context.Context) error, len(fns))
	for i, fn := range fns {
		wrapped[i] = func(ctx context.Context) (err error) {
			checks[i], err = fn(ctx)
			return err
		}
	}
	errs := a.runDash(ctx, wrapped)
	var done []*healthCheck
	for i, err := range errs {
		switch {
		case err != nil:
			a.logger().Warn("admin: site health check left out", "check", i, "err", err)
		case checks[i] != nil:
			done = append(done, checks[i])
		}
	}
	if len(done) == 0 {
		return dashResult{}, nil
	}
	sec := &dashSection{ID: "dash-health", Title: "Site health", State: dashOK}
	ok := 0
	for _, g := range healthGroupOrder {
		grp := healthGroup{Name: g}
		for _, c := range done {
			if c.Group != g {
				continue
			}
			it := healthItem{Label: c.Label, Badge: ui.Badge{Label: c.Value, Kind: healthKinds[c.Level]}}
			if c.Level == healthOK {
				ok++
			} else {
				it.Href = c.Href
			}
			grp.Items = append(grp.Items, it)
		}
		if len(grp.Items) > 0 {
			sec.Health = append(sec.Health, grp)
		}
	}
	sec.Description = fmt.Sprintf("%d of %d passing", ok, len(done))
	return dashResult{Sec: sec}, nil
}

// healthJob is the Site health card, owner only; nil when no store it reads
// is wired.
func (a *Admin) healthJob() *dashJob {
	if len(a.healthChecks()) == 0 {
		return nil
	}
	return &dashJob{Name: "site health", SecPos: 1, Run: a.dashHealth,
		Fallback: dashSection{ID: "dash-health", Title: "Site health", ErrMsg: "The site health could not be checked."}}
}
