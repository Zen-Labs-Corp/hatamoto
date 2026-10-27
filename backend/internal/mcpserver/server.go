// Package mcpserver is the Model Context Protocol endpoint (/mcp): the tools
// an AI agent uses to draft, publish and inspect a site. Every tool calls the
// same internal services as the HTTP API (no HTTP self-calls), is gated by the
// calling API key's scopes, and is audit-logged.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

// KeyService validates API keys (auth.APIKeyService).
type KeyService interface {
	ValidateKey(ctx context.Context, plainKey string) (*hmauth.APIKey, error)
	RecordUsage(ctx context.Context, keyID uuid.UUID, ipAddress string) error
}

// Limiter is the per-key rate limiter (middleware.APIKeyRateLimiter).
type Limiter interface {
	Allow(keyID uuid.UUID, rateLimit int) bool
}

// Response is an in-process answer of the public site.
type Response struct {
	Status int
	Body   []byte
}

// Deps are the services the tools call. Every field but Limiter is required.
type Deps struct {
	Keys           KeyService
	Limiter        Limiter
	Entities       *entity.Service
	PublicEntities *entity.PostgresPublicRepository
	Media          *media.Service
	Menus          *menu.Service
	Taxonomy       *taxonomy.Service
	Redirects      *redirect.Service
	Crawlers       *agents.StatsRepository
	Referrals      *analytics.Reader
	Audit          Auditor
	Logger         *slog.Logger

	// ClientIP is the caller's address as the API sees it (trusted-proxy
	// aware). Defaults to the connection's remote address.
	ClientIP func(*http.Request) string
	// PublicOrigin is the site's public origin (scheme://host from
	// PUBLIC_BASE_URL); a browser request carrying an Origin header must
	// match it or one of AllowedOrigins. Requests without Origin (non-browser
	// clients) are always allowed.
	PublicOrigin   string
	AllowedOrigins []string

	// Fetch serves a public GET path in process (sitemaps, llms.txt, pages).
	Fetch func(ctx context.Context, path string) (Response, error)
	// PageURL is the public path of an entity.
	PageURL func(ctx context.Context, e *entity.Entity) string
	// Changed drops cached pages and sitemaps after a write the entity
	// events do not cover (menus, redirects, terms).
	Changed func(ctx context.Context)

	// The builder tools. BuilderEntities defaults to Entities; the others
	// are optional and the tools that need one say so when it is nil.
	BuilderEntities entity.BuilderStore
	// Design resolves the site's design tokens and presets (builder_design).
	Design func(ctx context.Context) (DesignInfo, error)
	// Templates is the section template store (builder_templates_list).
	Templates TemplateStore
	// RenderPage renders a document in the page's context (builder_render).
	RenderPage func(ctx context.Context, req RenderRequest) (RenderResult, error)
}

// Server holds the registered tools.
type Server struct {
	d   Deps
	mcp *mcp.Server
}

// New builds the MCP server and returns its HTTP handler (streamable HTTP;
// every request must carry an API key as "Authorization: Bearer <key>").
func New(d Deps, version string) http.Handler {
	s := &Server{d: d, mcp: mcp.NewServer(&mcp.Implementation{Name: "hatamoto", Version: version}, &mcp.ServerOptions{
		Instructions: "Hatamoto CMS. Create drafts with entities_create, review them, then entities_publish. " +
			"Read schemas_get first to learn which attributes a content type takes.",
	})}
	s.registerContent()
	s.registerInsight()
	s.registerBuilder()

	// A base64 upload of the largest allowed file (plus JSON framing) must fit.
	// The SDK's DNS-rebinding guard rejects a non-loopback Host on a loopback
	// connection, which is exactly what a reverse proxy on the same machine
	// (Caddy, nginx) produces. Every request needs a secret bearer key that a
	// rebinding page cannot supply, and Origin is checked below instead.
	opts := &mcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
		SessionTimeout:             30 * time.Minute,
	}
	if d.Media != nil {
		opts.MaxRequestBodyBytes = int64(d.Media.Config().MaxSizeMB)*1024*1024/3*4 + 1<<20
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, opts)
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key, ok := keyFromContext(r.Context()); ok && s.d.Limiter != nil && !s.d.Limiter.Allow(key.ID, key.RateLimit) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		h.ServeHTTP(w, r)
	})
	return s.checkOrigin(auth.RequireBearerToken(s.verify, nil)(limited))
}

func keyFromContext(ctx context.Context) (*hmauth.APIKey, bool) {
	if info := auth.TokenInfoFromContext(ctx); info != nil {
		k, ok := info.Extra["key"].(*hmauth.APIKey)
		return k, ok
	}
	return nil, false
}

