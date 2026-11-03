package sdk

import (
	"encoding/json"
	"fmt"
)

// HostFunc is the name of the one host function every plugin may import
// (wasm module "extism:host/user"). It takes a pointer to a JSON CallRequest in
// Extism memory and returns a pointer to a JSON CallResponse.
const HostFunc = "hm_call"

// CallRequest is the input of hm_call: the capability function and its
// arguments (see docs/PLUGIN_API.md for each function's shape).
type CallRequest struct {
	Fn   string          `json:"fn"`
	Args json.RawMessage `json:"args,omitempty"`
}

// CallResponse is the output of hm_call. Exactly one of Result and Error is set.
type CallResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *HostError      `json:"error,omitempty"`
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
	FnEntitiesGet  = "entities.get"  // {id} | {path} | {slug, parent_id?, schema_slug? (with parent_id)} -> Entity; a locale-prefixed path is read only for plugins requiring 0.15+
	FnEntitiesList = "entities.list" // {schema_slug, status?, search?, limit?, cursor?} -> {items, next_cursor}
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

	FnSettingsGet = "settings.get" // {} -> the plugin's own settings object
	FnLog         = "log"          // {level, message}
	FnMailSend    = "mail.send"    // {to[], subject, text?, html?}
	FnHTTPFetch   = "http.fetch"   // {method?, url, headers?, body?, body_b64?, timeout_ms?} -> {status, headers, body, body_b64?}
	FnKVGet       = "kv.get"       // {key} -> {found, value}
	FnKVSet       = "kv.set"       // {key, value, ttl_seconds?}
	FnKVDelete    = "kv.delete"    // {key}
	FnKVList      = "kv.list"      // {prefix?, limit?} -> {keys}
	FnDBQuery     = "db.query"     // {sql, args?} -> {columns, rows, truncated}
	FnDBExec      = "db.exec"      // {sql, args?} -> {rows_affected}
)

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
	User       *HTTPUser           `json:"user,omitempty"`     // nil on public routes
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
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// CronEvent is the input of an `on_cron_<name>` export.
type CronEvent struct {
	Name        string `json:"name"`
	ScheduledAt string `json:"scheduled_at"` // RFC 3339
}
