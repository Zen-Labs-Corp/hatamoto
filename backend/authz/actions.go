package authz

import "sort"

// The action catalogue. Every authorization decision in core is one of these
// strings; a registered Authorizer may also receive actions defined by a module.
const (
	// ActionAdminStyleguide is the "admin.styleguide" action.
	ActionAdminStyleguide Action = "admin.styleguide"
	// ActionAnalyticsRead is the "analytics.read" action.
	ActionAnalyticsRead Action = "analytics.read"
	// ActionAnalyticsSettingsManage is the "analytics.settings.manage" action.
	ActionAnalyticsSettingsManage Action = "analytics.settings.manage"
	// ActionAPIKeyManage is the "apikey.manage" action.
	ActionAPIKeyManage Action = "apikey.manage"
	// ActionAPIKeyCreatorAct is the "apikey.creator.act" action: the creator of an API key still may let it act.
	ActionAPIKeyCreatorAct Action = "apikey.creator.act"
	// ActionAuditRead is the "audit.read" action.
	ActionAuditRead Action = "audit.read"
	// ActionAuthorManage is the "author.manage" action.
	ActionAuthorManage Action = "author.manage"
	// ActionBuilderBlockManage is the "builder.block.manage" action.
	ActionBuilderBlockManage Action = "builder.block.manage"
	// ActionBuilderPresetCreate is the "builder.preset.create" action.
	ActionBuilderPresetCreate Action = "builder.preset.create"
	// ActionBuilderStyle is the "builder.style" action.
	ActionBuilderStyle Action = "builder.style"
	// ActionCacheFlush is the "cache.flush" action.
	ActionCacheFlush Action = "cache.flush"
	// ActionCommentBlocklistManage is the "comment.blocklist.manage" action.
	ActionCommentBlocklistManage Action = "comment.blocklist.manage"
	// ActionCommentCommenterErase is the "comment.commenter.erase" action.
	ActionCommentCommenterErase Action = "comment.commenter.erase"
	// ActionCommentCommenterExport is the "comment.commenter.export" action.
	ActionCommentCommenterExport Action = "comment.commenter.export"
	// ActionCommentModerate is the "comment.moderate" action.
	ActionCommentModerate Action = "comment.moderate"
	// ActionCommentRead is the "comment.read" action.
	ActionCommentRead Action = "comment.read"
	// ActionCommentReceiveModerationMail is the "comment.receive_moderation_mail" action.
	ActionCommentReceiveModerationMail Action = "comment.receive_moderation_mail"
	// ActionCommentReplyAsStaff is the "comment.reply_as_staff" action.
	ActionCommentReplyAsStaff Action = "comment.reply_as_staff"
	// ActionCommentSettingsManage is the "comment.settings.manage" action.
	ActionCommentSettingsManage Action = "comment.settings.manage"
	// ActionCommentSettingsRead is the "comment.settings.read" action.
	ActionCommentSettingsRead Action = "comment.settings.read"
	// ActionContentWrite is the "content.write" action: change entities and media at all. The admin and the REST API ask it before
	// every non-read request to the content screens and routes; which entity or media item may change is decided per object
	// (entity.update, media.update, ...).
	ActionContentWrite Action = "content.write"
	// ActionDashboardOwnerRead is the "dashboard.owner.read" action.
	ActionDashboardOwnerRead Action = "dashboard.owner.read"
	// ActionDashboardRead is the "dashboard.read" action.
	ActionDashboardRead Action = "dashboard.read"
	// ActionEntityCreate is the "entity.create" action.
	ActionEntityCreate Action = "entity.create"
	// ActionEntityDelete is the "entity.delete" action.
	ActionEntityDelete Action = "entity.delete"
	// ActionEntityDuplicate is the "entity.duplicate" action.
	ActionEntityDuplicate Action = "entity.duplicate"
	// ActionEntityExport is the "entity.export" action.
	ActionEntityExport Action = "entity.export"
	// ActionEntityHandback is the "entity.handback" action.
	ActionEntityHandback Action = "entity.handback"
	// ActionEntityPublish is the "entity.publish" action.
	ActionEntityPublish Action = "entity.publish"
	// ActionEntityRead is the "entity.read" action.
	ActionEntityRead Action = "entity.read"
	// ActionEntityReorder is the "entity.reorder" action.
	ActionEntityReorder Action = "entity.reorder"
	// ActionEntityReviewBypass is the "entity.review.bypass" action: publish or change published content
	// without review.
	ActionEntityReviewBypass Action = "entity.review.bypass"
	// ActionEntityReviewDecide is the "entity.review.decide" action: decide a stage of the reviewers group.
	ActionEntityReviewDecide Action = "entity.review.decide"
	// ActionEntityReviewRequest is the "entity.review.request" action: send a change for review.
	ActionEntityReviewRequest Action = "entity.review.request"
	// ActionEntityShare is the "entity.share" action.
	ActionEntityShare Action = "entity.share"
	// ActionEntityTakeover is the "entity.takeover" action.
	ActionEntityTakeover Action = "entity.takeover"
	// ActionEntityTermsUpdate is the "entity.terms.update" action.
	ActionEntityTermsUpdate Action = "entity.terms.update"
	// ActionEntityTranslate is the "entity.translate" action.
	ActionEntityTranslate Action = "entity.translate"
	// ActionEntityUnpublish is the "entity.unpublish" action.
	ActionEntityUnpublish Action = "entity.unpublish"
	// ActionEntityUpdate is the "entity.update" action.
	ActionEntityUpdate Action = "entity.update"
	// ActionEntityVersionRestore is the "entity.version.restore" action.
	ActionEntityVersionRestore Action = "entity.version.restore"
	// ActionFormManage is the "form.manage" action.
	ActionFormManage Action = "form.manage"
	// ActionFormRead is the "form.read" action.
	ActionFormRead Action = "form.read"
	// ActionFormSettingsManage is the "form.settings.manage" action.
	ActionFormSettingsManage Action = "form.settings.manage"
	// ActionFormSubmissionManage is the "form.submission.manage" action.
	ActionFormSubmissionManage Action = "form.submission.manage"
	// ActionFormSubmissionRead is the "form.submission.read" action.
	ActionFormSubmissionRead Action = "form.submission.read"
	// ActionImportRun is the "import.run" action.
	ActionImportRun Action = "import.run"
	// ActionLinkInsert is the "link.insert" action.
	ActionLinkInsert Action = "link.insert"
	// ActionLinkRuleManage is the "link.rule.manage" action.
	ActionLinkRuleManage Action = "link.rule.manage"
	// ActionLinkSuggest is the "link.suggest" action.
	ActionLinkSuggest Action = "link.suggest"
	// ActionMediaDelete is the "media.delete" action.
	ActionMediaDelete Action = "media.delete"
	// ActionMediaRead is the "media.read" action.
	ActionMediaRead Action = "media.read"
	// ActionMediaSettingsManage is the "media.settings.manage" action.
	ActionMediaSettingsManage Action = "media.settings.manage"
	// ActionMediaUpdate is the "media.update" action.
	ActionMediaUpdate Action = "media.update"
	// ActionMediaUpload is the "media.upload" action.
	ActionMediaUpload Action = "media.upload"
	// ActionMenuManage is the "menu.manage" action.
	ActionMenuManage Action = "menu.manage"
	// ActionMenuRead is the "menu.read" action.
	ActionMenuRead Action = "menu.read"
	// ActionPersonManage is the "person.manage" action.
	ActionPersonManage Action = "person.manage"
	// ActionPluginChangesetReview is the "plugin.changeset.review" action.
	ActionPluginChangesetReview Action = "plugin.changeset.review"
	// ActionPluginGrant is the "plugin.grant" action.
	ActionPluginGrant Action = "plugin.grant"
	// ActionPluginManage is the "plugin.manage" action.
	ActionPluginManage Action = "plugin.manage"
	// ActionPluginRouteInvoke is the "plugin.route.invoke" action.
	ActionPluginRouteInvoke Action = "plugin.route.invoke"
	// ActionPluginUIAccess is the "plugin.ui.access" action.
	ActionPluginUIAccess Action = "plugin.ui.access"
	// ActionPluginUIHost is the "plugin.ui.host" action.
	ActionPluginUIHost Action = "plugin.ui.host"
	// ActionPluginView is the "plugin.view" action.
	ActionPluginView Action = "plugin.view"
	// ActionProfileUpdate is the "profile.update" action.
	ActionProfileUpdate Action = "profile.update"
	// ActionRedirectManage is the "redirect.manage" action.
	ActionRedirectManage Action = "redirect.manage"
	// ActionRedirectRead is the "redirect.read" action.
	ActionRedirectRead Action = "redirect.read"
	// ActionRenderPreview is the "render.preview" action.
	ActionRenderPreview Action = "render.preview"
	// ActionRobotsPolicyWrite is the "robots.policy.write" action.
	ActionRobotsPolicyWrite Action = "robots.policy.write"
	// ActionSchemaManage is the "schema.manage" action.
	ActionSchemaManage Action = "schema.manage"
	// ActionSchemaRead is the "schema.read" action.
	ActionSchemaRead Action = "schema.read"
	// ActionSectionTemplateManage is the "section_template.manage" action.
	ActionSectionTemplateManage Action = "section_template.manage"
	// ActionSettingsManage is the "settings.manage" action.
	ActionSettingsManage Action = "settings.manage"
	// ActionSettingsSEORead is the "settings.seo.read" action.
	ActionSettingsSEORead Action = "settings.seo.read"
	// ActionSettingsSEOWrite is the "settings.seo.write" action.
	ActionSettingsSEOWrite Action = "settings.seo.write"
	// ActionSetupRun is the "setup.run" action.
	ActionSetupRun Action = "setup.run"
	// ActionSiteReceiveOwnerMail is the "site.receive_owner_mail" action: owner notices (run digest, export failure, admin address, comment surge).
	ActionSiteReceiveOwnerMail Action = "site.receive_owner_mail"
	// ActionSiteWrite is the "site.write" action: change anything at all. The admin and the REST API ask it before every
	// request that is not a read (GET, HEAD, OPTIONS), in addition to the route's own action. Denying it makes a user read-only.
	ActionSiteWrite Action = "site.write"
	// ActionTermManage is the "term.manage" action.
	ActionTermManage Action = "term.manage"
	// ActionTermRead is the "term.read" action.
	ActionTermRead Action = "term.read"
	// ActionThemeManage is the "theme.manage" action.
	ActionThemeManage Action = "theme.manage"
	// ActionThemePreview is the "theme.preview" action.
	ActionThemePreview Action = "theme.preview"
	// ActionUserDelete is the "user.delete" action.
	ActionUserDelete Action = "user.delete"
	// ActionUserManage is the "user.manage" action.
	ActionUserManage Action = "user.manage"
	// ActionUserOwnerGrant is the "user.owner.grant" action.
	ActionUserOwnerGrant Action = "user.owner.grant"
	// ActionWebhookManage is the "webhook.manage" action.
	ActionWebhookManage Action = "webhook.manage"
)

// Actions returns a sorted copy of every action in the catalogue.
func Actions() []Action {
	out := make([]Action, 0, len(table))
	for a := range table {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
