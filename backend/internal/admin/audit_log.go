package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/admin/ui"
)

const (
	auditRel     = "/audit"
	auditLogPer  = 25
	auditLogPage = "audit/index"
)

// auditLabels are the plain names of the actions the admin writes. Any other
// action (packages, imports, CLI) shows as stored.
var auditLabels = map[string]string{
	"session.sign_in":              "Signed in",
	"session.sign_in_second_step":  "Signed in with a provider, second step asked",
	"session.sign_in_failed":       "Sign-in refused",
	"signin.oidc.settings":         "Changed single sign-on settings",
	"signin.user_created":          "Created a user at first single sign-on",
	"signin.identity_linked":       "Linked a single sign-on identity",
	"entity.publish":               "Published",
	"entity.unpublish":             "Unpublished",
	"entity.archive":               "Archived",
	"entity.update":                "Edited",
	"term.seo_update":              "Edited term SEO",
	"entity.schedule":              "Scheduled",
	"entity.unpublish_schedule":    "Scheduled an unpublish",
	"entity.unpublish_clear":       "Removed a scheduled unpublish",
	"entity.unpublish_scheduled":   "Unpublished (scheduled)",
	"entity.delete":                "Deleted",
	"entity.restore_version":       "Restored a version",
	"search_replace.apply":         "Searched and replaced",
	"backups.create":               "Started a backup",
	"backups.download":             "Downloaded a backup",
	"backups.delete":               "Deleted a backup",
	"backups.retention":            "Changed backup retention",
	"audit.retention":              "Changed audit log retention",
	"security.csp_enforce":         "Turned on stricter page protection",
	"email.settings":               "Changed email settings",
	"email.settings.reset":         "Reset email settings",
	"cache.settings":               "Changed cache settings",
	"cache.clear":                  "Cleared the cache",
	"cache.preload":                "Preloaded the cache",
	"spam.settings":                "Changed spam settings",
	"kit.apply":                    "Applied a kit",
	"comments.enable":              "Turned comments on",
	"comments.disable":             "Turned comments off",
	"comments.settings":            "Changed comment settings",
	"comments.blocklist":           "Changed a comment blocklist",
	"comments.entity-mode":         "Changed comments on an entry",
	"comments.schema-mode":         "Changed comments for a type",
	"comments.commenter.vouch":     "Vouched for a commenter",
	"comments.commenter.block":     "Blocked a commenter",
	"comments.commenter.signout":   "Signed a commenter out everywhere",
	"comments.commenter.export":    "Exported a commenter's data",
	"comments.commenter.erase":     "Erased a commenter",
	"comments.approve":             "Approved comments",
	"comments.spam":                "Marked comments as spam",
	"comments.trash":               "Moved comments to trash",
	"comments.reply":               "Replied to a comment",
	"comments.edit":                "Edited a comment",
	"comments.delete":              "Deleted comments",
	"updates.save":                 "Changed update settings",
	"updates.check":                "Checked for updates",
	"updates.apply":                "Started an update",
	"updates.apply_without_backup": "Started an update without a backup",
	"updates.unpin":                "Removed the version pin",
	"changeset.publish":            "Published plugin changes",
	"changeset.discard":            "Discarded plugin changes",
	"changeset.revert":             "Undid plugin changes",
	"changeset.edit_submit":        "Sent for review",
	"changeset.stage_decision":     "Review decision",
	"changeset.edit_publish":       "Published a reviewed change",
	"changeset.edit_discard":       "Withdrew or discarded a change waiting for review",
	"settings.review":              "Turned review on or off",
	"changeset.suggestion.approve": "Approved a plugin suggestion",
	"changeset.suggestion.reject":  "Rejected a plugin suggestion",
	"plugin.run.cancel":            "Cancelled a plugin run",
	"plugin.mail_quota":            "Changed a plugin's mail quota",
	"package.allow_unsigned":       "Changed the unsigned packages setting",
	"package.allow_developer":      "Changed the developer packages setting",
	"package.revoke_disable":       "Disabled a package revoked by the registry",
	"session.sign_out":             "Signed out",
	"entity.create":                "Created",
	"entity.publish_scheduled":     "Published on schedule",
	"entity.links_inserted":        "Links inserted",
	"entity.terms_replaced":        "Tags and categories set",
	"user.created":                 "User added",
	"user.role_changed":            "Role changed",
	"user.updated":                 "User updated",
	"user.deleted":                 "User removed",
	"user.invited":                 "User invited",
	"user.invite_resent":           "Invite sent again",
	"user.invite_revoked":          "Invite withdrawn",
	"user.invite_accepted":         "Invite accepted",
	"user.reset_link_sent":         "Password link sent",
	"user.password_reset":          "Password reset",
	"user.password_changed":        "Password changed",
	"user.identity_linked":         "Sign-in identity linked",
	"user.identity_unlinked":       "Sign-in identity unlinked",
	"user.sessions_revoked":        "Signed out everywhere",
	"api_key.created":              "API key created",
	"apikey.create":                "API key created",
	"api_key.updated":              "API key changed",
	"api_key.revoked":              "API key revoked",
	"api_key.regenerated":          "API key regenerated",
	"api_key.deleted":              "API key deleted",
	"settings.updated":             "Settings saved",
	"package.grants":               "Plugin permissions changed",
	"webhook.created":              "Webhook added",
	"webhook.updated":              "Webhook changed",
	"webhook.paused":               "Webhook paused",
	"webhook.resumed":              "Webhook resumed",
	"webhook.secret_rotated":       "Webhook secret replaced",
	"webhook.deleted":              "Webhook deleted",
	"content_type.created":         "Content type added",
	"content_type.updated":         "Content type changed",
	"content_type.deleted":         "Content type deleted",
	"template.saved":               "Template saved",
	"template.deleted":             "Template deleted",
	"linking.rule_saved":           "Link rule saved",
	"linking.rule_toggled":         "Link rule switched",
	"linking.rule_deleted":         "Link rule deleted",
	"linking.rules_changed":        "Link rules changed",
	"term.created":                 "Tag or category added",
	"term.updated":                 "Tag or category changed",
	"term.deleted":                 "Tag or category deleted",
	"term.merged":                  "Tag or category merged",
	"term.copied":                  "Tag or category copied",
	"term.linked":                  "Tag or category linked",
	"term.unlinked":                "Tag or category unlinked",
	"menu.created":                 "Menu added",
	"menu.updated":                 "Menu changed",
	"menu.deleted":                 "Menu deleted",
	"menu.item_created":            "Menu item added",
	"menu.item_updated":            "Menu item changed",
	"menu.item_deleted":            "Menu item removed",
	"menu.items_reordered":         "Menu items reordered",
	"redirect.created":             "Redirect added",
	"redirect.updated":             "Redirect changed",
	"redirect.deleted":             "Redirect deleted",
	"not_found.ignored":            "Missing page ignored",
	"not_found.deleted":            "Missing page removed",
	"media.uploaded":               "File uploaded",
	"media.updated":                "File changed",
	"media.deleted":                "File deleted",
	"media.rebuild.start":          "Image rebuild started",
	"media.rebuild.finish":         "Image rebuild finished",
	"media.swept":                  "Unused files removed",
	"media.adopted":                "Files adopted",
}

