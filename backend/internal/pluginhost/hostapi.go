package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/time/rate"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// Mailer is the slice of the email service mail.send uses.
type Mailer interface {
	IsConfigured() bool
	Send(ctx context.Context, to []string, subject, htmlBody, textBody string) error
}

// EntityPaths gives the entities.* host functions the public site's view of
// an entity: where it lives and which entity lives at a path. The server
// implements it over the URL registry and the public resolver, so a plugin
// sees exactly the URLs the site serves.
type EntityPaths interface {
	// Path is the canonical public path of ent ("/a/b").
	Path(ctx context.Context, ent *entity.Entity) string
	// Resolve returns the published entity served at path, or
	// entity.ErrEntityNotFound.
	Resolve(ctx context.Context, path string) (*entity.Entity, error)
	// BaseURL is the configured public origin ("" when none is set).
	BaseURL(ctx context.Context) string
}

// Services are the core services host functions call. A nil service makes its
// functions fail with "unavailable".
type Services struct {
	Entities  *entity.Service
	Paths     EntityPaths // optional: without it entities carry no path/url and cannot be read by path
	Terms     *taxonomy.Service
	Menus     *menu.Service
	Media     *media.Service
	Redirects *redirect.Service
	Forms     *form.Service
	Mail      Mailer
	Settings  *SettingsStore
	DB        *PluginDB
	Fetcher   *Fetcher
	// FetchPerMinute caps one plugin's outbound http.fetch requests per
	// minute; 0 means DefaultFetchPerMinute.
	FetchPerMinute int
}

// DefaultFetchPerMinute is the outbound request cap per plugin.
const DefaultFetchPerMinute = 600

// hostCallTimeout bounds one host function call (the hook call's own deadline
// applies as well).
const hostCallTimeout = 15 * time.Second

// maxHostResultBytes bounds what one host function returns to a guest.
const maxHostResultBytes = 2 << 20

type depthKey struct{}

// entityWriteDepth is how many plugin entity writes the current call chain is
// inside: a write sets it to 1 for the hooks it triggers.
func entityWriteDepth(ctx context.Context) int {
	d, _ := ctx.Value(depthKey{}).(int)
	return d
}

// API answers a plugin's hm_call requests. Every call is permission checked
// against the plugin's grants and bound to a deadline.
type API struct {
	svc      Services
	grants   Grants
	manifest func(plugin string) (sdk.Manifest, bool)
	log      *slog.Logger

	mu       sync.Mutex
	limiters map[string]*pluginLimits
}

type pluginLimits struct {
	log, mail, fetch *rate.Limiter
}

// NewAPI returns the host-function dispatcher. manifest looks up a loaded
// plugin's manifest.
func NewAPI(svc Services, grants Grants, manifest func(string) (sdk.Manifest, bool), log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{svc: svc, grants: grants, manifest: manifest, log: log, limiters: map[string]*pluginLimits{}}
}

func (a *API) limits(plugin string) *pluginLimits {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.limiters[plugin]
	if l == nil {
		perMin := a.svc.FetchPerMinute
		if perMin <= 0 {
			perMin = DefaultFetchPerMinute
		}
		l = &pluginLimits{
			log:   rate.NewLimiter(10, 20),                         // 10 lines/s, burst 20
			mail:  rate.NewLimiter(rate.Every(10*time.Second), 10), // 6 recipients/min, burst 10
			fetch: rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMin)), min(perMin, 60)),
		}
		a.limiters[plugin] = l
	}
	return l
}

type callEnv struct {
	plugin   string
	manifest sdk.Manifest
}

type hostFn struct {
	perm sdk.Permission // "" = always allowed
	run  func(ctx context.Context, a *API, e *callEnv, args json.RawMessage) (any, error)
}

func herr(code, format string, v ...any) *sdk.HostError {
	return &sdk.HostError{Code: code, Message: fmt.Sprintf(format, v...)}
}

// Call implements wasmhost.HostCall.
func (a *API) Call(ctx context.Context, plugin string, in []byte) []byte {
	ctx, cancel := context.WithTimeout(ctx, hostCallTimeout)
	defer cancel()
	res, err := a.dispatch(ctx, plugin, in)
	resp := sdk.CallResponse{OK: err == nil}
	if err == nil {
		b, merr := json.Marshal(res)
		switch {
		case merr != nil:
			err = herr(sdk.ErrInternal, "result not encodable")
		case len(b) > maxHostResultBytes:
			err = herr(sdk.ErrInvalidArgument, "result over %d bytes; ask for less", maxHostResultBytes)
		default:
			resp.Result = b
		}
	}
	if err != nil {
		resp.OK = false
		resp.Error = a.toHostError(plugin, err)
	}
	out, _ := json.Marshal(resp)
	return out
}

