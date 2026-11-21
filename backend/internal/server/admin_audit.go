package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/jsonx"
)

// adminAudit writes one hatamoto_admin_audit row (and a log line) for an
// owner action. The write outlives a cancelled request; it is skipped when
// there is no database (route-table builds, tests).
func (s *Server) adminAudit(ctx context.Context, e admin.AuditEvent) {
	raw, _ := jsonx.Marshal(e.Detail)
	if s.db != nil {
		err := auditlog.Recorder{Pool: s.db.Pool()}.Record(ctx, nil, audit.Event{
			ActorID: e.ActorID, Actor: e.Actor, ActorKind: auditlog.KindFor(e.ActorID, e.Actor),
			Action: e.Action, Target: e.Target, Detail: e.Detail,
			IP: e.IP, UserAgent: e.UserAgent, RequestID: e.RequestID,
		})
		if err != nil {
			s.logger.Error("admin audit write failed", "action", e.Action, "error", err)
		}
	}
	s.logger.Info("admin audit", "action", e.Action, "actor", e.Actor, "target", e.Target, "detail", string(raw))
}

// adminAuditLog reads hatamoto_admin_audit back for the Audit log page and the
// Overview Activity card.
type adminAuditLog struct{ pool *pgxpool.Pool }

// Recent lists events newest first and the total number of stored events.
func (l adminAuditLog) Recent(ctx context.Context, limit, offset int) ([]admin.AuditRow, int, error) {
	var total int
	if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit rows: %w", err)
	}
	out, err := l.list(ctx, limit, offset)
	return out, total, err
}

// Latest lists the newest events without counting the table.
func (l adminAuditLog) Latest(ctx context.Context, limit int) ([]admin.AuditRow, error) {
	return l.list(ctx, limit, 0)
}

func (l adminAuditLog) list(ctx context.Context, limit, offset int) ([]admin.AuditRow, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT id, at, coalesce(actor,''), action, coalesce(target,''), coalesce(detail,'{}'::jsonb)
		   FROM hatamoto_admin_audit ORDER BY at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit rows: %w", err)
	}
	defer rows.Close()
	var out []admin.AuditRow
	for rows.Next() {
		var (
			r   admin.AuditRow
			at  time.Time
			raw []byte
		)
		if err := rows.Scan(&r.ID, &at, &r.Actor, &r.Action, &r.Target, &raw); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		r.At = at
		_ = jsonx.Unmarshal(raw, &r.Detail, jsonx.Stored) // a malformed detail shows as empty
		out = append(out, r)
	}
	return out, rows.Err()
}

const (
	auditSearchDefaultLimit = 25
	auditSearchMaxLimit     = 200
	auditSearchMaxRunes     = 100
	auditFacetActors        = 100
	auditFacetActions       = 200
)

// auditSourceCase derives the source of an admin audit row; it mirrors
// auditlog.KindFor for legacy rows whose actor_kind is empty.
const auditSourceCase = `CASE
	WHEN actor_kind = 'user' THEN 'admin'
	WHEN actor_kind = 'api_key' THEN 'api'
	WHEN actor_kind = 'cli' THEN 'cli'
	WHEN actor_kind IN ('system', 'plugin') THEN 'system'
	WHEN actor = 'cli' THEN 'cli'
	WHEN actor = 'api' THEN 'api'
	WHEN actor_id IS NOT NULL THEN 'admin'
	ELSE 'system' END`

// auditMCPTarget picks the argument that names what an agent call touched.
const auditMCPTarget = `coalesce(nullif(args->>'slug',''), nullif(args->>'path',''), nullif(args->>'entity_id',''), nullif(args->>'id',''), nullif(args->>'name',''), '')`

const auditMCPResult = `CASE WHEN ok THEN 'ok' WHEN error LIKE 'forbidden:%' THEN 'denied' ELSE 'error' END`

// auditArgs collects bind arguments and numbers their placeholders.
type auditArgs []any

func (a *auditArgs) ph(v any) string {
	*a = append(*a, v)
	return fmt.Sprintf("$%d", len(*a))
}

// Search reads admin actions and agent calls as one list, newest first. Each
// table is its own branch so it can use its own index; a branch the filters
// exclude is not queried. A page holds Limit rows; Next is set when older
// rows exist. Every filter value is a bind argument.
// auditQueryTimeout bounds one Search or Facets call: a text search cannot use
// an index, so a wide range must fail rather than hold a connection.
var auditQueryTimeout = 10 * time.Second