// checkOrigin enforces the MCP spec's Origin rule: a request that names an
// Origin (a browser) must come from the site itself or an allow-listed origin.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range append([]string{s.d.PublicOrigin}, s.d.AllowedOrigins...) {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allowed[strings.ToLower(o)] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !allowed[strings.ToLower(strings.TrimRight(o, "/"))] {
			http.Error(w, "Forbidden: origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// verify turns a bearer token into the calling API key. Revoked, expired and
// orphaned keys fail as in the API's key middleware.
func (s *Server) verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	key, err := s.d.Keys.ValidateKey(ctx, token)
	if err != nil || key == nil || key.CreatedBy == nil || !key.CreatorCanAct() {
		return nil, fmt.Errorf("%w: invalid api key", auth.ErrInvalidToken)
	}
	exp := time.Now().Add(time.Hour)
	if key.ExpiresAt != nil && key.ExpiresAt.Before(exp) {
		exp = *key.ExpiresAt
	}
	scopes := make([]string, len(key.Scopes))
	for i, sc := range key.Scopes {
		scopes[i] = string(sc)
	}
	ip := r.RemoteAddr
	if s.d.ClientIP != nil {
		ip = s.d.ClientIP(r)
	}
	go func(id uuid.UUID, ip string) {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.d.Keys.RecordUsage(rctx, id, ip)
	}(key.ID, ip)
	return &auth.TokenInfo{Scopes: scopes, Expiration: exp, UserID: key.ID.String(), Extra: map[string]any{"key": key}}, nil
}

func keyOf(req *mcp.CallToolRequest) (*hmauth.APIKey, error) {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		if k, ok := req.Extra.TokenInfo.Extra["key"].(*hmauth.APIKey); ok {
			return k, nil
		}
	}
	return nil, errors.New("unauthenticated")
}

// toolSpec describes one tool and the scope it needs. Read tools carry a read
// scope, so a read-only key can never reach a write tool.
type toolSpec struct {
	name, description string
	scope             hmauth.APIKeyScope
	write, idempotent bool
	// destructive marks writes that replace or remove existing content; MCP
	// clients use it to ask the user first.
	destructive bool
}

// register adds a tool: scope check, rate limit, audit and error mapping wrap
// h, which returns the JSON-encodable result.
func register[In any](s *Server, spec toolSpec, h func(ctx context.Context, key *hmauth.APIKey, in In) (any, error)) {
	destructive := spec.destructive
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        spec.name,
		Description: spec.description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !spec.write, IdempotentHint: spec.idempotent, DestructiveHint: &destructive},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		key, err := keyOf(req)
		if err != nil {
			return nil, nil, err
		}
		var out any
		switch {
		case !key.HasScope(spec.scope):
			err = userErr("forbidden: the API key lacks the %s scope", spec.scope)
		default:
			out, err = h(ctx, key, in)
		}
		s.d.Audit.Record(ctx, AuditEntry{KeyID: key.ID, KeyName: key.Name, Tool: spec.name, Args: auditArgs(in), Err: err})
		if err != nil {
			return nil, nil, publicError(s.d.Logger, spec.name, err)
		}
		body, merr := json.Marshal(out)
		if merr != nil {
			return nil, nil, errors.New("internal error")
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil, nil
	})
}

// userError is an error whose text is safe to show the agent.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func userErr(format string, a ...any) error { return userError{fmt.Sprintf(format, a...)} }

// publicError maps a service error to what the agent sees: known domain
// errors keep their text, anything else is logged and hidden.
func publicError(l *slog.Logger, tool string, err error) error {
	var ue userError
	switch {
	case errors.As(err, &ue):
		return ue
	case errors.Is(err, entity.ErrSchemaNotFound), errors.Is(err, entity.ErrEntityNotFound),
		errors.Is(err, entity.ErrSlugAlreadyExists), errors.Is(err, entity.ErrValidationFailed),
		errors.Is(err, entity.ErrParentNotFound), errors.Is(err, entity.ErrParentCycle), errors.Is(err, entity.ErrParentTooDeep),
		errors.Is(err, entity.ErrAuthorNotFound), errors.Is(err, entity.ErrHasChildren),
		errors.Is(err, entity.ErrRejectedByPlugin), errors.Is(err, entity.ErrInvalidStatus),
		errors.Is(err, entity.ErrInvalidDoc), errors.Is(err, entity.ErrPageSectionsRemoved),
		errors.Is(err, menu.ErrMenuNotFound), errors.Is(err, menu.ErrEntityMissing), errors.Is(err, menu.ErrInvalidLocation), errors.Is(err, menu.ErrInvalidLinkType),
		errors.Is(err, redirect.ErrRedirectNotFound), errors.Is(err, redirect.ErrDuplicateSourcePath),
		errors.Is(err, redirect.ErrInvalidStatusCode), errors.Is(err, redirect.ErrInvalidPattern), errors.Is(err, redirect.ErrUnsafePattern),
		errors.Is(err, media.ErrFileTooLarge), errors.Is(err, media.ErrImageTooLarge), errors.Is(err, media.ErrInvalidMimeType),
		errors.Is(err, taxonomy.ErrTermSlugConflict):
		return err
	}
	if l != nil {
		l.Error("mcp tool failed", "tool", tool, "error", err)
	}
	return errors.New("internal error")
}

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, userErr("%s must be a UUID", field)
	}
	return id, nil
}