func (a *API) toHostError(plugin string, err error) *sdk.HostError {
	var he *sdk.HostError
	switch {
	case errors.As(err, &he):
		return he
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return herr(sdk.ErrTimeout, "call timed out")
	case errors.Is(err, ErrFetchBlocked), errors.Is(err, ErrSQLBlocked):
		return herr(sdk.ErrBlocked, "%s", err.Error())
	case errors.Is(err, ErrRoleUnavailable):
		return herr(sdk.ErrUnavailable, "%s", err.Error())
	case isNotFound(err):
		return herr(sdk.ErrNotFound, "%s", err.Error())
	case isInvalid(err):
		return herr(sdk.ErrInvalidArgument, "%s", err.Error())
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "42501": // insufficient_privilege: the plugin role may not touch that
			return herr(sdk.ErrBlocked, "database: %s", pe.Message)
		case "57014": // query_canceled: statement_timeout
			return herr(sdk.ErrTimeout, "database: statement timeout")
		}
		return herr(sdk.ErrInvalidArgument, "database: %s", pe.Message)
	}
	a.log.Error("host function failed", slog.String("plugin", plugin), slog.Any("error", err))
	return herr(sdk.ErrInternal, "internal error")
}

func isNotFound(err error) bool {
	for _, e := range []error{
		entity.ErrEntityNotFound, entity.ErrSchemaNotFound, taxonomy.ErrTermNotFound, menu.ErrMenuNotFound,
		menu.ErrMenuItemNotFound, media.ErrMediaNotFound, redirect.ErrRedirectNotFound, form.ErrFormNotFound,
		form.ErrSubmissionNotFound,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func isInvalid(err error) bool {
	for _, e := range []error{
		entity.ErrValidationFailed, entity.ErrSlugAlreadyExists, entity.ErrInvalidStatus, entity.ErrRejectedByPlugin,
		entity.ErrParentNotFound, entity.ErrParentCycle, entity.ErrParentTooDeep, entity.ErrAmbiguousSlug, entity.ErrAuthorNotFound, entity.ErrHasChildren,
		entity.ErrInvalidDoc, entity.ErrPageSectionsRemoved,
		taxonomy.ErrTermSlugConflict, menu.ErrSlugAlreadyExists, menu.ErrInvalidLinkType, menu.ErrInvalidLocation,
		media.ErrInvalidMimeType, media.ErrFileTooLarge, media.ErrInvalidFile, media.ErrImageTooLarge,
		redirect.ErrDuplicateSourcePath, redirect.ErrInvalidStatusCode, redirect.ErrInvalidPattern, redirect.ErrUnsafePattern,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func (a *API) dispatch(ctx context.Context, plugin string, in []byte) (any, error) {
	var req sdk.CallRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, herr(sdk.ErrInvalidArgument, "request is not a JSON {fn, args}")
	}
	fn, ok := hostFns[req.Fn]
	if !ok {
		return nil, herr(sdk.ErrUnknownFunction, "no host function %q", req.Fn)
	}
	if fn.perm != "" && !a.grants.Has(plugin, fn.perm) {
		he := herr(sdk.ErrCapabilityDenied, "%s needs the %q permission, which the owner has not granted", req.Fn, fn.perm)
		he.Capability = fn.perm
		return nil, he
	}
	m, ok := a.manifest(plugin)
	if !ok {
		return nil, herr(sdk.ErrUnavailable, "plugin not loaded")
	}
	return fn.run(ctx, a, &callEnv{plugin: plugin, manifest: m}, req.Args)
}

// decodeArgs strictly decodes args into T (unknown fields are an error: a typo
// should not silently do nothing).
func decodeArgs[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, herr(sdk.ErrInvalidArgument, "bad arguments: %v", err)
	}
	return v, nil
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, herr(sdk.ErrInvalidArgument, "bad id %q", s)
	}
	return id, nil
}

func unavailable(what string) error { return herr(sdk.ErrUnavailable, "%s is not available", what) }

func clamp(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

// HostFunctionsFor returns the names of the host functions a plugin holding
// perms may call (functions that need no permission included), sorted.
func HostFunctionsFor(perms []sdk.Permission) []string {
	var out []string
	for name, f := range hostFns {
		if f.perm == "" || slices.Contains(perms, f.perm) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// hostFns is the table of host functions. Names and permissions are the
// contract documented in docs/PLUGIN_API.md.
var hostFns = map[string]hostFn{
	"permissions":          {run: fnPermissions},
	sdk.FnLog:              {perm: sdk.PermLog, run: fnLog},
	sdk.FnSettingsGet:      {perm: sdk.PermSettings, run: fnSettingsGet},
	sdk.FnMailSend:         {perm: sdk.PermMailSend, run: fnMailSend},
	sdk.FnHTTPFetch:        {perm: sdk.PermHTTPFetch, run: fnHTTPFetch},
	sdk.FnKVGet:            {perm: sdk.PermKV, run: fnKVGet},
	sdk.FnKVSet:            {perm: sdk.PermKV, run: fnKVSet},
	sdk.FnKVDelete:         {perm: sdk.PermKV, run: fnKVDelete},
	sdk.FnKVList:           {perm: sdk.PermKV, run: fnKVList},
	sdk.FnDBQuery:          {perm: sdk.PermDB, run: fnDBQuery},
	sdk.FnDBExec:           {perm: sdk.PermDB, run: fnDBExec},
	sdk.FnEntitiesGet:      {perm: sdk.PermEntitiesRead, run: fnEntitiesGet},
	sdk.FnEntitiesList:     {perm: sdk.PermEntitiesRead, run: fnEntitiesList},
	sdk.FnEntitiesCreate:   {perm: sdk.PermEntitiesWrite, run: fnEntitiesCreate},
	sdk.FnEntitiesUpdate:   {perm: sdk.PermEntitiesWrite, run: fnEntitiesUpdate},
	sdk.FnEntitiesStatus:   {perm: sdk.PermEntitiesWrite, run: fnEntitiesStatus},
	sdk.FnEntitiesDelete:   {perm: sdk.PermEntitiesWrite, run: fnEntitiesDelete},
	sdk.FnTermsList:        {perm: sdk.PermTermsRead, run: fnTermsList},
	sdk.FnTermsGet:         {perm: sdk.PermTermsRead, run: fnTermsGet},
	sdk.FnTermsSave:        {perm: sdk.PermTermsWrite, run: fnTermsSave},
	sdk.FnTermsDel:         {perm: sdk.PermTermsWrite, run: fnTermsDelete},
	sdk.FnMenusList:        {perm: sdk.PermMenusRead, run: fnMenusList},
	sdk.FnMenusGet:         {perm: sdk.PermMenusRead, run: fnMenusGet},
	sdk.FnMenusSave:        {perm: sdk.PermMenusWrite, run: fnMenusSave},
	sdk.FnMenusAddItem:     {perm: sdk.PermMenusWrite, run: fnMenusAddItem},
	sdk.FnMenusDelItem:     {perm: sdk.PermMenusWrite, run: fnMenusDeleteItem},
	sdk.FnMediaGet:         {perm: sdk.PermMediaRead, run: fnMediaGet},
	sdk.FnMediaList:        {perm: sdk.PermMediaRead, run: fnMediaList},
	sdk.FnMediaUpload:      {perm: sdk.PermMediaWrite, run: fnMediaUpload},
	sdk.FnRedirectsList:    {perm: sdk.PermRedirectsRead, run: fnRedirectsList},
	sdk.FnRedirectsCreate:  {perm: sdk.PermRedirectsWrite, run: fnRedirectsCreate},
	sdk.FnRedirectsDelete:  {perm: sdk.PermRedirectsWrite, run: fnRedirectsDelete},
	sdk.FnFormsList:        {perm: sdk.PermFormsRead, run: fnFormsList},
	sdk.FnFormsSubmissions: {perm: sdk.PermFormsRead, run: fnFormsSubmissions},
}

func fnPermissions(_ context.Context, a *API, e *callEnv, _ json.RawMessage) (any, error) {
	g := a.grants.Granted(e.plugin)
	if g == nil {
		g = []sdk.Permission{}
	}
	return map[string]any{"granted": g, "requested": e.manifest.Permissions}, nil
}

// --- log, settings, mail, fetch ----------------------------------------------

func fnLog(_ context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Level, Message string }](raw)
	if err != nil {
		return nil, err
	}
	if !a.limits(e.plugin).log.Allow() {
		return nil, herr(sdk.ErrRateLimited, "log rate limit (10 lines/s, burst 20)")
	}
	if len(args.Message) > 4<<10 {
		args.Message = args.Message[:4<<10] + "...(truncated)"
	}
	lvl := slog.LevelInfo
	switch args.Level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	a.log.Log(context.Background(), lvl, args.Message, slog.String("plugin", e.plugin))
	return map[string]any{}, nil
}

func fnSettingsGet(ctx context.Context, a *API, e *callEnv, _ json.RawMessage) (any, error) {
	if a.svc.Settings == nil {
		return nil, unavailable("settings")
	}
	stored, err := a.svc.Settings.Get(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	if len(e.manifest.SettingsSchema) > 0 {
		stored = MergeDefaults(e.manifest.SettingsSchema, stored)
	}
	return json.RawMessage(stored), nil
}

func fnMailSend(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
	}](raw)
	if err != nil {
		return nil, err
	}
	switch {
	case len(args.To) == 0 || len(args.To) > 10:
		return nil, herr(sdk.ErrInvalidArgument, "to: 1 to 10 recipients")
	case args.Subject == "" || len(args.Subject) > 200 || strings.ContainsAny(args.Subject, "\r\n"):
		return nil, herr(sdk.ErrInvalidArgument, "subject: 1 to 200 characters, one line")
	case args.Text == "" && args.HTML == "":
		return nil, herr(sdk.ErrInvalidArgument, "text or html is required")
	case len(args.Text) > 256<<10 || len(args.HTML) > 256<<10:
		return nil, herr(sdk.ErrInvalidArgument, "body over 256 KiB")
	}
	for _, to := range args.To {
		addr, err := mail.ParseAddress(to)
		if err != nil || addr.Address != to {
			return nil, herr(sdk.ErrInvalidArgument, "recipient %q is not a plain address", to)
		}
	}
	if a.svc.Mail == nil || !a.svc.Mail.IsConfigured() {
		return nil, unavailable("mail")
	}
	// One token per recipient: the limit bounds the mails a plugin sends,
	// not the calls it makes.
	if !a.limits(e.plugin).mail.AllowN(time.Now(), len(args.To)) {
		return nil, herr(sdk.ErrRateLimited, "mail rate limit (6 recipients per minute, burst 10)")
	}
	if err := a.svc.Mail.Send(ctx, args.To, args.Subject, args.HTML, args.Text); err != nil {
		return nil, err
	}
	return map[string]any{"sent": len(args.To)}, nil
}

func fnHTTPFetch(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	req, err := decodeArgs[FetchRequest](raw)
	if err != nil {
		return nil, err
	}
	f := a.svc.Fetcher
	if f == nil {
		return nil, unavailable("http.fetch")
	}
	if !a.limits(e.plugin).fetch.Allow() {
		return nil, herr(sdk.ErrRateLimited, "http.fetch rate limit for this plugin reached; try again shortly")
	}
	hosts := e.manifest.AllowedHosts
	if !a.grants.Has(e.plugin, sdk.PermHTTPFetchAny) {
		hosts = withoutWildcard(hosts) // "*" is honoured only with its own grant
	}
	return f.Do(ctx, e.plugin, hosts, req)
}

// --- kv and db ------------------------------------------------------------------

func (a *API) db(ctx context.Context, plugin string) (*PluginDB, error) {
	if a.svc.DB == nil {
		return nil, unavailable("plugin database")
	}
	if err := a.svc.DB.Provision(ctx, plugin); err != nil {
		return nil, err
	}
	return a.svc.DB, nil
}

func checkKVKey(k string) error {
	if k == "" || len(k) > maxKVKeyBytes {
		return herr(sdk.ErrInvalidArgument, "key: 1 to %d bytes", maxKVKeyBytes)
	}
	return nil
}

func fnKVGet(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Key string }](raw)
	if err != nil {
		return nil, err
	}
	if err := checkKVKey(args.Key); err != nil {
		return nil, err
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	v, found, err := db.KVGet(ctx, e.plugin, args.Key)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"found": false}, nil
	}
	return map[string]any{"found": true, "value": json.RawMessage(v)}, nil
}