func (l adminAuditLog) Search(ctx context.Context, q admin.AuditQuery) (admin.AuditPage, error) {
	ctx, cancel := context.WithTimeout(ctx, auditQueryTimeout)
	defer cancel()
	limit := q.Limit
	if limit <= 0 {
		limit = auditSearchDefaultLimit
	}
	limit = min(limit, auditSearchMaxLimit)

	var args auditArgs
	lim := args.ph(limit + 1)
	like := ""
	if text := truncateRunes(strings.TrimSpace(q.Q), auditSearchMaxRunes); text != "" {
		like = args.ph(escapeAuditLike(text))
	}
	var from, to, action, srcPH, atPH, idPH string
	if !q.From.IsZero() {
		from = args.ph(q.From)
	}
	if !q.To.IsZero() {
		to = args.ph(q.To)
	}
	if q.Action != "" {
		action = args.ph(q.Action)
	}
	if c := q.Before; c != nil {
		atPH, srcPH, idPH = args.ph(c.At), args.ph(c.Src), args.ph(c.ID)
	}
	actorKind, actorVal := splitAuditActor(q.Actor)
	var actorPH string
	if actorKind != "" {
		actorPH = args.ph(actorVal)
	}

	// where builds the filters both branches share.
	where := func(src, textExpr string) []string {
		var w []string
		if from != "" {
			w = append(w, "at >= "+from+"::timestamptz")
		}
		if to != "" {
			w = append(w, "at < "+to+"::timestamptz")
		}
		if action != "" {
			col := "action"
			if src == "m" {
				col = "tool"
			}
			w = append(w, col+" = "+action)
		}
		if like != "" {
			w = append(w, textExpr+` ILIKE '%' || `+like+` || '%' ESCAPE '\'`)
		}
		if atPH != "" {
			w = append(w, fmt.Sprintf("at <= %s::timestamptz AND (at, '%s'::text, id) < (%s::timestamptz, %s::text, %s::bigint)", atPH, src, atPH, srcPH, idPH))
		}
		return w
	}

	var branches []string
	if q.Source != "agent" {
		w := where("a", `coalesce(actor,'') || ' ' || action || ' ' || coalesce(target,'') || ' ' || detail::text`)
		switch actorKind {
		case "u":
			w = append(w, "actor_id = "+actorPH+"::uuid")
		case "k":
			w = append(w, "actor_key_id = "+actorPH+"::uuid")
		case "n":
			w = append(w, "actor_id IS NULL AND actor = "+actorPH)
		}
		if q.Source != "" {
			w = append(w, "("+auditSourceCase+") = "+args.ph(q.Source))
		}
		branches = append(branches, fmt.Sprintf(`(SELECT 'a'::text AS src, id, at, coalesce(actor,'') AS actor, action, coalesce(target,'') AS target,
		        detail::text AS detail, (%s) AS source, coalesce(nullif(result,''),'ok') AS result, ''::text AS error
		   FROM hatamoto_admin_audit%s ORDER BY at DESC, id DESC LIMIT %s)`, auditSourceCase, whereClause(w), lim))
	}
	if (q.Source == "" || q.Source == "agent") && (actorKind == "" || actorKind == "k") {
		w := where("m", `api_key_name || ' ' || tool || ' ' || error || ' ' || args::text`)
		if actorKind == "k" {
			w = append(w, "api_key_id = "+actorPH+"::uuid")
		}
		branches = append(branches, fmt.Sprintf(`(SELECT 'm'::text AS src, id, at, api_key_name AS actor, tool AS action, %s AS target,
		        '{}'::text AS detail, 'agent'::text AS source, %s AS result, error
		   FROM hatamoto_mcp_audit%s ORDER BY at DESC, id DESC LIMIT %s)`, auditMCPTarget, auditMCPResult, whereClause(w), lim))
	}
	if len(branches) == 0 {
		return admin.AuditPage{}, nil
	}

	sql := "SELECT * FROM (" + strings.Join(branches, " UNION ALL ") + ") u ORDER BY at DESC, src DESC, id DESC LIMIT " + lim
	rows, err := l.pool.Query(ctx, sql, args...)
	if err != nil {
		return admin.AuditPage{}, fmt.Errorf("search audit rows: %w", err)
	}
	defer rows.Close()
	var out []admin.AuditRow
	for rows.Next() {
		var (
			r   admin.AuditRow
			raw string
		)
		if err := rows.Scan(&r.Src, &r.ID, &r.At, &r.Actor, &r.Action, &r.Target, &raw, &r.Source, &r.Result, &r.Error); err != nil {
			return admin.AuditPage{}, fmt.Errorf("scan audit row: %w", err)
		}
		_ = jsonx.Unmarshal([]byte(raw), &r.Detail, jsonx.Stored) // a malformed detail shows as empty
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return admin.AuditPage{}, fmt.Errorf("search audit rows: %w", err)
	}
	page := admin.AuditPage{Rows: out}
	if len(out) > limit {
		last := out[limit-1]
		page.Rows = out[:limit]
		page.Next = &admin.AuditCursor{At: last.At, Src: last.Src, ID: last.ID}
	}
	return page, nil
}

