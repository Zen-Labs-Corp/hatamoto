package sdk

import (
	"encoding/json/jsontext"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// HostFunc is the name of the one host function every plugin may import
// (wasm module "extism:host/user"). It takes a pointer to a JSON CallRequest in
// Extism memory and returns a pointer to a JSON CallResponse.
const HostFunc = "hm_call"

// CallRequest is the input of hm_call: the capability function and its
// arguments (see docs/PLUGIN_API.md for each function's shape).
type CallRequest struct {
	Fn   string         `json:"fn"`
	Args jsontext.Value `json:"args,omitempty"`
}

// CallResponse is the output of hm_call. Exactly one of Result and Error is set.
type CallResponse struct {
	OK     bool           `json:"ok"`
	Result jsontext.Value `json:"result,omitempty"`
	Error  *HostError     `json:"error,omitzero"`
}

// Error codes of a failed host call.
const (
	ErrCapabilityDenied = "capability_denied" // capability not granted; HostError.Capability names it
	ErrInvalidArgument  = "invalid_argument"  // malformed or out-of-range arguments
	ErrNotFound         = "not_found"
	ErrRateLimited      = "rate_limited"
	ErrBlocked          = "blocked" // refused by policy (http.fetch allowlist, SSRF guard, SQL guard)
	ErrReentrant        = "reentrant"
	ErrTimeout          = "timeout"
	ErrUnavailable      = "unavailable" // the backing service is not configured or reachable
	ErrInternal         = "internal"
	ErrUnknownFunction  = "unknown_function"
	ErrPhaseDenied      = "phase_denied" // the function is not available in this run phase, or outside a run
	ErrNotOwned         = "not_owned"    // the object belongs to the owner or another source; a run may not change it
)

// HostError is the typed error of a failed host call.
//
// A call refused for a missing grant has Code ErrCapabilityDenied and the
// permission in Capability: {"code":"capability_denied","capability":"kv"}.
type HostError struct {
	Code       string `json:"code"`
	Capability string `json:"capability,omitempty"`
	Message    string `json:"message"`
}

func (e *HostError) Error() string { return fmt.Sprintf("hatamoto host: %s: %s", e.Code, e.Message) }

// Host function names, by capability.
const (
	FnEntitiesGet  = "entities.get"  // {id} | {path} | {slug, parent_id?, schema_slug? (with parent_id)} -> Entity; a locale-prefixed path is read only for plugins requiring 0.15+; terms ([]EntityTerm) is returned only to plugins requiring 0.20+ with terms.read
	FnEntitiesList = "entities.list" // {schema_slug, status?, search?, term? ({kind, slug}, needs terms.read), limit?, cursor?} -> {items, next_cursor}; items carry terms ([]EntityTerm) for plugins requiring 0.20+ with terms.read
	// FnEntitiesTranslations lists the entities in the translation group of
	// {id}: [{id, locale, status, path?}], empty when it has none.
	FnEntitiesTranslations = "entities.translations"

	FnTermsList = "terms.list" // {kind?, search?, limit?} -> {items}
	FnTermsGet  = "terms.get"  // {kind, slug} -> Term

	FnMenusGet  = "menus.get"  // {slug} -> Menu with items
	FnMenusList = "menus.list" // {} -> {items}

	FnMediaGet  = "media.get"  // {id}
	FnMediaList = "media.list" // {limit?, offset?}

	FnRedirectsList = "redirects.list"

	FnFormsList        = "forms.list"
	FnFormsSubmissions = "forms.submissions" // {form_slug, limit?, offset?}

	FnCommentsThread = "comments.thread" // {entity_id, page?, include_dead? (honoured only with comments.moderate)} -> CommentThread; args CommentsThreadArgs
	FnCommentsCounts = "comments.counts" // {entity_ids (1..100)} -> CommentCounts (approved only)

	FnSettingsGet     = "settings.get"     // {} -> the plugin's own settings object
	FnLog             = "log"              // {level, message}
	FnMailSend        = "mail.send"        // {to[], subject, text?, html?}
	FnHTTPFetch       = "http.fetch"       // {method?, url, headers?, body?, body_b64?, timeout_ms?} -> {status, headers, body, body_b64?}
	FnPackagesInspect = "packages.inspect" // {url, signer_key?} -> InspectResult
	FnKVGet           = "kv.get"           // {key} -> {found, value}
	FnKVSet           = "kv.set"           // {key, value, ttl_seconds?}
	FnKVDelete        = "kv.delete"        // {key}
	FnKVList          = "kv.list"          // {prefix?, limit?} -> {keys}
	FnDBQuery         = "db.query"         // {sql, args?} -> {columns, rows, truncated}
	FnDBExec          = "db.exec"          // {sql, args?} -> {rows_affected}
)

// Host functions of the SDK 0.27 contract. entities.create|update|delete reuse the
// names SDK 0.17 removed; the removed permission entities.write stays removed.
const (
	FnEntitiesCreate    = "entities.create"     // entities.write.own: EntityWrite -> Entity (as entities.get); entity_schema only; status "published" (default) or "draft"; origin plugin:<name>
	FnEntitiesUpdate    = "entities.update"     // entities.write.own: EntityWrite{id, name?, attributes? (entity_fields only), status?} -> Entity; own-origin entries of entity_schema
	FnEntitiesDelete    = "entities.delete"     // entities.write.own: EntityDeleteArgs -> {}; hard delete of an own-origin entry of entity_schema
	FnCommentsMarkDead  = "comments.mark_dead"  // comments.moderate: CommentIDsArgs (1..MaxCommentIDs) -> CommentsChanged
	FnCommentsRestore   = "comments.restore"    // comments.moderate: CommentIDsArgs (1..MaxCommentIDs) -> CommentsChanged
	FnCommentsSetRank   = "comments.set_rank"   // comments.moderate: CommentRankArgs (1..MaxCommentRankItems) -> CommentsChanged
	FnCommentsSetViewer = "comments.set_viewer" // comments.moderate: CommentViewerArgs -> {}
	FnCommentsByAuthor  = "comments.by_author"  // comments.read: CommentsByAuthorArgs -> CommentThread (newest first, 50 per page; approved, and dead only with include_dead + comments.moderate on the plugin's own type; listed by the comment's own status, so a reply under a hidden parent still appears)
	FnCommentsSubtree   = "comments.subtree"    // comments.read: CommentSubtreeArgs -> CommentSubtree
)

// Host functions of the SDK 0.29 contract.
const (
	// comments.identity: CommentsIdentityArgs -> CommentsIdentityEnsured
	FnCommentsIdentityEnsure = "comments.identity.ensure"
	// comments.identity: CommentsIdentityArgs -> CommentsIdentityRevoked
	FnCommentsIdentityRevoke = "comments.identity.revoke"
)

// MaxIdentityAccountBytes caps a plugin account id.
const MaxIdentityAccountBytes = 128

// CommentsIdentityArgs names one of the plugin's own accounts.
type CommentsIdentityArgs struct {
	Account string `json:"account"`
}

// CommentsIdentityEnsured answers comments.identity.ensure: the plugin-scoped
// key of the account's commenter, stable across calls.
type CommentsIdentityEnsured struct {
	CommenterKey string `json:"commenter_key"`
}

// CommentsIdentityRevoked answers comments.identity.revoke: false when the
// plugin has no commenter for the account.
type CommentsIdentityRevoked struct {
	Revoked bool `json:"revoked"`
}

// ValidIdentityAccount reports whether s is a usable plugin account id: 1 to
// MaxIdentityAccountBytes bytes of valid UTF-8 without control characters.
func ValidIdentityAccount(s string) bool {
	if s == "" || len(s) > MaxIdentityAccountBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CommenterSession asks the host to sign this browser in as the commenter of
// Account, or out (SignOut). Honoured only on a non-GET public route response
// of a plugin holding comments.identity, from a same-origin request.
type CommenterSession struct {
	Account string `json:"account,omitempty"`
	// Link binds the browser's current commenter to Account when that
	// commenter belongs to no plugin account yet. The binding cannot be
	// undone and the host cannot prove the browser's commenter is the
	// account's owner: send Link only from an explicit connect action by
	// the signed-in user, never on a routine signup or login.
	Link    bool `json:"link,omitempty"`
	SignOut bool `json:"sign_out,omitempty"`
	// MaxAge is the cookie lifetime in seconds; 0 means the site's
	// device_cookie_days, and a larger value is capped to it.
	MaxAge int `json:"max_age,omitempty"`
}

// Valid checks the shape: SignOut alone, or a valid Account with a
// non-negative MaxAge.
func (s CommenterSession) Valid() error {
	if s.SignOut {
		if s.Account != "" || s.Link || s.MaxAge != 0 {
			return fmt.Errorf("sdk: commenter_session: sign_out takes no other field")
		}
		return nil
	}
	if !ValidIdentityAccount(s.Account) {
		return fmt.Errorf("sdk: commenter_session: account must be 1 to %d bytes of text without control characters", MaxIdentityAccountBytes)
	}
	if s.MaxAge < 0 {
		return fmt.Errorf("sdk: commenter_session: max_age must not be negative")
	}
	return nil
}

// Limits of the SDK 0.27 comment host functions.
const (
	MaxCommentIDs       = 100
	MaxCommentRankItems = 200
)

// EntityWrite is the argument of entities.create and entities.update (SDK 0.27).
type EntityWrite struct {
	ID         string         `json:"id,omitempty"` // update only
	Name       string         `json:"name,omitempty"`
	Slug       string         `json:"slug,omitempty"`   // create only
	Locale     string         `json:"locale,omitempty"` // create only
	Attributes jsontext.Value `json:"attributes,omitempty"`
	Status     string         `json:"status,omitempty"` // "published" (create default) or "draft"
}

// EntityDeleteArgs is the argument of entities.delete (SDK 0.27).
type EntityDeleteArgs struct {
	ID string `json:"id"`
}

// CommentsThreadArgs is the argument of comments.thread.
type CommentsThreadArgs struct {
	EntityID    string `json:"entity_id"`
	Page        int    `json:"page,omitempty"`
	IncludeDead bool   `json:"include_dead,omitempty"` // honoured only with comments.moderate
}

// CommentIDsArgs is the argument of comments.mark_dead and comments.restore (SDK 0.27).
type CommentIDsArgs struct {
	IDs []string `json:"ids"`
}

// CommentRankItem is one comment's rank in comments.set_rank (SDK 0.27).
type CommentRankItem struct {
	ID   string  `json:"id"`
	Rank float64 `json:"rank"`
}

// CommentRankArgs is the argument of comments.set_rank (SDK 0.27).
type CommentRankArgs struct {
	Items []CommentRankItem `json:"items"`
}

// CommentsChanged is the result of the comment moderation functions (SDK 0.27).
type CommentsChanged struct {
	Changed int `json:"changed"`
}

// CommentViewerArgs is the argument of comments.set_viewer (SDK 0.27).
type CommentViewerArgs struct {
	CommenterKey string `json:"commenter_key"`
	ShowDead     bool   `json:"show_dead"`
}

// CommentsByAuthorArgs is the argument of comments.by_author (SDK 0.27).
type CommentsByAuthorArgs struct {
	CommenterKey string `json:"commenter_key"`
	Page         int    `json:"page,omitempty"`
	IncludeDead  bool   `json:"include_dead,omitempty"` // honoured only with comments.moderate
}

// CommentSubtreeArgs is the argument of comments.subtree (SDK 0.27).
type CommentSubtreeArgs struct {
	CommentID   string `json:"comment_id"`
	Page        int    `json:"page,omitempty"`
	IncludeDead bool   `json:"include_dead,omitempty"` // honoured only with comments.moderate
}

// Host functions of a run (SDK 0.17.0). They work only inside a run, in the
// phase noted, and need no permission. Content is written by staging items
// into the run's changeset; nothing is live until the owner publishes it.
const (
	FnRunInfo       = "run.info"       // both phases: {} -> RunInfo
	FnRunCheckpoint = "run.checkpoint" // both phases: {cursor} -> {}; the cursor of the next on_run_step
	FnRunProgress   = "run.progress"   // both phases: {done, total?, message?} -> {}
	FnRunFinish     = "run.finish"     // both phases: {summary?} -> {}; ends the current phase
	FnRunFail       = "run.fail"       // both phases: {reason} -> {}; fails the run and discards its changeset

	FnUploadRead     = "upload.read"      // fetch: {upload_id, offset, len} -> {data_b64, size, eof}
	FnBufferPut      = "buffer.put"       // fetch: {key, value_b64} -> {}
	FnBufferGet      = "buffer.get"       // transform: {key} -> {found, value_b64}
	FnBufferList     = "buffer.list"      // transform: {prefix?, after?, limit?} -> {keys, next}
	FnMediaImportURL = "media.import_url" // fetch: {url, filename?} -> {handle}

	FnSiteInfo          = "site.info"           // transform: {} -> SiteInfo
	FnEntitiesSlugTaken = "entities.slug_taken" // transform: {schema, slug, locale?} -> {taken}

	FnChangesetEntityUpsert   = "changeset.entity.upsert"   // transform: EntityUpsert -> UpsertResult
	FnChangesetTermUpsert     = "changeset.term.upsert"     // transform: TermUpsert -> UpsertResult
	FnChangesetPersonUpsert   = "changeset.person.upsert"   // transform: PersonUpsert -> UpsertResult
	FnChangesetMediaAttach    = "changeset.media.attach"    // transform: MediaAttach -> UpsertResult
	FnChangesetMenuUpsert     = "changeset.menu.upsert"     // transform: MenuUpsert -> UpsertResult
	FnChangesetRedirectUpsert = "changeset.redirect.upsert" // transform: RedirectUpsert -> UpsertResult
	FnChangesetDelete         = "changeset.delete"          // transform: {kind, source_key} -> UpsertResult
	FnChangesetPropose        = "changeset.propose"         // transform: Proposal -> {}
	FnChangesetMediaAlt       = "changeset.media.alt"       // transform: MediaAlt -> UpsertResult
)

// HTTPRequest is the input of an `http_handle` export: a request to one of the
// plugin's declared routes.
type HTTPRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"` // below /api/v1/p/<plugin> or /p/<plugin>, starts with "/"; always the path the route was matched and authorised on
	// Pattern is the manifest route path that matched (e.g. "/items/{id}") and
	// PathValues its wildcard values, so a plugin need not re-route by hand.
	Pattern    string              `json:"pattern,omitempty"`
	PathValues map[string]string   `json:"path_values,omitempty"`
	Query      map[string][]string `json:"query,omitempty"`
	Headers    map[string]string   `json:"headers,omitempty"` // allowlisted request headers, lower-case names
	Body       string              `json:"body,omitempty"`
	BodyB64    string              `json:"body_b64,omitempty"` // set instead of Body when the body is not UTF-8
	User       *HTTPUser           `json:"user,omitzero"`      // nil on public routes
	// Cookies are the request cookies the manifest's `cookies` list declares,
	// by their short names. Public routes only.
	Cookies map[string]string `json:"cookies,omitempty"`
	// CommenterKey (needs comments.read), AddrKey and NetKey (need client.key) are
	// per-plugin pseudonymous keys of the signed-in commenter and of the visitor's
	// address and network (SDK 0.27). Public routes only, empty on admin routes
	// and absent without the grant.
	CommenterKey string `json:"commenter_key,omitempty"`
	AddrKey      string `json:"addr_key,omitempty"`
	NetKey       string `json:"net_key,omitempty"`
}

// HTTPUser is the signed-in user behind an admin route request.
type HTTPUser struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// HTTPResponse is the output of `http_handle`. Only allowlisted headers
// (Content-Type, Cache-Control, Location, Content-Disposition, ETag) are
// forwarded; Status defaults to 200.
type HTTPResponse struct {
	Status  int               `json:"status,omitzero"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
	// SetCookies are cookies to set on a public route's response: declared
	// names only, stored as hmp_<plugin>_<name>, with the limits and flags of
	// render.page's cookies. An admin route that sets one fails.
	SetCookies []SetCookie `json:"set_cookies,omitempty"`
	// CommenterSession signs the browser in or out as a commenter (SDK 0.29,
	// permission comments.identity, non-GET public routes only).
	CommenterSession *CommenterSession `json:"commenter_session,omitempty"`
}

// CronEvent is the input of an `on_cron_<name>` export.
type CronEvent struct {
	Name        string `json:"name"`
	ScheduledAt string `json:"scheduled_at"` // RFC 3339
}

// InspectRequest is the argument of packages.inspect (SDK 0.21.0). URL must be
// https. SignerKey is the base64 ed25519 public key the package is expected to
// be signed by.
type InspectRequest struct {
	URL       string `json:"url"`
	SignerKey string `json:"signer_key,omitempty"`
}

// InspectResult is what packages.inspect returns. A package that cannot be
// fetched, parsed or verified is reported in Problems, not as an error. The
// package body is never part of the result.
type InspectResult struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	SHA256       string   `json:"sha256"`
	Size         int64    `json:"size"`
	SignerKeyID  string   `json:"signer_key_id"` // the package's own signer id, "" if unsigned
	SignatureOK  bool     `json:"signature_ok"`
	SDK          string   `json:"sdk,omitempty"`
	Permissions  []string `json:"permissions,omitempty"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	Description  string   `json:"description,omitempty"`
	Problems     []string `json:"problems"` // never nil on the wire
}

// MailQuota is the owner-set mail.send allowance of a plugin (SDK 0.22.1).
type MailQuota struct {
	RecipientsPerHour int `json:"recipients_per_hour"`
}

// PermissionsResult is what the permissions host function returns. Mail is
// present only for a plugin granted mail.send.
type PermissionsResult struct {
	Granted   []Permission `json:"granted"`
	Requested []string     `json:"requested"`
	Mail      *MailQuota   `json:"mail,omitempty"`
}
