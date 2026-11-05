package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/time/rate"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// Mailer is the slice of the email service mail.send uses.
type Mailer interface {
	IsConfigured() bool
	SendPlugin(ctx context.Context, plugin string, to []string, subject, htmlBody, textBody string) error
}

// EntityPaths gives the entities.* host functions the public site's view of
// an entity: where it lives and which entity lives at a path. The server
// implements it over the URL registry and the public resolver, so a plugin
// sees exactly the URLs the site serves.
type EntityPaths interface {
	// Path is the canonical public path of ent ("/a/b").
	Path(ctx context.Context, ent *entity.Entity) string
	// Resolve returns the published entity served at path, or
	// entity.ErrEntityNotFound. With localePrefix a leading locale segment
	// ("/xx/about") is read as that locale; without it the path is read as
	// the site language only.
	Resolve(ctx context.Context, path string, localePrefix bool) (*entity.Entity, error)
	// BaseURL is the configured public origin ("" when none is set).
	BaseURL(ctx context.Context) string
}

// TranslationGroups is the slice of the translation group store the
// entities.* host functions use; *entity.GroupStore satisfies it.
type TranslationGroups interface {
	Link(ctx context.Context, ids ...uuid.UUID) (uuid.UUID, error)
	GroupOf(ctx context.Context, id uuid.UUID) (*uuid.UUID, error)
	GroupsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]entity.GroupMember, error)
	Members(ctx context.Context, id uuid.UUID) ([]entity.Member, error)
}

// Services are the core services host functions call. A nil service makes its
// functions fail with "unavailable".
type Services struct {
	Entities *entity.Service
	Paths    EntityPaths // optional: without it entities carry no path/url and cannot be read by path
	// Groups links translations. Without it translation_of is unavailable and
	// entities carry no translation_group.
	Groups    TranslationGroups
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

	// Run services. A nil one makes the functions that need it unavailable.
	Changesets *changeset.Engine
	Runs       *plugrun.Store
	Uploads    UploadReader
	Site       SiteSource
	// SlugTaken reports whether a published, public entity holds the slug.
	SlugTaken func(ctx context.Context, schema, slug, locale string) (bool, error)
	// PublicEntityIDs and PublicMediaStems say which entities and media files
	// an anonymous visitor can see; the fetch phase of a run reads only those
	// (nil: those reads are unavailable there).
	PublicEntityIDs  func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	PublicMediaStems func(ctx context.Context, stems []string) (map[string]bool, error)
	// LivePath reports whether path is served by live content now; a staged
	// redirect may not start there.
	LivePath func(ctx context.Context, path string) bool
	Blobs    StagedBlobs
}

// DefaultFetchPerMinute is the outbound request cap per plugin.
const DefaultFetchPerMinute = 600

// hostCallTimeout bounds one host function call (the hook call's own deadline
// applies as well).
const hostCallTimeout = 15 * time.Second

// maxHostResultBytes bounds what one host function returns to a guest.
const maxHostResultBytes = 2 << 20

