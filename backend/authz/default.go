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
	ActionAdminStyleguide:              {editor: false, scope: ""},
	ActionAnalyticsRead:                {editor: true, scope: "read:reports"},
	ActionAnalyticsSettingsManage:      {editor: false, scope: ""},
	ActionAPIKeyCreatorAct:             {editor: true, scope: ""},
	ActionAPIKeyManage:                 {editor: false, scope: ""},
	ActionAuditRead:                    {editor: false, scope: ""},
	ActionAuthorManage:                 {editor: true, scope: ""},
	ActionBuilderBlockManage:           {editor: true, scope: ""},
	ActionBuilderPresetCreate:          {editor: true, scope: ""},
	ActionBuilderStyle:                 {editor: false, scope: ""},
	ActionCacheFlush:                   {editor: false, scope: ""},
	ActionCommentBlocklistManage:       {editor: true, scope: ""},
	ActionCommentCommenterErase:        {editor: false, scope: ""},
	ActionCommentCommenterExport:       {editor: false, scope: ""},
	ActionCommentModerate:              {editor: true, scope: "write:comments"},
	ActionCommentRead:                  {editor: true, scope: "read:comments"},
	ActionCommentReceiveModerationMail: {editor: true, scope: ""},
	ActionCommentReplyAsStaff:          {editor: true, scope: "write:comments"},
	ActionCommentSettingsManage:        {editor: false, scope: ""},
	ActionCommentSettingsRead:          {editor: true, scope: ""},
	ActionContentWrite:                 {editor: true, scope: ""},
	ActionDashboardOwnerRead:           {editor: false, scope: ""},
	ActionDashboardRead:                {editor: true, scope: ""},
	ActionEntityCreate:                 {editor: true, scope: "write:entities"},
	ActionEntityDelete:                 {editor: true, scope: ""},
	ActionEntityDuplicate:              {editor: true, scope: "write:entities"},
	ActionEntityExport:                 {editor: true, scope: ""},
	ActionEntityHandback:               {editor: true, scope: ""},
	ActionEntityPublish:                {editor: true, scope: "write:entities"},
	ActionEntityRead:                   {editor: true, scope: "read:entities"},
	ActionEntityReorder:                {editor: true, scope: "write:entities"},
	ActionEntityReviewBypass:           {editor: false, scope: "write:entities"},
	ActionEntityReviewDecide:           {editor: true, scope: ""}, // keys never decide
	ActionEntityReviewRequest:          {editor: true, scope: "write:entities"},
	ActionEntityShare:                  {editor: true, scope: ""},
	ActionEntityTakeover:               {editor: true, scope: "write:entities"},
	ActionEntityTermsUpdate:            {editor: true, scope: ""},
	ActionEntityTranslate:              {editor: true, scope: ""},
	ActionEntityUnpublish:              {editor: true, scope: "write:entities"},
	ActionEntityUpdate:                 {editor: true, scope: "write:entities"},
	ActionEntityVersionRestore:         {editor: true, scope: "write:entities"},
	ActionFormManage:                   {editor: true, scope: ""},
	ActionFormRead:                     {editor: true, scope: "read:forms"},
	ActionFormSettingsManage:           {editor: false, scope: ""},
	ActionFormSubmissionManage:         {editor: true, scope: ""},
	ActionFormSubmissionRead:           {editor: true, scope: "read:forms"},
	ActionImportRun:                    {editor: false, scope: ""},
	ActionLinkInsert:                   {editor: true, scope: "write:entities"},
	ActionLinkRuleManage:               {editor: true, scope: ""},
	ActionLinkSuggest:                  {editor: true, scope: "read:entities"},
	ActionMediaDelete:                  {editor: true, scope: ""},
	ActionMediaRead:                    {editor: true, scope: "read:media"},
	ActionMediaSettingsManage:          {editor: false, scope: ""},
	ActionMediaUpdate:                  {editor: true, scope: ""},
	ActionMediaUpload:                  {editor: true, scope: "write:media"},
	ActionMenuManage:                   {editor: true, scope: "write:menus"},
	ActionMenuRead:                     {editor: true, scope: "read:entities"},
	ActionPersonManage:                 {editor: true, scope: ""},
	ActionPluginChangesetReview:        {editor: false, scope: ""},
	ActionPluginGrant:                  {editor: false, scope: ""},
	ActionPluginManage:                 {editor: false, scope: ""},
	ActionPluginRouteInvoke:            {editor: true, scope: ""},
	ActionPluginUIAccess:               {editor: true, scope: ""},
	ActionPluginUIHost:                 {editor: false, scope: ""},
	ActionPluginView:                   {editor: true, scope: ""},
	ActionProfileUpdate:                {editor: true, scope: ""},
	ActionRedirectManage:               {editor: true, scope: "write:redirects"},
	ActionRedirectRead:                 {editor: true, scope: "read:entities"},
	ActionRenderPreview:                {editor: true, scope: "read:entities"},
	ActionRobotsPolicyWrite:            {editor: false, scope: ""},
	ActionSchemaManage:                 {editor: false, scope: ""},
	ActionSchemaRead:                   {editor: true, scope: "read:entities"},
	ActionSectionTemplateManage:        {editor: true, scope: ""},
	ActionSettingsManage:               {editor: false, scope: ""},
	ActionSettingsSEORead:              {editor: false, scope: "read:seo"},
	ActionSettingsSEOWrite:             {editor: false, scope: "write:seo"},
	ActionSetupRun:                     {editor: false, scope: ""},
	ActionSiteReceiveOwnerMail:         {editor: false, scope: ""},
	ActionSiteWrite:                    {editor: true, scope: ""},
	ActionTermManage:                   {editor: false, scope: "write:terms"},
	ActionTermRead:                     {editor: true, scope: "read:entities"},
	ActionThemeManage:                  {editor: false, scope: ""},
	ActionThemePreview:                 {editor: true, scope: ""},
	ActionUserDelete:                   {editor: false, scope: ""},
	ActionUserManage:                   {editor: false, scope: ""},
	ActionUserOwnerGrant:               {editor: false, scope: ""},
	ActionWebhookManage:                {editor: false, scope: ""},
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

