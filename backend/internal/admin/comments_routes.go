package admin

import (
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/httpx"
)

// mountComments registers every comments route. Moderation, blocklists and
// commenter trust are owner+editor (editors moderate everything); the site
// switch, site settings, schema defaults and privacy export/erase are
// owner-only.
func (a *Admin) mountComments(authed *httpx.Group) {
	ed := authed.Group("", a.RequireCan(authz.ActionCommentModerate))
	owner := authed.Group("", a.RequireCan(authz.ActionCommentSettingsManage))
	sudo := owner.Group("", a.RequireSudo) // personal data out or gone: confirm the password first
	ed.HandleFunc("GET /comments", a.handleComments)
	ed.HandleFunc("POST /comments/bulk", a.handleCommentsBulk)
	ed.HandleFunc("POST /comments/{id}/approve", a.handleCommentApprove)
	ed.HandleFunc("GET /comments/{id}/spam", a.handleCommentSpamPage)
	ed.HandleFunc("POST /comments/{id}/spam", a.handleCommentSpam)
	ed.HandleFunc("POST /comments/{id}/trash", a.handleCommentTrash)
	ed.HandleFunc("GET /comments/{id}/reply", a.handleCommentReplyPage)
	ed.HandleFunc("POST /comments/{id}/reply", a.handleCommentReply)
	ed.HandleFunc("GET /comments/{id}/edit", a.handleCommentEditPage)
	ed.HandleFunc("POST /comments/{id}/edit", a.handleCommentEdit)
	ed.HandleFunc("POST /entities/{id}/comments", a.handleEntityComments)
	ed.HandleFunc("GET /settings/comments", a.handleCommentSettings)
	ed.HandleFunc("GET /settings/comments/blocklists", a.handleCommentBlocklists)
	ed.HandleFunc("POST /settings/comments/blocklists", a.handleCommentBlocklistsSave)
	ed.HandleFunc("GET /settings/comments/commenters", a.handleCommenters)
	ed.HandleFunc("POST /settings/comments/commenters/{id}/{action}", a.handleCommenterAction) // vouch, block or signout
	ed.HandleFunc("GET /settings/comments/commenters/{id}/{action}", a.handleCommenterConfirm) // block or signout
	owner.HandleFunc("POST /settings/comments", a.handleCommentSettingsSave)
	owner.HandleFunc("POST /settings/comments/switch", a.handleCommentSwitch)
	owner.HandleFunc("GET /settings/comments/enable", a.handleCommentEnablePage)
	owner.HandleFunc("POST /settings/comments/enable", a.handleCommentEnable)
	owner.HandleFunc("POST /settings/comments/types/{id}", a.handleCommentPolicySave)
	owner.HandleFunc("POST /settings/types/{id}/comments", a.handleSchemaComments)
	sudo.HandleFunc("GET /settings/comments/commenters/{id}/export", a.handleCommenterExport)
	sudo.HandleFunc("GET /settings/comments/commenters/{id}/erase", a.handleCommenterErasePage)
	sudo.HandleFunc("POST /settings/comments/commenters/{id}/erase", a.handleCommenterErase)
}
