package admin

// Names of the audit actions the admin and the API write. They are exported
// because the REST handlers record the same actions. The entity repository,
// the scheduler and the content services keep their own string constants.
const (
	AuditSignIn       = "session.sign_in"
	AuditSignInFailed = "session.sign_in_failed"
	AuditSignOut      = "session.sign_out"

	AuditEntityLinksInserted = "entity.links_inserted"
	AuditEntityTermsReplaced = "entity.terms_replaced"

	AuditUserCreated         = "user.created"
	AuditUserRoleChanged     = "user.role_changed"
	AuditUserUpdated         = "user.updated"
	AuditUserDeleted         = "user.deleted"
	AuditUserInvited         = "user.invited"
	AuditUserInviteResent    = "user.invite_resent"
	AuditUserInviteRevoked   = "user.invite_revoked"
	AuditUserInviteAccepted  = "user.invite_accepted"
	AuditUserResetLinkSent   = "user.reset_link_sent"
	AuditUserPasswordReset   = "user.password_reset"
	AuditUserIdentityLinked  = "user.identity_linked"
	AuditUserIdentityUnlink  = "user.identity_unlinked"
	AuditUserSessionsRevoked = "user.sessions_revoked"

	AuditAPIKeyCreated     = "api_key.created"
	AuditAPIKeyUpdated     = "api_key.updated"
	AuditAPIKeyRevoked     = "api_key.revoked"
	AuditAPIKeyRegenerated = "api_key.regenerated"
	AuditAPIKeyDeleted     = "api_key.deleted"

	AuditSettingsUpdated    = "settings.updated"
	AuditPluginPermissions  = "plugin.permissions"
	AuditWebhookCreated     = "webhook.created"
	AuditWebhookUpdated     = "webhook.updated"
	AuditWebhookPaused      = "webhook.paused"
	AuditWebhookResumed     = "webhook.resumed"
	AuditWebhookSecretRot   = "webhook.secret_rotated"
	AuditWebhookDeleted     = "webhook.deleted"
	AuditContentTypeCreated = "content_type.created"
	AuditContentTypeUpdated = "content_type.updated"
	AuditContentTypeDeleted = "content_type.deleted"
	AuditTemplateSaved      = "template.saved"
	AuditTemplateDeleted    = "template.deleted"

	AuditLinkRuleSaved    = "linking.rule_saved"
	AuditLinkRuleToggled  = "linking.rule_toggled"
	AuditLinkRuleDeleted  = "linking.rule_deleted"
	AuditLinkRulesChanged = "linking.rules_changed"

	AuditTermCreated  = "term.created"
	AuditTermUpdated  = "term.updated"
	AuditTermDeleted  = "term.deleted"
	AuditTermMerged   = "term.merged"
	AuditTermCopied   = "term.copied"
	AuditTermLinked   = "term.linked"
	AuditTermUnlinked = "term.unlinked"

	AuditMenuCreated        = "menu.created"
	AuditMenuUpdated        = "menu.updated"
	AuditMenuDeleted        = "menu.deleted"
	AuditMenuItemCreated    = "menu.item_created"
	AuditMenuItemUpdated    = "menu.item_updated"
	AuditMenuItemDeleted    = "menu.item_deleted"
	AuditMenuItemsReordered = "menu.items_reordered"

	AuditRedirectCreated = "redirect.created"
	AuditRedirectUpdated = "redirect.updated"
	AuditRedirectDeleted = "redirect.deleted"
	AuditNotFoundIgnored = "not_found.ignored"
	AuditNotFoundDeleted = "not_found.deleted"

	AuditMediaUploaded = "media.uploaded"
	AuditMediaUpdated  = "media.updated"
	AuditMediaDeleted  = "media.deleted"
	AuditMediaRebuilt  = "media.rebuilt"
	AuditMediaSwept    = "media.swept"
	AuditMediaAdopted  = "media.adopted"
)