// Built-in role ids besides owner and editor. Keep in sync with internal/user (roles_sync_test.go).
const (
	roleAuthor      = "author"
	roleContributor = "contributor"
	roleReviewer    = "reviewer"
	roleReadOnly    = "read-only"
)

// readRights are what a read-only user may do: read the admin and keep their own account.
var readRights = []Action{
	ActionAnalyticsRead, ActionAPIKeyCreatorAct, ActionDashboardRead, ActionEntityRead, ActionFormRead,
	ActionMediaRead, ActionMenuRead, ActionProfileUpdate, ActionRedirectRead, ActionSchemaRead, ActionTermRead,
}

// roleRights holds the rights of the built-in roles other than owner and
// editor. Author, contributor and reviewer start with the read-only rights;
// their content rules (contentRights) are added on top.
var roleRights = map[string]map[Action]bool{
	roleAuthor:      setOf(readRights),
	roleContributor: setOf(readRights),
	roleReviewer:    setOf(readRights),
	roleReadOnly:    setOf(readRights),
}

func setOf(as []Action) map[Action]bool {
	m := make(map[Action]bool, len(as))
	for _, a := range as {
		m[a] = true
	}
	return m
}

// objectRule decides an action for a built-in content role from the object.
type objectRule func(s Subject, o Object) bool

const statusDraft = "draft"

func anyObject(Subject, Object) bool { return true }

// ownObject allows a capability question (empty Object) and objects the subject created.
func ownObject(s Subject, o Object) bool {
	return o.ID == "" || (o.CreatedBy != "" && o.CreatedBy == s.UserID)
}

// ownDraft is ownObject, and a named object must still be a draft.
func ownDraft(s Subject, o Object) bool {
	return ownObject(s, o) && (o.ID == "" || o.Status == statusDraft)
}

// contentRights are the object rules of the content roles, added to their read rights (roleRights).
var contentRights = map[string]map[Action]objectRule{
	roleAuthor: {
		ActionContentWrite: anyObject, ActionEntityCreate: anyObject, ActionEntityUpdate: ownObject,
		ActionEntityPublish: ownObject, ActionEntityUnpublish: ownObject, ActionEntityDelete: ownObject,
		ActionEntityVersionRestore: ownObject, ActionEntityReviewRequest: ownObject,
		ActionMediaUpload: anyObject, ActionMediaUpdate: ownObject, ActionMediaDelete: ownObject,
	},
	roleContributor: {
		ActionContentWrite: anyObject, ActionEntityCreate: anyObject, ActionEntityUpdate: ownDraft,
		ActionEntityDelete: ownDraft, ActionEntityVersionRestore: ownDraft,
		ActionEntityReviewRequest: ownObject, // any status: their own published entry may be sent
		ActionMediaUpload:         anyObject, ActionMediaUpdate: ownObject, ActionMediaDelete: ownObject,
	},
	roleReviewer: {
		ActionContentWrite: anyObject, ActionEntityUpdate: anyObject, ActionEntityPublish: anyObject,
		ActionEntityUnpublish: anyObject, ActionEntityVersionRestore: anyObject,
		ActionEntityReviewBypass: anyObject, ActionEntityReviewDecide: anyObject, ActionEntityReviewRequest: anyObject,
		ActionMediaUpload: anyObject, ActionMediaUpdate: ownObject, ActionMediaDelete: ownObject,
	},
}

// roleAllows reports whether a user with role may do a under rule r on o. Owners and editors ignore the
// object. An unknown role may do nothing.
func roleAllows(role string, a Action, r rule, s Subject, o Object) bool {
	switch role {
	case "owner":
		return true
	case "editor":
		return r.editor
	}
	if roleRights[role][a] {
		return true
	}
	if f := contentRights[role][a]; f != nil {
		return f(s, o)
	}
	return false
}

type defaultAuthorizer struct{}

// Can implements Authorizer with the built-in rules. Owners and editors ignore the object; the author,
// contributor and reviewer roles decide some actions from its CreatedBy and Status.
func (defaultAuthorizer) Can(_ context.Context, s Subject, a Action, o Object) (bool, error) {
	r, ok := table[a]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownAction, a)
	}
	if s.KeyID != "" {
		// A valid key always has a creator (all three key entry points reject
		// one without), so UserID is required here as well.
		creatorOK := s.UserID != "" && roleAllows(s.CreatorRole, a, r, s, o)
		return creatorOK && r.scope != "" && hasScope(s.Scopes, s.CreatorRole, r.scope), nil
	}
	if s.UserID == "" {
		return false, nil
	}
	return roleAllows(s.Role, a, r, s, o), nil
}