func fnKVSet(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Key        string          `json:"key"`
		Value      json.RawMessage `json:"value"`
		TTLSeconds int             `json:"ttl_seconds"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if err := checkKVKey(args.Key); err != nil {
		return nil, err
	}
	if len(args.Value) == 0 || len(args.Value) > maxKVValueBytes || !json.Valid(args.Value) {
		return nil, herr(sdk.ErrInvalidArgument, "value: JSON of at most %d bytes", maxKVValueBytes)
	}
	if args.TTLSeconds < 0 {
		return nil, herr(sdk.ErrInvalidArgument, "ttl_seconds must not be negative")
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, db.KVSet(ctx, e.plugin, args.Key, args.Value, time.Duration(args.TTLSeconds)*time.Second)
}

func fnKVDelete(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Key string }](raw)
	if err != nil {
		return nil, err
	}
	if err := checkKVKey(args.Key); err != nil {
		return nil, err
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, db.KVDelete(ctx, e.plugin, args.Key)
}

func fnKVList(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Prefix string
		Limit  int
	}](raw)
	if err != nil {
		return nil, err
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	keys, err := db.KVList(ctx, e.plugin, args.Prefix, clamp(args.Limit, 100, maxKVListLimit))
	if err != nil {
		return nil, err
	}
	return map[string]any{"keys": keys}, nil
}

type sqlArgs struct {
	SQL  string          `json:"sql"`
	Args json.RawMessage `json:"args"`
}

func fnDBQuery(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sqlArgs](raw)
	if err != nil {
		return nil, err
	}
	args, err := DecodeArgs(in.Args)
	if err != nil {
		return nil, herr(sdk.ErrInvalidArgument, "%v", err)
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	return db.Query(ctx, e.plugin, in.SQL, args...)
}

func fnDBExec(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sqlArgs](raw)
	if err != nil {
		return nil, err
	}
	args, err := DecodeArgs(in.Args)
	if err != nil {
		return nil, herr(sdk.ErrInvalidArgument, "%v", err)
	}
	db, err := a.db(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	n, err := db.Exec(ctx, e.plugin, in.SQL, args...)
	return map[string]any{"rows_affected": n}, err
}

// --- entities --------------------------------------------------------------------

type entityDTO struct {
	ID         string `json:"id"`
	SchemaSlug string `json:"schema_slug"`
	Slug       string `json:"slug"`
	Locale     string `json:"locale"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
	// ParentID is the parent entity, absent for a root row. Path is the
	// canonical public path and URL the same with the public origin in front
	// (just the path while no origin is configured); both are absent when the
	// host has no URL routing.
	ParentID    string          `json:"parent_id,omitempty"`
	Path        string          `json:"path,omitempty"`
	URL         string          `json:"url,omitempty"`
	Attributes  json.RawMessage `json:"attributes"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (a *API) entityDTO(ctx context.Context, e *entity.Entity) entityDTO {
	if e.Schema == nil {
		if s, err := a.svc.Entities.GetSchema(ctx, e.SchemaID); err == nil {
			e.Schema = s
		}
	}
	dto := entityDTO{
		ID: e.ID.String(), Slug: e.Slug, Locale: e.Locale, Name: e.Name, Status: string(e.Status),
		Version: e.Version, Attributes: entity.DerivedAttributes(e.Attributes), PublishedAt: e.PublishedAt, UpdatedAt: e.UpdatedAt,
	}
	if e.Schema != nil {
		dto.SchemaSlug = e.Schema.Slug
	}
	if e.ParentID != nil {
		dto.ParentID = e.ParentID.String()
	}
	if a.svc.Paths != nil {
		dto.Path = a.svc.Paths.Path(ctx, e)
		dto.URL = dto.Path
		if base := a.svc.Paths.BaseURL(ctx); base != "" {
			dto.URL = strings.TrimRight(base, "/") + dto.Path
		}
	}
	return dto
}

// optionalID reads an optional entity id argument that may be explicitly null
// (or empty), which means "no parent". set is false when the argument was not
// sent at all.
func optionalID(raw json.RawMessage) (id *uuid.UUID, set bool, err error) {
	if raw == nil {
		return nil, false, nil
	}
	var s *string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, true, herr(sdk.ErrInvalidArgument, "parent_id must be a string or null")
	}
	if s == nil || *s == "" {
		return nil, true, nil
	}
	u, err := parseID(*s)
	if err != nil {
		return nil, true, err
	}
	return &u, true, nil
}

func fnEntitiesGet(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		ID       string          `json:"id"`
		Slug     string          `json:"slug"`
		Path     string          `json:"path"`
		ParentID json.RawMessage `json:"parent_id"`
		// SchemaSlug narrows a {slug, parent_id} lookup to one schema.
		SchemaSlug string `json:"schema_slug"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	selectors := 0
	for _, set := range []bool{args.ID != "", args.Path != "", args.Slug != ""} {
		if set {
			selectors++
		}
	}
	if selectors > 1 {
		return nil, herr(sdk.ErrInvalidArgument, "send only one of id, path, or slug")
	}
	if args.Slug == "" && (args.SchemaSlug != "" || len(args.ParentID) > 0) {
		return nil, herr(sdk.ErrInvalidArgument, "parent_id and schema_slug go with slug")
	}
	var ent *entity.Entity
	switch {
	case args.ID != "":
		id, err := parseID(args.ID)
		if err != nil {
			return nil, err
		}
		ent, err = a.svc.Entities.GetEntity(ctx, id)
		if err != nil {
			return nil, err
		}
	case args.Path != "":
		if a.svc.Paths == nil {
			return nil, unavailable("entity paths")
		}
		if !strings.HasPrefix(args.Path, "/") || strings.ContainsAny(args.Path, "?#") {
			return nil, herr(sdk.ErrInvalidArgument, "path must be an absolute url path such as /a/b")
		}
		if ent, err = a.svc.Paths.Resolve(ctx, args.Path); err != nil {
			return nil, err
		}
	case args.Slug != "":
		parent, set, err := optionalID(args.ParentID)
		if err != nil {
			return nil, err
		}
		if set {
			ent, err = a.svc.Entities.GetEntityByParentSlug(ctx, parent, args.Slug, args.SchemaSlug)
		} else {
			if args.SchemaSlug != "" {
				return nil, herr(sdk.ErrInvalidArgument, "schema_slug needs parent_id (null for a root)")
			}
			ent, err = a.svc.Entities.GetEntityBySlug(ctx, args.Slug)
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, herr(sdk.ErrInvalidArgument, "id, path, or slug (with an optional parent_id) is required")
	}
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesList(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		SchemaSlug string `json:"schema_slug"`
		Status     string `json:"status"`
		Search     string `json:"search"`
		Limit      int    `json:"limit"`
		Cursor     string `json:"cursor"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	filter := &entity.ListEntitiesFilter{Search: args.Search}
	if args.SchemaSlug != "" {
		s, err := a.svc.Entities.GetSchemaBySlug(ctx, args.SchemaSlug)
		if err != nil {
			return nil, err
		}
		filter.SchemaID = &s.ID
	}
	if args.Status != "" {
		st := entity.EntityStatus(args.Status)
		if !entity.ValidStatus(st) {
			return nil, herr(sdk.ErrInvalidArgument, "status %q", args.Status)
		}
		filter.Status = &st
	}
	page := &entity.KeysetPage{Limit: clamp(args.Limit, 50, 200), Direction: "next"}
	if args.Cursor != "" {
		page.Cursor = &args.Cursor
	}
	res, err := a.svc.Entities.ListEntities(ctx, filter, page)
	if err != nil {
		return nil, err
	}
	items := make([]entityDTO, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, a.entityDTO(ctx, it))
	}
	return map[string]any{"items": items, "next_cursor": res.NextCursor}, nil
}

// beginEntityWrite enforces the re-entrancy guard: a plugin write may trigger
// hooks (entity.before_save, entity.created, ...), but a write made from inside
// those hooks is refused, so plugins cannot recurse.
func beginEntityWrite(ctx context.Context) (context.Context, error) {
	if entityWriteDepth(ctx) >= 1 {
		return nil, herr(sdk.ErrReentrant, "entity writes from inside an entity hook of a plugin write are not allowed")
	}
	return context.WithValue(ctx, depthKey{}, 1), nil
}

func fnEntitiesCreate(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		SchemaSlug string          `json:"schema_slug"`
		Name       string          `json:"name"`
		Slug       string          `json:"slug"`
		Locale     string          `json:"locale"`
		ParentID   json.RawMessage `json:"parent_id"`
		Attributes json.RawMessage `json:"attributes"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	parent, _, err := optionalID(args.ParentID)
	if err != nil {
		return nil, err
	}
	if args.Name == "" || args.SchemaSlug == "" {
		return nil, herr(sdk.ErrInvalidArgument, "schema_slug and name are required")
	}
	if len(args.Attributes) == 0 {
		args.Attributes = json.RawMessage(`{}`)
	}
	ctx, err = beginEntityWrite(ctx)
	if err != nil {
		return nil, err
	}
	schema, err := a.svc.Entities.GetSchemaBySlug(ctx, args.SchemaSlug)
	if err != nil {
		return nil, err
	}
	ent, err := a.svc.Entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{
		SchemaID: schema.ID, Name: args.Name, Slug: args.Slug, Locale: args.Locale, Attributes: args.Attributes, ParentID: parent,
	})
	if err != nil {
		return nil, err
	}
	ent.Schema = schema
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesUpdate(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		ID         string           `json:"id"`
		Name       *string          `json:"name"`
		Slug       *string          `json:"slug"`
		Locale     *string          `json:"locale"`
		ParentID   json.RawMessage  `json:"parent_id"`
		Attributes *json.RawMessage `json:"attributes"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	parent, parentSet, err := optionalID(args.ParentID)
	if err != nil {
		return nil, err
	}
	if ctx, err = beginEntityWrite(ctx); err != nil {
		return nil, err
	}
	ent, err := a.svc.Entities.UpdateEntity(ctx, id, nil, &entity.UpdateEntityInput{
		Name: args.Name, Slug: args.Slug, Locale: args.Locale, Attributes: args.Attributes,
		ParentID: parent, ClearParent: parentSet && parent == nil,
	})
	if err != nil {
		return nil, err
	}
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesStatus(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ ID, Status string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	st := entity.EntityStatus(args.Status)
	if !entity.ValidStatus(st) {
		return nil, herr(sdk.ErrInvalidArgument, "status %q", args.Status)
	}
	if ctx, err = beginEntityWrite(ctx); err != nil {
		return nil, err
	}
	ent, err := a.svc.Entities.UpdateEntityStatus(ctx, id, nil, st)
	if err != nil {
		return nil, err
	}
	return a.entityDTO(ctx, ent), nil
}

func fnEntitiesDelete(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ ID string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	if ctx, err = beginEntityWrite(ctx); err != nil {
		return nil, err
	}
	return map[string]any{}, a.svc.Entities.DeleteEntity(ctx, id)
}

// --- terms ----------------------------------------------------------------------

func fnTermsList(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Kind, Search string
		Limit        int
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Terms == nil {
		return nil, unavailable("terms")
	}
	res, err := a.svc.Terms.ListTerms(ctx, args.Kind, args.Search, 1, clamp(args.Limit, 100, 500))
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": res.Items, "total": res.Total}, nil
}

func fnTermsGet(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Kind, Slug string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Terms == nil {
		return nil, unavailable("terms")
	}
	return a.svc.Terms.GetTermBySlug(ctx, args.Kind, args.Slug)
}

func fnTermsSave(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Kind, Slug, Name string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Terms == nil {
		return nil, unavailable("terms")
	}
	if args.Kind == "" || args.Name == "" {
		return nil, herr(sdk.ErrInvalidArgument, "kind and name are required")
	}
	if args.Slug != "" {
		if cur, err := a.svc.Terms.GetTermBySlug(ctx, args.Kind, args.Slug); err == nil {
			return a.svc.Terms.UpdateTerm(ctx, taxonomy.UpdateTermInput{ID: cur.ID, Name: args.Name, Slug: args.Slug})
		} else if !errors.Is(err, taxonomy.ErrTermNotFound) {
			return nil, err
		}
	}
	return a.svc.Terms.CreateTerm(ctx, taxonomy.CreateTermInput{Kind: args.Kind, Name: args.Name, Slug: args.Slug})
}

func fnTermsDelete(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Kind, Slug string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Terms == nil {
		return nil, unavailable("terms")
	}
	t, err := a.svc.Terms.GetTermBySlug(ctx, args.Kind, args.Slug)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, a.svc.Terms.DeleteTerm(ctx, t.ID)
}

// --- menus ----------------------------------------------------------------------

func fnMenusList(ctx context.Context, a *API, _ *callEnv, _ json.RawMessage) (any, error) {
	if a.svc.Menus == nil {
		return nil, unavailable("menus")
	}
	items, err := a.svc.Menus.ListMenus(ctx)
	return map[string]any{"items": items}, err
}

func fnMenusGet(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ Slug string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Menus == nil {
		return nil, unavailable("menus")
	}
	m, err := a.svc.Menus.GetMenuBySlug(ctx, args.Slug)
	if err != nil {
		return nil, err
	}
	return a.svc.Menus.GetMenuWithItems(ctx, m.ID)
}

func fnMenusSave(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Slug, Name string
		Location   *string
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Menus == nil {
		return nil, unavailable("menus")
	}
	if args.Slug != "" {
		cur, err := a.svc.Menus.GetMenuBySlug(ctx, args.Slug)
		if err == nil {
			in := &menu.UpdateMenuInput{Location: args.Location}
			if args.Name != "" {
				in.Name = &args.Name
			}
			return a.svc.Menus.UpdateMenu(ctx, cur.ID, in)
		} else if !errors.Is(err, menu.ErrMenuNotFound) {
			return nil, err
		}
	}
	if args.Name == "" {
		return nil, herr(sdk.ErrInvalidArgument, "name is required to create a menu")
	}
	return a.svc.Menus.CreateMenu(ctx, &menu.CreateMenuInput{Name: args.Name, Slug: args.Slug, Location: args.Location})
}

func fnMenusAddItem(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		MenuSlug     string  `json:"menu_slug"`
		ParentID     *string `json:"parent_id"`
		Label        string  `json:"label"`
		LinkType     string  `json:"link_type"`
		EntityID     *string `json:"entity_id"`
		URL          *string `json:"url"`
		OpenInNewTab bool    `json:"open_in_new_tab"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Menus == nil {
		return nil, unavailable("menus")
	}
	m, err := a.svc.Menus.GetMenuBySlug(ctx, args.MenuSlug)
	if err != nil {
		return nil, err
	}
	in := &menu.CreateMenuItemInput{Label: args.Label, LinkType: args.LinkType, URL: args.URL, OpenInNewTab: args.OpenInNewTab}
	if args.ParentID != nil {
		id, err := parseID(*args.ParentID)
		if err != nil {
			return nil, err
		}
		in.ParentID = &id
	}
	if args.EntityID != nil {
		id, err := parseID(*args.EntityID)
		if err != nil {
			return nil, err
		}
		in.EntityID = &id
	}
	return a.svc.Menus.CreateMenuItem(ctx, m.ID, in)
}

func fnMenusDeleteItem(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ ID string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Menus == nil {
		return nil, unavailable("menus")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, a.svc.Menus.DeleteMenuItem(ctx, id)
}

// --- media ----------------------------------------------------------------------

type mediaDTO struct {
	ID        string  `json:"id"`
	Filename  string  `json:"filename"`
	MimeType  string  `json:"mime_type"`
	FileSize  int64   `json:"file_size"`
	PublicURL *string `json:"public_url,omitempty"`
	Width     *int    `json:"width,omitempty"`
	Height    *int    `json:"height,omitempty"`
	AltText   *string `json:"alt_text,omitempty"`
}

func toMediaDTO(m *media.Media) mediaDTO {
	return mediaDTO{ID: m.ID.String(), Filename: m.OriginalFilename, MimeType: m.MimeType, FileSize: m.FileSize,
		PublicURL: m.PublicURL, Width: m.Width, Height: m.Height, AltText: m.AltText}
}

func fnMediaGet(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ ID string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Media == nil {
		return nil, unavailable("media")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.Media.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return toMediaDTO(m), nil
}

func fnMediaList(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Search, Folder string
		MimeType       string `json:"mime_type"`
		Limit, Offset  int
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Media == nil {
		return nil, unavailable("media")
	}
	res, err := a.svc.Media.List(ctx, &media.ListMediaFilter{Search: args.Search, Folder: args.Folder, MimeType: args.MimeType},
		clamp(args.Limit, 50, 200), max(args.Offset, 0))
	if err != nil {
		return nil, err
	}
	items := make([]mediaDTO, 0, len(res.Items))
	for _, m := range res.Items {
		items = append(items, toMediaDTO(m))
	}
	return map[string]any{"items": items, "total": res.Total}, nil
}

func fnMediaUpload(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
		DataB64  string `json:"data_b64"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Media == nil {
		return nil, unavailable("media")
	}
	data, err := base64.StdEncoding.DecodeString(args.DataB64)
	if err != nil || len(data) == 0 || args.Filename == "" || args.MimeType == "" {
		return nil, herr(sdk.ErrInvalidArgument, "filename, mime_type and data_b64 are required")
	}
	m, err := a.svc.Media.Upload(ctx, nil, bytes.NewReader(data), args.Filename, args.MimeType, int64(len(data)))
	if err != nil {
		return nil, err
	}
	return toMediaDTO(m), nil
}

// --- redirects ------------------------------------------------------------------

func fnRedirectsList(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		Search  string
		Page    int
		PerPage int `json:"per_page"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Redirects == nil {
		return nil, unavailable("redirects")
	}
	items, total, err := a.svc.Redirects.ListRedirects(ctx, redirect.RedirectListParams{
		Search: args.Search, Page: clamp(args.Page, 1, 1_000_000), PerPage: clamp(args.PerPage, 50, 200)})
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "total": total}, nil
}

func fnRedirectsCreate(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		SourcePath string `json:"source_path"`
		TargetURL  string `json:"target_url"`
		StatusCode int    `json:"status_code"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Redirects == nil {
		return nil, unavailable("redirects")
	}
	if args.StatusCode == 0 {
		args.StatusCode = 301
	}
	return a.svc.Redirects.CreateRedirect(ctx, &redirect.CreateRedirectInput{
		SourcePath: args.SourcePath, TargetURL: args.TargetURL, StatusCode: args.StatusCode})
}

func fnRedirectsDelete(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct{ ID string }](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Redirects == nil {
		return nil, unavailable("redirects")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{}, a.svc.Redirects.DeleteRedirect(ctx, id)
}

// --- forms ----------------------------------------------------------------------

func fnFormsList(ctx context.Context, a *API, _ *callEnv, _ json.RawMessage) (any, error) {
	if a.svc.Forms == nil {
		return nil, unavailable("forms")
	}
	forms, err := a.svc.Forms.ListForms(ctx, "")
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(forms))
	for _, f := range forms {
		items = append(items, map[string]any{"id": f.ID, "name": f.Name, "slug": f.Slug, "status": f.Status, "fields": f.Fields})
	}
	return map[string]any{"items": items}, nil
}

func fnFormsSubmissions(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		FormSlug string `json:"form_slug"`
		Limit    int
		Offset   int
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Forms == nil {
		return nil, unavailable("forms")
	}
	f, err := a.svc.Forms.GetFormBySlug(ctx, args.FormSlug)
	if err != nil {
		return nil, err
	}
	subs, total, err := a.svc.Forms.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{
		Limit: clamp(args.Limit, 50, 200), Offset: max(args.Offset, 0)})
	if err != nil {
		return nil, err
	}
	// Visitor metadata (IP address, user agent) is never handed to plugins.
	items := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		items = append(items, map[string]any{"id": s.ID, "status": s.Status, "data": s.Data, "created_at": s.CreatedAt})
	}
	return map[string]any{"items": items, "total": total}, nil
}