// auditLabel is the label of an action, or the action itself when unknown.
func auditLabel(action string) string {
	if l, ok := auditLabels[action]; ok {
		return l
	}
	return action
}

// auditTarget is what the event touched: the name the handler recorded, else
// the stored target.
func auditTarget(r AuditRow) string {
	if n, ok := r.Detail["name"].(string); ok && strings.TrimSpace(n) != "" {
		return n
	}
	return r.Target
}

// mountAuditLog adds the owner-only Audit log page. Without a reader there is
// no page.
func (a *Admin) mountAuditLog(owner *httpx.Group) {
	if a.deps.AuditLog == nil {
		return
	}
	owner.HandleFunc("GET "+auditRel, a.handleAuditLog)
	owner.HandleFunc("GET "+auditRetentionRel, a.handleAuditRetention)
	owner.HandleFunc("POST "+auditRetentionRel, a.handleAuditRetentionSave)
}

// Search window rules: a text search always carries a date range so the scan
// stays bounded.
const (
	auditSearchDefaultDays = 30
	auditSearchMaxDays     = 366
	auditFacetDays         = 90
	auditErrorClip         = 200 // runes of an agent call's error shown in the Result cell
)

// auditParams is the URL state of the page: everything but the cursor.
type auditParams struct {
	q, source, actor, action, from, to string
}

func (p auditParams) query() url.Values {
	v := url.Values{}
	for k, s := range map[string]string{"q": p.q, "source": p.source, "actor": p.actor, "action": p.action, "from": p.from, "to": p.to} {
		if s != "" {
			v.Set(k, s)
		}
	}
	return v
}

// href is the page URL with the given params dropped.
func (p auditParams) href(prefix string, drop ...string) string {
	q := p.query()
	for _, k := range drop {
		q.Del(k)
	}
	if len(q) == 0 {
		return prefix
	}
	return prefix + "?" + q.Encode()
}