// maxRequestBytes bounds one host call's request. The wasm host enforces the
// same limit; it is repeated here so a call that does not come through it is
// bounded too.
const maxRequestBytes = 1 << 20

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
	case errors.Is(err, changeset.ErrNotOwned):
		return herr(sdk.ErrNotOwned, "object belongs to the owner")
	case errors.Is(err, changeset.ErrNotStaged), errors.Is(err, changeset.ErrExpired):
		return herr(sdk.ErrUnavailable, "%s", err.Error())
	case errors.Is(err, changeset.ErrSingleton), errors.Is(err, changeset.ErrInvalidItem), errors.Is(err, changeset.ErrUnknownKind),
		errors.Is(err, changeset.ErrUnsupportedOp), errors.Is(err, changeset.ErrNoWriter), errors.Is(err, changeset.ErrQuotaExceeded):
		return herr(sdk.ErrInvalidArgument, "%s", err.Error())
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
		form.ErrSubmissionNotFound, changeset.ErrNotFound,
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
		taxonomy.ErrTermSlugConflict, menu.ErrSlugAlreadyExists, menu.ErrInvalidLinkType, menu.ErrInvalidLocation, menu.ErrInvalidAttrs,
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
	if len(in) > maxRequestBytes {
		return nil, herr(sdk.ErrInvalidArgument, "request too large (over %d bytes)", maxRequestBytes)
	}
	var req sdk.CallRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, herr(sdk.ErrInvalidArgument, "request is not a JSON {fn, args}")
	}
	// Run and changeset functions are new in 0.17 and bounded from the start;
	// the functions that predate them keep their old, looser acceptance.
	if runOnly[req.Fn] {
		if err := defaultShape.check(req.Args); err != nil {
			return nil, herr(sdk.ErrInvalidArgument, "arguments: %v", err)
		}
	}
	if repl, gone := removedFns[req.Fn]; gone {
		return nil, herr(sdk.ErrUnavailable, "%s was removed in SDK 0.17; use %s inside a run", req.Fn, repl)
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
	if err := phaseGate(ctx, req.Fn); err != nil {
		return nil, err
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
	"permissions":              {run: fnPermissions},
	sdk.FnLog:                  {perm: sdk.PermLog, run: fnLog},
	sdk.FnSettingsGet:          {perm: sdk.PermSettings, run: fnSettingsGet},
	sdk.FnMailSend:             {perm: sdk.PermMailSend, run: fnMailSend},
	sdk.FnHTTPFetch:            {perm: sdk.PermHTTPFetch, run: fnHTTPFetch},
	sdk.FnKVGet:                {perm: sdk.PermKV, run: fnKVGet},
	sdk.FnKVSet:                {perm: sdk.PermKV, run: fnKVSet},
	sdk.FnKVDelete:             {perm: sdk.PermKV, run: fnKVDelete},
	sdk.FnKVList:               {perm: sdk.PermKV, run: fnKVList},
	sdk.FnDBQuery:              {perm: sdk.PermDB, run: fnDBQuery},
	sdk.FnDBExec:               {perm: sdk.PermDB, run: fnDBExec},
	sdk.FnEntitiesGet:          {perm: sdk.PermEntitiesRead, run: fnEntitiesGet},
	sdk.FnEntitiesList:         {perm: sdk.PermEntitiesRead, run: fnEntitiesList},
	sdk.FnEntitiesTranslations: {perm: sdk.PermEntitiesRead, run: fnEntitiesTranslations},
	sdk.FnTermsList:            {perm: sdk.PermTermsRead, run: fnTermsList},
	sdk.FnTermsGet:             {perm: sdk.PermTermsRead, run: fnTermsGet},
	sdk.FnMenusList:            {perm: sdk.PermMenusRead, run: fnMenusList},
	sdk.FnMenusGet:             {perm: sdk.PermMenusRead, run: fnMenusGet},
	sdk.FnMediaGet:             {perm: sdk.PermMediaRead, run: fnMediaGet},
	sdk.FnMediaList:            {perm: sdk.PermMediaRead, run: fnMediaList},
	sdk.FnRedirectsList:        {perm: sdk.PermRedirectsRead, run: fnRedirectsList},
	sdk.FnFormsList:            {perm: sdk.PermFormsRead, run: fnFormsList},
	sdk.FnFormsSubmissions:     {perm: sdk.PermFormsRead, run: fnFormsSubmissions},
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
	if err := a.svc.Mail.SendPlugin(ctx, e.plugin, args.To, args.Subject, args.HTML, args.Text); err != nil {
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
	hosts := effectiveHosts(ctx, e.manifest.AllowedHosts)
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
	// TranslationGroup is the id shared by the entity's translations, absent
	// when it has none (or the host has no translation groups).
	TranslationGroup string `json:"translation_group,omitempty"`
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

// entityDTOGrouped is entityDTO plus the translation group. A missing field
// always means "ungrouped", so a failed lookup is an error, never a silent
// omission. For create, update and status the write is already committed when
// the lookup runs; the plugin sees internal and can read the entity back.
func (a *API) entityDTOGrouped(ctx context.Context, e *entity.Entity) (entityDTO, error) {
	dto := a.entityDTO(ctx, e)
	if a.svc.Groups == nil {
		return dto, nil
	}
	g, err := a.svc.Groups.GroupOf(ctx, e.ID)
	if err != nil {
		a.log.Warn("translation group lookup failed", slog.String("entity_id", e.ID.String()), slog.Any("error", err))
		return entityDTO{}, herr(sdk.ErrInternal, "translation group lookup failed")
	}
	if g != nil {
		dto.TranslationGroup = g.String()
	}
	return dto, nil
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

func fnEntitiesGet(ctx context.Context, a *API, env *callEnv, raw json.RawMessage) (any, error) {
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
		if ent, err = a.svc.Paths.Resolve(ctx, args.Path, requiresLocale(env)); err != nil {
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
	if runSeesOnlyPublished(ctx, ent.Status) {
		return nil, herr(sdk.ErrNotFound, "entity not found")
	}
	if inFetchPhase(ctx) {
		vis, err := a.publicEntities(ctx, []uuid.UUID{ent.ID})
		if err != nil {
			return nil, err
		}
		if !vis[ent.ID] {
			return nil, herr(sdk.ErrNotFound, "entity not found")
		}
	}
	return a.entityDTOGrouped(ctx, ent)
}

// runSeesOnlyPublished reports whether a read of an entity with this status
// must be refused: inside a run the transform phase reads published content
// only (ADR 0004 s2), so drafts, scheduled and archived entities cannot be
// copied into staged content or out through a later phase.
func runSeesOnlyPublished(ctx context.Context, status entity.EntityStatus) bool {
	_, inRun := RunFrom(ctx)
	return inRun && status != entity.StatusPublished
}

// inFetchPhase reports whether ctx is the fetch phase of a run, the phase that
// can reach the network: its reads see only what an anonymous visitor sees.
func inFetchPhase(ctx context.Context) bool {
	rc, ok := RunFrom(ctx)
	return ok && rc.Phase == sdk.RunPhaseFetch
}

// publicEntities returns which of ids the public site shows.
func (a *API) publicEntities(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	if a.svc.PublicEntityIDs == nil {
		return nil, unavailable("public content")
	}
	return a.svc.PublicEntityIDs(ctx, ids)
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
	if _, inRun := RunFrom(ctx); inRun {
		if args.Status != "" && args.Status != string(entity.StatusPublished) {
			return nil, herr(sdk.ErrInvalidArgument, "a run reads published entities only")
		}
		args.Status = string(entity.StatusPublished)
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
	if inFetchPhase(ctx) && len(res.Items) > 0 {
		ids := make([]uuid.UUID, len(res.Items))
		for i, it := range res.Items {
			ids[i] = it.ID
		}
		vis, err := a.publicEntities(ctx, ids)
		if err != nil {
			return nil, err
		}
		res.Items = slices.DeleteFunc(res.Items, func(e *entity.Entity) bool { return !vis[e.ID] })
	}
	var groups map[uuid.UUID]entity.GroupMember
	if a.svc.Groups != nil && len(res.Items) > 0 {
		ids := make([]uuid.UUID, 0, len(res.Items))
		for _, it := range res.Items {
			ids = append(ids, it.ID)
		}
		if groups, err = a.svc.Groups.GroupsFor(ctx, ids); err != nil {
			a.log.Warn("translation group lookup failed", slog.Any("error", err))
			return nil, herr(sdk.ErrInternal, "translation group lookup failed")
		}
	}
	items := make([]entityDTO, 0, len(res.Items))
	for _, it := range res.Items {
		dto := a.entityDTO(ctx, it)
		if g, ok := groups[it.ID]; ok {
			dto.TranslationGroup = g.Group.String()
		}
		items = append(items, dto)
	}
	return map[string]any{"items": items, "next_cursor": res.NextCursor}, nil
}

// localeRequiredSince is the first SDK version whose plugins must name the
// locale of the entities they create.
const localeRequiredSince = "0.15.0"

// requiresLocale reports whether the calling plugin was built for SDK 0.15 or
// later only. Older plugins keep the site language as the default.
func requiresLocale(env *callEnv) bool {
	if env == nil {
		return false
	}
	return rangeRequiresLocale(env.manifest.SDK, append(slices.Clone(sdk.CompatibleWith), sdk.Version))
}

// rangeRequiresLocale reports whether rng admits some version in compatible
// at or after localeRequiredSince and none before it.
func rangeRequiresLocale(rng string, compatible []string) bool {
	admitsNew := false
	for _, v := range compatible {
		ok, err := sdk.Satisfies(v, rng)
		if err != nil {
			return false
		}
		if !ok {
			continue
		}
		if older, _ := sdk.Satisfies(v, "<"+localeRequiredSince); older {
			return false
		}
		admitsNew = true
	}
	return admitsNew
}

type translationDTO struct {
	ID     string `json:"id"`
	Locale string `json:"locale"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
}

// fnEntitiesTranslations lists the members of an entity's translation group
// (the entity itself included), ordered by locale. It reads each member once
// to place it, so a group costs one query per member.
func fnEntitiesTranslations(ctx context.Context, a *API, _ *callEnv, raw json.RawMessage) (any, error) {
	args, err := decodeArgs[struct {
		ID string `json:"id"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Entities == nil {
		return nil, unavailable("entities")
	}
	if a.svc.Groups == nil {
		return nil, unavailable("translations")
	}
	id, err := parseID(args.ID)
	if err != nil {
		return nil, err
	}
	members, err := a.svc.Groups.Members(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []translationDTO{}
	if _, inRun := RunFrom(ctx); inRun { // published members only, and only for a published entity
		members = slices.DeleteFunc(members, func(m entity.Member) bool { return m.Status != string(entity.StatusPublished) })
		if inFetchPhase(ctx) && len(members) > 0 {
			ids := make([]uuid.UUID, len(members))
			for i, m := range members {
				ids[i] = m.ID
			}
			vis, err := a.publicEntities(ctx, ids)
			if err != nil {
				return nil, err
			}
			members = slices.DeleteFunc(members, func(m entity.Member) bool { return !vis[m.ID] })
		}
		if !slices.ContainsFunc(members, func(m entity.Member) bool { return m.ID == id }) {
			return nil, herr(sdk.ErrNotFound, "entity not found")
		}
	}
	if len(members) < 2 {
		return out, nil
	}
	for _, m := range members {
		dto := translationDTO{ID: m.ID.String(), Locale: m.Locale, Status: m.Status}
		if a.svc.Paths != nil {
			ent, err := a.svc.Entities.GetEntity(ctx, m.ID)
			if err != nil {
				return nil, err
			}
			if ent.Schema == nil {
				if ent.Schema, err = a.svc.Entities.GetSchema(ctx, ent.SchemaID); err != nil {
					return nil, err
				}
			}
			dto.Path = a.svc.Paths.Path(ctx, ent)
		}
		out = append(out, dto)
	}
	slices.SortStableFunc(out, func(x, y translationDTO) int { return strings.Compare(x.Locale, y.Locale) })
	return out, nil
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
	Caption   *string `json:"caption,omitempty"`
	Credit    *string `json:"credit,omitempty"`
}

func toMediaDTO(m *media.Media) mediaDTO {
	return mediaDTO{ID: m.ID.String(), Filename: m.OriginalFilename, MimeType: m.MimeType, FileSize: m.FileSize,
		PublicURL: m.PublicURL, Width: m.Width, Height: m.Height, AltText: m.AltText, Caption: m.Caption, Credit: m.Credit}
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
	if inFetchPhase(ctx) {
		vis, err := a.publicMedia(ctx, []*media.Media{m})
		if err != nil {
			return nil, err
		}
		if len(vis) == 0 {
			return nil, herr(sdk.ErrNotFound, "media not found")
		}
	}
	return toMediaDTO(m), nil
}

// publicMedia keeps the media files a public page references.
func (a *API) publicMedia(ctx context.Context, items []*media.Media) ([]*media.Media, error) {
	if a.svc.PublicMediaStems == nil {
		return nil, unavailable("public media")
	}
	stems := make([]string, 0, len(items))
	for _, m := range items {
		if s := strings.TrimSuffix(m.Filename, path.Ext(m.Filename)); s != "" {
			stems = append(stems, s)
		}
	}
	vis, err := a.svc.PublicMediaStems(ctx, stems)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(items, func(m *media.Media) bool { return !vis[strings.TrimSuffix(m.Filename, path.Ext(m.Filename))] }), nil
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
	if args.Folder != "" {
		if _, err := media.NormalizeFolder(args.Folder); err != nil {
			return nil, herr(sdk.ErrInvalidArgument, "%v", err)
		}
	}
	if a.svc.Media == nil {
		return nil, unavailable("media")
	}
	res, err := a.svc.Media.List(ctx, &media.ListMediaFilter{Search: args.Search, Folder: args.Folder, MimeType: args.MimeType},
		clamp(args.Limit, 50, 200), max(args.Offset, 0))
	if err != nil {
		return nil, err
	}
	if inFetchPhase(ctx) {
		if res.Items, err = a.publicMedia(ctx, res.Items); err != nil {
			return nil, err
		}
	}
	items := make([]mediaDTO, 0, len(res.Items))
	for _, m := range res.Items {
		items = append(items, toMediaDTO(m))
	}
	return map[string]any{"items": items, "total": res.Total}, nil
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
