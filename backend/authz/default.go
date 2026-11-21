package authz

import (
	"context"
	"fmt"
	"strings"
)

// rule is the default answer for one action. The owner session is always
// allowed. scope "" means API keys are denied.
type rule struct {
	editor bool
	scope  string
}

// table is the default policy, one entry per catalogue action.
var table = map[Action]rule{
	ActionAdminStyleguide:         {editor: false, scope: ""},
	ActionAnalyticsRead:           {editor: true, scope: "read:reports"},
	ActionAnalyticsSettingsManage: {editor: false, scope: ""},
	ActionAPIKeyManage:            {editor: false, scope: ""},
	ActionAuditRead:               {editor: false, scope: ""},
	ActionAuthorManage:            {editor: true, scope: ""},
	ActionBuilderBlockManage:      {editor: true, scope: ""},
	ActionBuilderPresetCreate:     {editor: true, scope: ""},
	ActionBuilderStyle:            {editor: false, scope: ""},
	ActionCacheFlush:              {editor: false, scope: ""},
	ActionCommentBlocklistManage:  {editor: true, scope: ""},
	ActionCommentCommenterErase:   {editor: false, scope: ""},
	ActionCommentCommenterExport:  {editor: false, scope: ""},
	ActionCommentModerate:         {editor: true, scope: "write:comments"},
	ActionCommentRead:             {editor: true, scope: "read:comments"},
	ActionCommentReplyAsStaff:     {editor: true, scope: "write:comments"},
	ActionCommentSettingsManage:   {editor: false, scope: ""},
	ActionCommentSettingsRead:     {editor: true, scope: ""},
	ActionDashboardRead:           {editor: true, scope: ""},
	ActionEntityCreate:            {editor: true, scope: "write:entities"},
	ActionEntityDelete:            {editor: true, scope: ""},
	ActionEntityDuplicate:         {editor: true, scope: "write:entities"},
	ActionEntityExport:            {editor: true, scope: ""},
	ActionEntityHandback:          {editor: true, scope: ""},
	ActionEntityPublish:           {editor: true, scope: "write:entities"},
	ActionEntityRead:              {editor: true, scope: "read:entities"},
	ActionEntityReorder:           {editor: true, scope: "write:entities"},
	ActionEntityShare:             {editor: true, scope: ""},
	ActionEntityTakeover:          {editor: true, scope: "write:entities"},
	ActionEntityTermsUpdate:       {editor: true, scope: ""},
	ActionEntityTranslate:         {editor: true, scope: ""},
	ActionEntityUnpublish:         {editor: true, scope: "write:entities"},
	ActionEntityUpdate:            {editor: true, scope: "write:entities"},
	ActionEntityVersionRestore:    {editor: true, scope: "write:entities"},
	ActionFormRead:                {editor: true, scope: "read:forms"},
	ActionFormSettingsManage:      {editor: false, scope: ""},
	ActionFormSubmissionRead:      {editor: true, scope: "read:forms"},
	ActionImportRun:               {editor: false, scope: ""},
	ActionLinkInsert:              {editor: true, scope: "write:entities"},
	ActionLinkSuggest:             {editor: true, scope: "read:entities"},
	ActionMediaDelete:             {editor: true, scope: ""},
	ActionMediaRead:               {editor: true, scope: "read:media"},
	ActionMediaSettingsManage:     {editor: false, scope: ""},
	ActionMediaUpdate:             {editor: true, scope: ""},
	ActionMediaUpload:             {editor: true, scope: "write:media"},
	ActionMenuManage:              {editor: true, scope: "write:menus"},
	ActionMenuRead:                {editor: true, scope: "read:entities"},
	ActionPersonManage:            {editor: true, scope: ""},
	ActionPluginChangesetReview:   {editor: false, scope: ""},
	ActionPluginGrant:             {editor: false, scope: ""},
	ActionPluginManage:            {editor: false, scope: ""},
	ActionPluginRouteInvoke:       {editor: true, scope: ""},
	ActionPluginUIAccess:          {editor: true, scope: ""},
	ActionPluginUIHost:            {editor: false, scope: ""},
	ActionPluginView:              {editor: true, scope: ""},
	ActionProfileUpdate:           {editor: true, scope: ""},
	ActionRedirectManage:          {editor: true, scope: "write:redirects"},
	ActionRedirectRead:            {editor: true, scope: "read:entities"},
	ActionRenderPreview:           {editor: true, scope: "read:entities"},
	ActionRobotsPolicyWrite:       {editor: false, scope: ""},
	ActionSchemaManage:            {editor: false, scope: ""},
	ActionSchemaRead:              {editor: true, scope: "read:entities"},
	ActionSettingsManage:          {editor: false, scope: ""},
	ActionSettingsSEORead:         {editor: false, scope: "read:seo"},
	ActionSettingsSEOWrite:        {editor: false, scope: "write:seo"},
	ActionSetupRun:                {editor: false, scope: ""},
	ActionTermManage:              {editor: false, scope: "write:terms"},
	ActionTermRead:                {editor: true, scope: "read:entities"},
	ActionThemeManage:             {editor: false, scope: ""},
	ActionThemePreview:            {editor: true, scope: ""},
	ActionUserDelete:              {editor: false, scope: ""},
	ActionUserManage:              {editor: false, scope: ""},
	ActionUserOwnerGrant:          {editor: false, scope: ""},
	ActionWebhookManage:           {editor: false, scope: ""},
}

// knownScopes are the API key scopes (without the "*" wildcard).
var knownScopes = []string{
	"read:entities", "write:entities", "read:citations", "read:reports", "write:reports",
	"read:query_suites", "write:query_suites", "write:media", "read:media", "write:menus",
	"write:redirects", "write:terms", "read:seo", "write:seo", "read:forms",
	"read:comments", "write:comments",
}

// ownerOnlyScope reports scopes a key holds only while its creator is an owner.
func ownerOnlyScope(s string) bool {
	switch s {
	case "read:seo", "write:seo", "write:terms":
		return true
	}
	return false
}

// hasScope reports whether a key with the held scopes and creator role has
// need: exact match or "*", and write:X implies read:X. Owner-only scopes are
// refused, also through "*", unless the creator is an owner.
func hasScope(held []string, creatorRole, need string) bool {
	if ownerOnlyScope(need) && creatorRole != "owner" {
		return false
	}
	write, isRead := strings.CutPrefix(need, "read:")
	write = "write:" + write
	for _, s := range held {
		if s == "*" || s == need || (isRead && s == write) {
			return true
		}
	}
	return false
}

type defaultAuthorizer struct{}

// Can implements Authorizer with the built-in rules; the object is ignored.
func (defaultAuthorizer) Can(_ context.Context, s Subject, a Action, _ Object) (bool, error) {
	r, ok := table[a]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownAction, a)
	}
	if s.KeyID != "" {
		creatorOK := s.CreatorRole == "owner" || s.CreatorRole == "editor"
		return creatorOK && r.scope != "" && hasScope(s.Scopes, s.CreatorRole, r.scope), nil
	}
	if s.UserID == "" {
		return false, nil
	}
	switch s.Role {
	case "owner":
		return true, nil
	case "editor":
		return r.editor, nil
	}
	return false, nil
}