// auditRange turns the from and to params (days in loc, to inclusive) into
// the half-open window of the query. A bad date is ignored. With a text search
// the window is always set: from defaults to 30 days before the end and is
// never wider than 366 days. The returned from value is the day to show.
// parseAuditDay is parseDay with the day clamped to the range the audit log
// can hold: from 2000-01-01 to tomorrow, like the cursor.
func parseAuditDay(s string, loc *time.Location, now time.Time) (time.Time, bool) {
	t, ok := parseDay(s, loc)
	if !ok {
		return t, false
	}
	if oldest := time.Date(2000, 1, 1, 0, 0, 0, 0, loc); t.Before(oldest) {
		return oldest, true
	}
	y, m, d := now.In(loc).Date()
	if newest := time.Date(y, m, d, 0, 0, 0, 0, loc).AddDate(0, 0, 1); t.After(newest) {
		return newest, true
	}
	return t, true
}

// auditDayValue is the clamped day as a form value, or "" for a bad date.
func auditDayValue(s string, loc *time.Location, now time.Time) string {
	if t, ok := parseAuditDay(s, loc, now); ok {
		return t.Format("2006-01-02")
	}
	return ""
}

func auditRange(p auditParams, loc *time.Location, now time.Time) (from, to time.Time, fromShown string) {
	from, fromOK := parseAuditDay(p.from, loc, now)
	toDay, toOK := parseAuditDay(p.to, loc, now)
	if fromOK && toOK && from.After(toDay) {
		from, toDay = toDay, from
	}
	if toOK {
		to = toDay.AddDate(0, 0, 1)
	}
	if !fromOK {
		from = time.Time{}
	}
	if p.q != "" {
		end := to
		if end.IsZero() {
			y, m, d := now.In(loc).Date()
			end = time.Date(y, m, d, 0, 0, 0, 0, loc).AddDate(0, 0, 1)
		}
		if from.IsZero() {
			from = end.AddDate(0, 0, -auditSearchDefaultDays)
		}
		if oldest := end.AddDate(0, 0, -auditSearchMaxDays); from.Before(oldest) {
			from = oldest
		}
	}
	if !from.IsZero() {
		fromShown = from.In(loc).Format("2006-01-02")
	}
	return from, to, fromShown
}

