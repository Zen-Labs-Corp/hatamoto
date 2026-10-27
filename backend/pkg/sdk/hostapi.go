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
	FnEntitiesGet    = "entities.get"    // {id} | {path} | {slug, parent_id?, schema_slug? (with parent_id)} -> Entity
	FnEntitiesList   = "entities.list"   // {schema_slug, status?, search?, limit?, cursor?} -> {items, next_cursor}
	FnEntitiesCreate = "entities.create" // {schema_slug, name, slug?, locale?, parent_id?, attributes} -> Entity
	FnEntitiesUpdate = "entities.update" // {id, name?, slug?, locale?, parent_id? (null = root), attributes?} -> Entity
	FnEntitiesStatus = "entities.set_status"
	FnEntitiesDelete = "entities.delete"

	FnTermsList = "terms.list" // {kind?, search?, limit?} -> {items}
	FnTermsGet  = "terms.get"  // {kind, slug} -> Term
	FnTermsSave = "terms.save" // {kind, slug, name, description?} -> Term (create or update)
	FnTermsDel  = "terms.delete"

	FnMenusGet  = "menus.get"  // {slug} -> Menu with items
	FnMenusList = "menus.list" // {} -> {items}
	FnMenusSave = "menus.save" // {slug, name, location?} -> Menu (create or update)

	FnMenusAddItem = "menus.add_item"    // {menu_slug, label, link_type, url?, entity_id?, parent_id?, open_in_new_tab?}
	FnMenusDelItem = "menus.delete_item" // {id}

	FnMediaGet    = "media.get"    // {id}
	FnMediaList   = "media.list"   // {limit?, offset?}
	FnMediaUpload = "media.upload" // {filename, mime_type, data_b64} -> Media

	FnRedirectsList   = "redirects.list"
	FnRedirectsCreate = "redirects.create" // {from_path, to_path, status_code?}
	FnRedirectsDelete = "redirects.delete" // {id}

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