// Facets lists the actors and actions seen in [from, to) for the filter
// selects. A zero from or to leaves that end open.
func (l adminAuditLog) Facets(ctx context.Context, from, to time.Time) (admin.AuditFacets, error) {
	ctx, cancel := context.WithTimeout(ctx, auditQueryTimeout)
	defer cancel()
	var args auditArgs
	var w []string
	if !from.IsZero() {
		w = append(w, "at >= "+args.ph(from)+"::timestamptz")
	}
	if !to.IsZero() {
		w = append(w, "at < "+args.ph(to)+"::timestamptz")
	}
	win := whereClause(w)
	if win == "" {
		win = " WHERE true"
	}
	var out admin.AuditFacets
	for _, s := range []struct{ prefix, sql string }{
		{"u:", `SELECT actor_id::text, max(actor) FROM hatamoto_admin_audit` + win + ` AND actor_id IS NOT NULL GROUP BY actor_id ORDER BY max(at) DESC LIMIT %d`},
		{"n:", `SELECT actor, actor FROM hatamoto_admin_audit` + win + ` AND actor_id IS NULL AND actor <> '' GROUP BY actor ORDER BY max(at) DESC LIMIT %d`},
		{"k:", `SELECT api_key_id::text, max(api_key_name) FROM hatamoto_mcp_audit` + win + ` AND api_key_id IS NOT NULL GROUP BY api_key_id ORDER BY max(at) DESC LIMIT %d`},
	} {
		rows, err := l.pool.Query(ctx, fmt.Sprintf(s.sql, auditFacetActors), args...)
		if err != nil {
			return admin.AuditFacets{}, fmt.Errorf("audit actor facets: %w", err)
		}
		err = scanAuditActors(rows, s.prefix, &out)
		if err != nil {
			return admin.AuditFacets{}, err
		}
	}
	sort.SliceStable(out.Actors, func(i, j int) bool {
		return strings.ToLower(out.Actors[i].Label) < strings.ToLower(out.Actors[j].Label)
	})

	rows, err := l.pool.Query(ctx, `SELECT a FROM (SELECT action AS a FROM hatamoto_admin_audit`+win+
		` UNION SELECT tool FROM hatamoto_mcp_audit`+win+`) x ORDER BY 1 LIMIT `+fmt.Sprint(auditFacetActions), args...)
	if err != nil {
		return admin.AuditFacets{}, fmt.Errorf("audit action facets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return admin.AuditFacets{}, fmt.Errorf("scan audit action: %w", err)
		}
		out.Actions = append(out.Actions, a)
	}
	return out, rows.Err()
}

func scanAuditActors(rows pgx.Rows, prefix string, out *admin.AuditFacets) error {
	defer rows.Close()
	for rows.Next() {
		var val, label string
		if err := rows.Scan(&val, &label); err != nil {
			return fmt.Errorf("scan audit actor: %w", err)
		}
		if label == "" {
			label = val
		}
		out.Actors = append(out.Actors, admin.AuditActor{Value: prefix + val, Label: label})
	}
	return rows.Err()
}

func whereClause(w []string) string {
	if len(w) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w, " AND ")
}

// splitAuditActor returns the kind ("u", "k", "n") and value of an Actor
// filter, or "" when it is not a usable value.
func splitAuditActor(s string) (kind, val string) {
	kind, val, ok := strings.Cut(s, ":")
	if !ok || val == "" {
		return "", ""
	}
	switch kind {
	case "u", "k":
		if _, err := uuid.Parse(val); err != nil {
			return "", ""
		}
	case "n":
	default:
		return "", ""
	}
	return kind, val
}

// escapeAuditLike makes text match literally in an ILIKE pattern whose escape
// character is a backslash.
func escapeAuditLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