func (a *Admin) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qv := r.URL.Query()
	q := normalizeAuditQuery(AuditQuery{Q: qv.Get("q"), Actor: qv.Get("actor"), Source: qv.Get("source"), Action: strings.TrimSpace(qv.Get("action"))})
	p := auditParams{q: q.Q, source: q.Source, actor: q.Actor, action: q.Action, from: qv.Get("from"), to: qv.Get("to")}
	loc := a.siteLocation(ctx)
	now := time.Now()
	from, to, fromShown := auditRange(p, loc, now)
	p.from = auditDayValue(p.from, loc, now)
	p.to = auditDayValue(p.to, loc, now)
	if p.from != "" && p.to != "" && p.from > p.to {
		p.from, p.to = p.to, p.from
	}
	q.From, q.To, q.Limit = from, to, auditLogPer
	q.Before = decodeAuditCursor(qv.Get("before"))
	page, err := a.deps.AuditLog.Search(ctx, q)
	if err != nil {
		a.logger().Error("search audit log failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The audit log could not be loaded.")
		return
	}
	facetFrom, facetTo := from, to
	if facetTo.IsZero() {
		facetTo = now.Add(24 * time.Hour)
	}
	if facetFrom.IsZero() {
		facetFrom = facetTo.AddDate(0, 0, -auditFacetDays)
	}
	facets, err := a.deps.AuditLog.Facets(ctx, facetFrom, facetTo)
	if err != nil {
		a.logger().Error("audit log facets failed", "err", err)
		facets = AuditFacets{}
	}

	base := a.adminPath + auditRel
	sourceOpts := []ui.Option{{Value: "", Label: "Any source", Selected: p.source == ""}}
	for _, o := range auditSources {
		sourceOpts = append(sourceOpts, ui.Option{Value: o.Value, Label: o.Label, Selected: p.source == o.Value})
	}
	actorOpts := []ui.Option{{Value: "", Label: "Any actor", Selected: p.actor == ""}}
	seenActor := p.actor == ""
	for _, o := range facets.Actors {
		seenActor = seenActor || o.Value == p.actor
		actorOpts = append(actorOpts, ui.Option{Value: o.Value, Label: o.Label, Selected: o.Value == p.actor})
	}
	if !seenActor {
		actorOpts = append(actorOpts, ui.Option{Value: p.actor, Label: p.actor[strings.Index(p.actor, ":")+1:], Selected: true})
	}
	actionOpts := []ui.Option{{Value: "", Label: "Any action", Selected: p.action == ""}}
	seenAction := p.action == ""
	for _, act := range facets.Actions {
		seenAction = seenAction || act == p.action
		actionOpts = append(actionOpts, ui.Option{Value: act, Label: auditLabel(act), Selected: act == p.action})
	}
	if !seenAction {
		actionOpts = append(actionOpts, ui.Option{Value: p.action, Label: auditLabel(p.action), Selected: true})
	}
	var chips []ui.Chip
	if t, ok := parseAuditDay(p.from, loc, now); ok {
		chips = append(chips, ui.Chip{Label: "From " + fmtDate(t), RemoveHref: p.href(base, "from")})
	}
	if t, ok := parseAuditDay(p.to, loc, now); ok {
		chips = append(chips, ui.Chip{Label: "To " + fmtDate(t), RemoveHref: p.href(base, "to")})
	}
	empty := ui.Empty{Icon: "clock", Title: "Nothing recorded yet", Body: "Changes made in the admin appear here."}
	if len(p.query()) > 0 {
		empty = ui.Empty{Icon: "search", Title: "No matching entries", Body: "Try fewer filters or a wider date range."}
	}
	t := ui.Table{
		ID: "audit-log", Caption: "Audit log", BaseURL: base, CSRF: csrfToken(ctx),
		Query: p.q, SearchLabel: "Search the audit log",
		PrimaryFilters: []ui.Filter{
			{Name: "source", Label: "Source", Options: sourceOpts},
			{Name: "actor", Label: "Actor", Options: actorOpts},
			{Name: "action", Label: "Action", Options: actionOpts},
		},
		PopoverFilters: []ui.Filter{
			{Name: "from", Label: "From", Input: "date", Value: fromShown},
			{Name: "to", Label: "To", Input: "date", Value: p.to},
		},
		Chips: chips, ClearHref: p.href(base, "source", "actor", "action", "from", "to"),
		OOB:     "#audit-log-older",
		Columns: []ui.Col{{Key: "when", Label: "When"}, {Key: "who", Label: "Who"}, {Key: "source", Label: "Source"}, {Key: "action", Label: "Action"}, {Key: "target", Label: "Target"}, {Key: "result", Label: "Result"}},
		Empty:   empty,
	}
	for _, e := range page.Rows {
		when := fmtDateTime(e.At.In(loc))
		result := auditResultLabel(e.Result)
		cell := ui.Cell{Text: result}
		if e.Error != "" {
			cell.Notes = []string{clip(e.Error, auditErrorClip)}
		}
		t.Rows = append(t.Rows, ui.Row{ID: e.Src + strconv.FormatInt(e.ID, 10), Label: auditLabel(e.Action) + " at " + when, Cells: []ui.Cell{
			{Text: when, Nowrap: true}, {Text: e.Actor}, {Text: auditSourceLabel(e.Source)}, {Text: auditLabel(e.Action)}, {Text: auditTarget(e), Clip: true}, cell,
		}})
	}
	older := ""
	if page.Next != nil {
		qq := p.query()
		qq.Set("before", page.Next.Encode())
		older = base + "?" + qq.Encode()
	}
	v := a.NewView(w, r, "Audit log")
	v.Data = struct {
		Table    ui.Table
		Older    string
		Overflow []ui.Action
	}{t, older, a.auditOverflow(ctx)}
	a.Render(w, r, http.StatusOK, auditLogPage, v)
}

func auditSourceLabel(s string) string {
	for _, o := range auditSources {
		if o.Value == s {
			return o.Label
		}
	}
	return s
}

func auditResultLabel(s string) string {
	switch s {
	case "ok":
		return "OK"
	case "denied":
		return "Refused"
	case "error":
		return "Failed"
	}
	return ""
}

// auditOverflow is the header's more menu: the retention link, only when the
// retention can be changed. The hint shows the saved periods when they load.
func (a *Admin) auditOverflow(ctx context.Context) []ui.Action {
	svc := a.deps.AuditRetention
	if svc == nil {
		return nil
	}
	item := ui.Action{Label: "Keep entries for", Href: a.adminPath + auditRetentionRel}
	if admin, agent, err := svc.Days(ctx); err != nil {
		a.logger().Error("audit retention settings", "err", err)
	} else {
		item.Hint = auditlog.RetentionLabel(admin) + " · agent calls " + auditlog.RetentionLabel(agent)
	}
	return []ui.Action{item}
}
