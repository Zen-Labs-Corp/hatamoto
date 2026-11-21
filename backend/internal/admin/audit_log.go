package admin

import (
	"net/http"
	"strconv"
	"strings"

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
	"email.settings":               "Changed email settings",
	"email.settings.reset":         "Reset email settings",
	"cache.settings":               "Changed cache settings",
	"cache.clear":                  "Cleared the cache",
	"cache.preload":                "Preloaded the cache",
	"spam.settings":                "Changed spam settings",
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
}

func (a *Admin) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	page := pageParam(r)
	rows, total, err := a.deps.AuditLog.Recent(r.Context(), auditLogPer, (page-1)*auditLogPer)
	if err != nil {
		a.logger().Error("list audit log failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The audit log could not be loaded.")
		return
	}
	loc := a.siteLocation(r.Context())
	base := a.adminPath + auditRel
	t := ui.Table{
		ID: "audit-log", Caption: "Audit log", BaseURL: base, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "when", Label: "When"}, {Key: "who", Label: "Who"}, {Key: "action", Label: "Action"}, {Key: "target", Label: "Target"}},
		Page:    ui.Pagination{Page: page, PerPage: auditLogPer, Total: total, BaseURL: base},
		Empty:   ui.Empty{Icon: "clock", Title: "Nothing recorded yet", Body: "Changes made in the admin appear here."},
	}
	for _, e := range rows {
		when := fmtDateTime(e.At.In(loc))
		t.Rows = append(t.Rows, ui.Row{ID: strconv.FormatInt(e.ID, 10), Label: auditLabel(e.Action) + " at " + when, Cells: []ui.Cell{
			{Text: when, Nowrap: true}, {Text: e.Actor}, {Text: auditLabel(e.Action)}, {Text: auditTarget(e), Clip: true},
		}})
	}
	v := a.NewView(w, r, "Audit log")
	v.Data = struct{ Table ui.Table }{t}
	a.Render(w, r, http.StatusOK, auditLogPage, v)
}
