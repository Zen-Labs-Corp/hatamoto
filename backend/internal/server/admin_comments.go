package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// commentsAdmin maps admin.CommentsService onto internal/comment. Every write
// goes through comment.Service; the few reads the service lacks (one comment,
// the commenter list, the raw entity override, entity titles) are read-only
// SQL here. No query selects ip_hash, net_hash or anything from the
// confirmations table, and the email is only opened for export and erase.
type commentsAdmin struct {
	svc      *comment.Service
	db       *database.DB
	entities *entity.Service
}

var _ admin.CommentsService = commentsAdmin{}

const (
	commentersPageSize = 50
	maxQueuePerPage    = 200
)

var commentLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (a commentsAdmin) Supports(feature string) bool {
	switch feature {
	case "privacy", "blocklists", "entity-options", "schema-mode", "schema-policy":
		return true
	}
	return false // "schema-mode-builtin" waits for a follow-up
}

// --- settings ---------------------------------------------------------------

func fromSettings(s comment.Settings) admin.CommentSettings {
	return admin.CommentSettings{
		Enabled:            s.Enabled,
		KnownMinApproved:   s.Thresholds.KnownApproved,
		KnownMinAgeDays:    s.Thresholds.KnownDays,
		TrustedMinApproved: s.Thresholds.TrustedApproved,
		TrustedMinAgeDays:  s.Thresholds.TrustedDays,
		AutoCloseDays:      s.AutoCloseDays,
		MaxDepth:           s.MaxDepth,
		PageSize:           s.PageSize,
		Sort:               string(s.Sort),
		SlowModeSeconds:    s.SlowModeSeconds,
		Captcha:            s.Captcha,
		CookieDays:         s.DeviceCookieDays,
		PurgeDays:          s.PurgeDays,
		NotifyMode:         string(s.NotifyMode),
	}
}

func toSettings(s admin.CommentSettings) comment.Settings {
	return comment.Settings{
		Enabled: s.Enabled,
		Thresholds: comment.Thresholds{
			KnownApproved:   s.KnownMinApproved,
			KnownDays:       s.KnownMinAgeDays,
			TrustedApproved: s.TrustedMinApproved,
			TrustedDays:     s.TrustedMinAgeDays,
		},
		AutoCloseDays:    s.AutoCloseDays,
		MaxDepth:         s.MaxDepth,
		PageSize:         s.PageSize,
		Sort:             comment.Sort(s.Sort),
		SlowModeSeconds:  s.SlowModeSeconds,
		Captcha:          s.Captcha,
		DeviceCookieDays: s.CookieDays,
		PurgeDays:        s.PurgeDays,
		NotifyMode:       comment.NotifyMode(s.NotifyMode),
	}
}

// adminField renames a store settings field to its admin form field.
func adminField(f string) string {
	switch f {
	case "thresholds.known_approved":
		return "known_min_approved"
	case "thresholds.known_days":
		return "known_min_age_days"
	case "thresholds.trusted_approved":
		return "trusted_min_approved"
	case "thresholds.trusted_days":
		return "trusted_min_age_days"
	case "device_cookie_days":
		return "cookie_days"
	}
	return f
}

// mapCommentErr translates the store's errors into the admin's.
func mapCommentErr(err error) error {
	var fe *comment.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return &admin.CommentFieldError{Field: adminField(fe.Field), Message: fe.Message}
	case errors.Is(err, comment.ErrNotFound):
		return admin.ErrCommentNotFound
	}
	return err
}

// actorPtr parses an admin user id; an unparsable one (a token session) is nil.
func actorPtr(actor string) *uuid.UUID {
	id, err := uuid.Parse(actor)
	if err != nil {
		return nil
	}
	return &id
}

func (a commentsAdmin) Settings(ctx context.Context) (admin.CommentSettings, error) {
	s, err := a.svc.Settings(ctx)
	if err != nil {
		return admin.CommentSettings{}, err
	}
	return fromSettings(s), nil
}

func (a commentsAdmin) SaveSettings(ctx context.Context, s admin.CommentSettings, actor string) error {
	return mapCommentErr(a.svc.SaveSettings(ctx, toSettings(s), actorPtr(actor)))
}

// SetEnabled is a read-modify-write. Settings answers defaults on an error, so
// a failed read must never be saved back.
func (a commentsAdmin) SetEnabled(ctx context.Context, on bool, actor string) (bool, error) {
	set, err := a.svc.Settings(ctx)
	if err != nil {
		return false, err
	}
	was := set.Enabled
	set.Enabled = on
	if err := a.svc.SaveSettings(ctx, set, actorPtr(actor)); err != nil {
		return false, mapCommentErr(err)
	}
	return was, nil
}

// --- queue ------------------------------------------------------------------

func (a commentsAdmin) Counts(ctx context.Context) (admin.CommentCounts, error) {
	m, err := a.svc.StatusCounts(ctx)
	if err != nil {
		return admin.CommentCounts{}, err
	}
	return admin.CommentCounts{
		Pending:  m[comment.StatusPending],
		Approved: m[comment.StatusApproved],
		Spam:     m[comment.StatusSpam],
		Trash:    m[comment.StatusTrash],
		Dead:     m[comment.StatusDead],
	}, nil
}

// toRow maps a stored comment to the admin row. It copies no hash, user agent
// or link domain list.
func toRow(it comment.QueueItem) admin.CommentRow {
	c := it.Comment
	row := admin.CommentRow{
		ID:            c.ID.String(),
		EntityID:      c.EntityID.String(),
		Status:        string(c.Status),
		BodyHTML:      c.BodyHTML,
		BodyRaw:       c.BodyRaw,
		AuthorName:    c.AuthorName,
		Trust:         string(it.Trust),
		DeadBy:        c.DeadBy,
		ApprovedCount: it.ApprovedCount,
		HasLinks:      c.HasLinks,
		Score:         c.SpamScore,
		Reasons:       c.SpamReasons,
		CreatedAt:     c.CreatedAt,
		EditedAt:      c.EditedAt,
	}
	if c.ParentID != nil {
		row.ParentID = c.ParentID.String()
	}
	if c.AuthorUserID != nil {
		row.AuthorUserID = c.AuthorUserID.String()
	}
	if c.CommenterID != nil {
		row.CommenterID = c.CommenterID.String()
	}
	return row
}

func (a commentsAdmin) Queue(ctx context.Context, f admin.CommentFilter) (admin.CommentPage, error) {
	perPage := min(max(f.PerPage, 1), maxQueuePerPage)
	offset := (max(f.Page, 1) - 1) * perPage
	qf := comment.QueueFilter{
		Status: comment.Status(f.Status),
		Trust:  comment.TrustLevel(f.Trust),
		Search: f.Query,
		Limit:  perPage + 1,
		Offset: offset,
	}
	if f.HasLinks {
		t := true
		qf.HasLinks = &t
	}
	var err error
	if qf.EntityID, err = optUUID(f.EntityID); err != nil {
		return admin.CommentPage{}, nil
	}
	if qf.CommenterID, err = optUUID(f.CommenterID); err != nil {
		return admin.CommentPage{}, nil
	}
	items, err := a.svc.Queue(ctx, qf)
	if err != nil {
		return admin.CommentPage{}, err
	}
	page := admin.CommentPage{}
	if len(items) > perPage {
		page.HasMore = true
		items = items[:perPage]
	} else if perPage == maxQueuePerPage && len(items) == perPage {
		// The store caps a page at 200, so the extra row was not fetched.
		more, err := a.svc.Queue(ctx, withWindow(qf, offset+perPage, 1))
		if err != nil {
			return admin.CommentPage{}, err
		}
		page.HasMore = len(more) > 0
	}
	titles, err := a.entityTitles(ctx, items)
	if err != nil {
		return admin.CommentPage{}, err
	}
	page.Rows = make([]admin.CommentRow, len(items))
	for i, it := range items {
		page.Rows[i] = toRow(it)
		page.Rows[i].EntityTitle = titles[it.Comment.EntityID]
	}
	return page, nil
}

func withWindow(f comment.QueueFilter, offset, limit int) comment.QueueFilter {
	f.Offset, f.Limit = offset, limit
	return f
}

// optUUID parses an optional filter id; "" is no filter.
func optUUID(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// entityTitles names the entities of one page of comments (G5).
func (a commentsAdmin) entityTitles(ctx context.Context, items []comment.QueueItem) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	var ids []uuid.UUID
	for _, it := range items {
		if _, seen := out[it.Comment.EntityID]; !seen {
			out[it.Comment.EntityID] = ""
			ids = append(ids, it.Comment.EntityID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := a.db.Pool().Query(ctx, `SELECT id, name FROM hatamoto_entities WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// loadComment reads one comment with its commenter's trust and its locale
// (G1). Hashes and the user agent are not selected.
func (a commentsAdmin) loadComment(ctx context.Context, id string) (admin.CommentRow, string, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return admin.CommentRow{}, "", admin.ErrCommentNotFound
	}
	var it comment.QueueItem
	c := &it.Comment
	var status, trust string
	var reasons []byte
	var title *string
	err = a.db.Pool().QueryRow(ctx, `SELECT c.id, c.entity_id, c.parent_id, c.status, c.body_raw, c.body_html,
			c.has_links, c.commenter_id, c.author_name, c.author_user_id, c.spam_score, c.spam_reasons,
			c.created_at, c.edited_at, c.locale,
			COALESCE(cm.trust_level, 'new'), COALESCE(cm.approved_count, 0), e.name
		FROM hatamoto_comments c
		LEFT JOIN hatamoto_commenters cm ON cm.id = c.commenter_id
		LEFT JOIN hatamoto_entities e ON e.id = c.entity_id
		WHERE c.id = $1`, cid).
		Scan(&c.ID, &c.EntityID, &c.ParentID, &status, &c.BodyRaw, &c.BodyHTML,
			&c.HasLinks, &c.CommenterID, &c.AuthorName, &c.AuthorUserID, &c.SpamScore, &reasons,
			&c.CreatedAt, &c.EditedAt, &c.Locale, &trust, &it.ApprovedCount, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.CommentRow{}, "", admin.ErrCommentNotFound
	}
	if err != nil {
		return admin.CommentRow{}, "", err
	}
	c.Status = comment.Status(status)
	it.Trust = comment.TrustLevel(trust)
	if len(reasons) > 0 {
		_ = jsonx.Unmarshal(reasons, &c.SpamReasons, jsonx.StoredLoose)
	}
	row := toRow(it)
	if title != nil {
		row.EntityTitle = *title
	}
	return row, c.Locale, nil
}

func (a commentsAdmin) Comment(ctx context.Context, id string) (admin.CommentRow, error) {
	row, _, err := a.loadComment(ctx, id)
	return row, err
}

// --- moderation -------------------------------------------------------------

// parseIDs parses the ids of a bulk action; a malformed one fails the call.
func parseIDs(ids []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, admin.ErrCommentNotFound
		}
		out = append(out, id)
	}
	return out, nil
}

// nonDrafts drops the ids of drafts: an approved draft would keep a NULL
// confirmed_at, so a draft is only ever released by its confirmation.
func (a commentsAdmin) nonDrafts(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := a.db.Pool().Query(ctx, `SELECT id FROM hatamoto_comments WHERE id = ANY($1) AND status <> 'draft'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ok := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ok[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := ids[:0:0]
	for _, id := range ids {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// each applies fn to every id and counts the ones that changed. A comment
// that is already gone, or that cannot make the move (another moderator got
// there first), is skipped, so one stale row never stops the rest.
func each(ids []uuid.UUID, fn func(uuid.UUID) error) (int, error) {
	n := 0
	for _, id := range ids {
		err := fn(id)
		if errors.Is(err, comment.ErrNotFound) || errors.Is(err, comment.ErrBadTransition) {
			continue
		}
		if err != nil {
			return n, mapCommentErr(err)
		}
		n++
	}
	return n, nil
}

func (a commentsAdmin) moderate(ctx context.Context, ids []string, fn func(uuid.UUID) error) (int, error) {
	parsed, err := parseIDs(ids)
	if err != nil {
		return 0, err
	}
	if parsed, err = a.nonDrafts(ctx, parsed); err != nil {
		return 0, err
	}
	return each(parsed, fn)
}

func (a commentsAdmin) Approve(ctx context.Context, ids []string, actor string) (int, error) {
	by := actorPtr(actor)
	return a.moderate(ctx, ids, func(id uuid.UUID) error {
		_, err := a.svc.Approve(ctx, id, by)
		return err
	})
}

func (a commentsAdmin) MarkSpam(ctx context.Context, ids []string, teach admin.SpamTeach, actor string) (int, error) {
	by := actorPtr(actor)
	learn := comment.Learn{EmailDomain: teach.EmailDomain, LinkDomains: teach.LinkDomains, Hashes: true}
	return a.moderate(ctx, ids, func(id uuid.UUID) error {
		_, err := a.svc.MarkSpam(ctx, id, by, learn)
		return err
	})
}

func (a commentsAdmin) Trash(ctx context.Context, ids []string, _ string) (int, error) {
	return a.moderate(ctx, ids, func(id uuid.UUID) error {
		_, err := a.svc.Trash(ctx, id)
		return err
	})
}

func (a commentsAdmin) Delete(ctx context.Context, ids []string, _ string) (int, error) {
	parsed, err := parseIDs(ids)
	if err != nil {
		return 0, err
	}
	return each(parsed, func(id uuid.UUID) error { return a.svc.Delete(ctx, id) })
}

// nextTrust is the level one Vouch click moves a commenter to. A blocked
// commenter is refused before this is asked.
func nextTrust(t comment.TrustLevel) comment.TrustLevel {
	switch t {
	case comment.TrustNew:
		return comment.TrustKnown
	}
	return comment.TrustTrusted
}

func (a commentsAdmin) Vouch(ctx context.Context, commenterID, _ string) (string, error) {
	id, err := uuid.Parse(commenterID)
	if err != nil {
		return "", admin.ErrCommentNotFound
	}
	var cur string
	err = a.db.Pool().QueryRow(ctx, `SELECT trust_level FROM hatamoto_commenters WHERE id = $1`, id).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", admin.ErrCommentNotFound
	}
	if err != nil {
		return "", err
	}
	if comment.TrustLevel(cur) == comment.TrustBlocked {
		return "", admin.ErrCommentBlocked
	}
	next := nextTrust(comment.TrustLevel(cur))
	if err := a.svc.Vouch(ctx, id, next); err != nil {
		return "", mapCommentErr(err)
	}
	return string(next), nil
}

// renderBody formats an admin-written body and maps the formatter's errors.
func renderBody(raw string) (commentfmt.Result, error) {
	res, err := commentfmt.Render(raw)
	switch {
	case errors.Is(err, commentfmt.ErrEmpty):
		return res, admin.ErrCommentEmpty
	case errors.Is(err, commentfmt.ErrTooLong):
		return res, admin.ErrCommentTooLong
	}
	return res, err
}

func (a commentsAdmin) Edit(ctx context.Context, id, bodyRaw, _ string) error {
	cid, err := uuid.Parse(id)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	res, err := renderBody(bodyRaw)
	if err != nil {
		return err
	}
	_, err = a.svc.Edit(ctx, cid, comment.Edited{
		BodyRaw: bodyRaw, BodyHTML: res.HTML, HasLinks: res.HasLinks,
		LinkDomains: res.LinkDomains, BodyHash: res.BodyHash, ByAdmin: true,
	})
	return mapCommentErr(err)
}

// replyDraft builds the stored form of a site reply: approved at once, the
// body always the formatter's output.
func replyDraft(entityID, parentID, author uuid.UUID, name, locale string, res commentfmt.Result, bodyRaw string) comment.Draft {
	status, reasons := comment.Decide(comment.DecideInput{Verdict: "allow", AdminAuthor: true})
	return comment.Draft{
		EntityID:     entityID,
		ParentID:     &parentID,
		AuthorUserID: &author,
		AuthorName:   name,
		BodyRaw:      bodyRaw,
		BodyHTML:     res.HTML,
		HasLinks:     res.HasLinks,
		LinkDomains:  res.LinkDomains,
		BodyHash:     res.BodyHash,
		SpamVerdict:  "allow",
		SpamReasons:  reasons,
		Status:       status,
		Locale:       locale,
	}
}

func (a commentsAdmin) Reply(ctx context.Context, parentID, bodyRaw, actor, name string) (string, error) {
	author, err := uuid.Parse(actor)
	if err != nil {
		return "", fmt.Errorf("comments: reply needs a signed-in user: %w", err)
	}
	parent, locale, err := a.loadComment(ctx, parentID)
	if err != nil {
		return "", err
	}
	res, err := renderBody(bodyRaw)
	if err != nil {
		return "", err
	}
	eid, _ := uuid.Parse(parent.EntityID)
	pid, _ := uuid.Parse(parent.ID)
	c, err := a.svc.Store(ctx, replyDraft(eid, pid, author, name, locale, res, bodyRaw))
	if err != nil {
		return "", mapCommentErr(err)
	}
	return c.ID.String(), nil
}

// --- entity and schema modes --------------------------------------------------

func (a commentsAdmin) EntityComments(ctx context.Context, entityID string) (admin.EntityComments, error) {
	id, err := uuid.Parse(entityID)
	if err != nil {
		return admin.EntityComments{}, admin.ErrCommentNotFound
	}
	var override *string
	err = a.db.Pool().QueryRow(ctx, `SELECT mode FROM hatamoto_comment_entities WHERE entity_id = $1`, id).Scan(&override)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return admin.EntityComments{}, err
	}
	resolved, err := a.svc.EntityMode(ctx, id)
	if err != nil {
		return admin.EntityComments{}, mapCommentErr(err)
	}
	counts, err := a.svc.Counts(ctx, []uuid.UUID{id})
	if err != nil {
		return admin.EntityComments{}, err
	}
	out := admin.EntityComments{Resolved: string(resolved)}
	if override != nil {
		out.Override = *override
	}
	c := counts[id]
	out.Total, out.Pending = c.Approved+c.Pending, c.Pending
	return out, nil
}

func (a commentsAdmin) SetEntityMode(ctx context.Context, entityID, mode string) error {
	id, err := uuid.Parse(entityID)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	return mapCommentErr(a.svc.SetEntityMode(ctx, id, comment.Mode(mode)))
}

// schemaSettings decodes a schema's settings object, keeping unknown keys.
func schemaSettings(raw []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(raw) == 0 {
		return m, nil
	}
	if err := jsonx.Unmarshal(raw, &m, jsonx.StoredLoose); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func (a commentsAdmin) SchemaMode(ctx context.Context, schemaID string) (string, error) {
	id, err := uuid.Parse(schemaID)
	if err != nil {
		return "", admin.ErrCommentNotFound
	}
	sch, err := a.entities.GetSchema(ctx, id)
	if errors.Is(err, entity.ErrSchemaNotFound) {
		return "", admin.ErrCommentNotFound
	}
	if err != nil {
		return "", err
	}
	m, err := schemaSettings(sch.Settings)
	if err != nil {
		return "", err
	}
	cm, _ := m["comments"].(map[string]any)
	mode, _ := cm["mode"].(string)
	return mode, nil
}

func (a commentsAdmin) SetSchemaMode(ctx context.Context, schemaID, mode string) error {
	id, err := uuid.Parse(schemaID)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	sch, err := a.entities.GetSchema(ctx, id)
	if errors.Is(err, entity.ErrSchemaNotFound) {
		return admin.ErrCommentNotFound
	}
	if err != nil {
		return err
	}
	if sch.IsBuiltin && !a.Supports("schema-mode-builtin") {
		return admin.ErrCommentsUnsupported
	}
	m, err := schemaSettings(sch.Settings)
	if err != nil {
		return err
	}
	cm, _ := m["comments"].(map[string]any)
	if cm == nil {
		cm = map[string]any{}
	}
	if mode == "" {
		delete(cm, "mode")
	} else {
		cm["mode"] = mode
	}
	if len(cm) == 0 {
		delete(m, "comments")
	} else {
		m["comments"] = cm
	}
	raw, err := jsonx.Marshal(m)
	if err != nil {
		return err
	}
	merged := jsontext.Value(raw)
	if _, err := a.entities.UpdateSchema(ctx, id, &entity.UpdateSchemaInput{Settings: &merged}); err != nil {
		return err
	}
	// UpdateSchema's schema hook already drops every rendered page; this event tells comment observers about the type default.
	a.svc.TypeModeChanged(ctx)
	return nil
}

func (a commentsAdmin) SchemaCommentConfigs(ctx context.Context) ([]admin.SchemaCommentConfig, error) {
	schemas, err := a.entities.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.SchemaCommentConfig, 0, len(schemas))
	for _, sch := range schemas {
		// One schema with unreadable settings has no comment config; it must not hide the others.
		m, err := schemaSettings(sch.Settings)
		if err != nil {
			m = nil
		}
		cm, _ := m["comments"].(map[string]any)
		mode, _ := cm["mode"].(string)
		order, _ := cm["order"].(string)
		if !comment.ValidOrder(order) {
			order = ""
		}
		var rawPolicy []byte
		if pm, ok := cm["policy"]; ok {
			if rawPolicy, err = jsonx.Marshal(pm); err != nil {
				return nil, err
			}
		}
		p := comment.DecodePolicy(rawPolicy)
		out = append(out, admin.SchemaCommentConfig{
			ID: sch.ID.String(), Slug: sch.Slug, Name: sch.Name, Builtin: sch.IsBuiltin,
			Mode: mode, Order: order,
			Policy: admin.CommentPolicy{Enabled: p.Enabled, MaxDepth: p.MaxDepth, EditWindowMinutes: p.EditWindowMinutes, SlowModeSeconds: p.SlowModeSeconds},
		})
	}
	return out, nil
}

func (a commentsAdmin) SetSchemaComments(ctx context.Context, schemaID, order string, p admin.CommentPolicy) error {
	id, err := uuid.Parse(schemaID)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	sch, err := a.entities.GetSchema(ctx, id)
	if errors.Is(err, entity.ErrSchemaNotFound) {
		return admin.ErrCommentNotFound
	}
	if err != nil {
		return err
	}
	if sch.IsBuiltin && !a.Supports("schema-mode-builtin") {
		return admin.ErrCommentsUnsupported
	}
	pol := comment.Policy{Enabled: p.Enabled, MaxDepth: p.MaxDepth, EditWindowMinutes: p.EditWindowMinutes, SlowModeSeconds: p.SlowModeSeconds}
	if err := pol.Validate(); err != nil {
		var fe *comment.FieldError
		if errors.As(err, &fe) {
			return &admin.CommentFieldError{Field: strings.TrimPrefix(fe.Field, "policy."), Message: fe.Message}
		}
		return err
	}
	if !comment.ValidOrder(order) {
		return &admin.CommentFieldError{Field: "order", Message: "must be newest, oldest or the site default"}
	}
	if _, err := schemaSettings(sch.Settings); err != nil {
		return &admin.CommentFieldError{Field: "order", Message: "this content type's settings are not readable, so they cannot be changed here"}
	}
	raw, err := comment.MergeSchemaComments(sch.Settings, &order, &pol)
	if err != nil {
		return err
	}
	merged := jsontext.Value(raw)
	if _, err := a.entities.UpdateSchema(ctx, id, &entity.UpdateSchemaInput{Settings: &merged}); err != nil {
		return err
	}
	a.svc.TypeModeChanged(ctx)
	return nil
}

func (a commentsAdmin) EntityOptions(ctx context.Context, limit int) ([]admin.EntityOption, error) {
	rows, err := a.db.Pool().Query(ctx, `SELECT e.id, e.name FROM hatamoto_entities e
		WHERE EXISTS (SELECT 1 FROM hatamoto_comments c WHERE c.entity_id = e.id AND c.status <> 'draft')
		ORDER BY e.name LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []admin.EntityOption
	for rows.Next() {
		var id uuid.UUID
		var o admin.EntityOption
		if err := rows.Scan(&id, &o.Title); err != nil {
			return nil, err
		}
		o.ID = id.String()
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- commenters -----------------------------------------------------------------

// Commenters lists the visitors who have a visible comment, newest activity
// first (G2). A draft-only commenter is a ghost and is left out. The query
// never touches email_hash or email_sealed.
func (a commentsAdmin) Commenters(ctx context.Context, q string, page int) (admin.CommenterPage, error) {
	pattern := ""
	if q != "" {
		pattern = "%" + commentLikeEscaper.Replace(q) + "%"
	}
	offset := (max(page, 1) - 1) * commentersPageSize
	rows, err := a.db.Pool().Query(ctx, `SELECT cm.id, cm.trust_level, cm.approved_count, cm.last_seen_at, latest.author_name
		FROM hatamoto_commenters cm
		JOIN LATERAL (SELECT author_name FROM hatamoto_comments c
			WHERE c.commenter_id = cm.id AND c.status <> 'draft'
			ORDER BY c.created_at DESC LIMIT 1) latest ON true
		WHERE ($1::text = '' OR latest.author_name ILIKE $1 ESCAPE '\')
		ORDER BY cm.last_seen_at DESC, cm.id
		LIMIT $2 OFFSET $3`, pattern, commentersPageSize+1, offset)
	if err != nil {
		return admin.CommenterPage{}, err
	}
	defer rows.Close()
	var out admin.CommenterPage
	for rows.Next() {
		var id uuid.UUID
		var r admin.CommenterRow
		if err := rows.Scan(&id, &r.Trust, &r.ApprovedCount, &r.LastSeen, &r.Name); err != nil {
			return admin.CommenterPage{}, err
		}
		r.ID = id.String()
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return admin.CommenterPage{}, err
	}
	if len(out.Rows) > commentersPageSize {
		out.HasMore = true
		out.Rows = out.Rows[:commentersPageSize]
	}
	return out, nil
}

func (a commentsAdmin) Block(ctx context.Context, commenterID, _ string) error {
	id, err := uuid.Parse(commenterID)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	return mapCommentErr(a.svc.Block(ctx, id))
}

func (a commentsAdmin) RevokeDevices(ctx context.Context, commenterID string) error {
	id, err := uuid.Parse(commenterID)
	if err != nil {
		return admin.ErrCommentNotFound
	}
	_, err = a.svc.RevokeDevices(ctx, id)
	return mapCommentErr(err)
}

// --- blocklists -------------------------------------------------------------------

var blocklistKinds = []comment.BlockKind{
	comment.BlockEmailDomain, comment.BlockLinkDomain,
	comment.BlockIPHash, comment.BlockNetHash, comment.BlockBodyHash,
}

// listsEntries reports whether a kind shows its values; hashes never leave.
func listsEntries(k comment.BlockKind) bool {
	return k == comment.BlockEmailDomain || k == comment.BlockLinkDomain
}

func (a commentsAdmin) Blocklists(ctx context.Context) ([]admin.Blocklist, error) {
	out := make([]admin.Blocklist, 0, len(blocklistKinds))
	for _, k := range blocklistKinds {
		blocks, err := a.svc.Blocks(ctx, k)
		if err != nil {
			return nil, err
		}
		bl := admin.Blocklist{Kind: string(k), Count: len(blocks)}
		if listsEntries(k) {
			for _, b := range blocks {
				bl.Entries = append(bl.Entries, b.Value)
			}
		}
		out = append(out, bl)
	}
	return out, nil
}

func (a commentsAdmin) SaveBlocklist(ctx context.Context, kind string, entries []string, actor string) error {
	k := comment.BlockKind(kind)
	if !slices.Contains(blocklistKinds, k) {
		return &admin.CommentFieldError{Field: "kind", Message: "unknown blocklist kind"}
	}
	if !listsEntries(k) && len(entries) > 0 {
		return &admin.CommentFieldError{Field: "kind", Message: "this list cannot be replaced"}
	}
	current, err := a.svc.Blocks(ctx, k)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, e := range entries {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			want[e] = true
		}
	}
	have := map[string]bool{}
	for _, b := range current {
		have[b.Value] = true
		if !want[b.Value] {
			if err := a.svc.RemoveBlock(ctx, b.ID); err != nil && !errors.Is(err, comment.ErrNotFound) {
				return mapCommentErr(err)
			}
		}
	}
	by := actorPtr(actor)
	for _, e := range sortedKeys(want) {
		if !have[e] {
			if err := a.svc.AddBlock(ctx, k, e, "", by); err != nil {
				return mapCommentErr(err)
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// --- privacy ------------------------------------------------------------------------

// commenterEmail opens a commenter's sealed address; it is the only place the
// email is read.
func (a commentsAdmin) commenterEmail(ctx context.Context, commenterID string) (string, error) {
	id, err := uuid.Parse(commenterID)
	if err != nil {
		return "", admin.ErrCommentNotFound
	}
	var hash string
	var sealed []byte
	err = a.db.Pool().QueryRow(ctx, `SELECT email_hash, email_sealed FROM hatamoto_commenters WHERE id = $1`, id).Scan(&hash, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", admin.ErrCommentNotFound
	}
	if err != nil {
		return "", err
	}
	email, ok := a.svc.Keys().OpenEmail(sealed, hash)
	if !ok {
		return "", errors.New("email unreadable (server secret changed)")
	}
	return email, nil
}

type commenterExport struct {
	Email         string          `json:"email"`
	Trust         string          `json:"trust"`
	ApprovedCount int             `json:"approved_count"`
	CreatedAt     time.Time       `json:"created_at"`
	LastSeenAt    time.Time       `json:"last_seen_at"`
	Comments      []exportComment `json:"comments"`
}

// exportComment is one comment as stored. body_html, body_hash, has_links,
// link_domains, path and depth are left out: they derive from body_raw.
type exportComment struct {
	ID            string     `json:"id"`
	EntityID      string     `json:"entity_id"`
	ParentID      *string    `json:"parent_id"`
	Status        string     `json:"status"`
	AuthorName    string     `json:"author_name"`
	BodyRaw       string     `json:"body_raw"`
	IPHash        string     `json:"ip_hash"`
	NetHash       string     `json:"net_hash"`
	UserAgent     string     `json:"user_agent"`
	NotifyReplies bool       `json:"notify_replies"`
	SpamScore     int        `json:"spam_score"`
	SpamVerdict   string     `json:"spam_verdict"`
	SpamReasons   []string   `json:"spam_reasons"`
	Locale        string     `json:"locale"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ConfirmedAt   *time.Time `json:"confirmed_at"`
	ApprovedAt    *time.Time `json:"approved_at"`
	EditedAt      *time.Time `json:"edited_at"`
}

// utcPtr is t in UTC, nil when unset.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func (a commentsAdmin) ExportCommenter(ctx context.Context, commenterID string) ([]byte, error) {
	email, err := a.commenterEmail(ctx, commenterID)
	if err != nil {
		return nil, err
	}
	ex, err := a.svc.ExportByEmail(ctx, email)
	if err != nil {
		return nil, mapCommentErr(err)
	}
	out := commenterExport{
		Email: email, Trust: string(ex.Commenter.Trust), ApprovedCount: ex.Commenter.ApprovedCount,
		CreatedAt: ex.Commenter.CreatedAt.UTC(), LastSeenAt: ex.Commenter.LastSeenAt.UTC(),
		Comments: make([]exportComment, 0, len(ex.Comments)),
	}
	for _, c := range ex.Comments {
		var parent *string
		if c.ParentID != nil {
			p := c.ParentID.String()
			parent = &p
		}
		reasons := c.SpamReasons
		if reasons == nil {
			reasons = []string{}
		}
		out.Comments = append(out.Comments, exportComment{
			ID: c.ID.String(), EntityID: c.EntityID.String(), ParentID: parent, Status: string(c.Status),
			AuthorName: c.AuthorName, BodyRaw: c.BodyRaw, IPHash: c.IPHash, NetHash: c.NetHash,
			UserAgent: c.UserAgent, NotifyReplies: c.NotifyReplies, SpamScore: c.SpamScore,
			SpamVerdict: c.SpamVerdict, SpamReasons: reasons, Locale: c.Locale,
			CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(), ConfirmedAt: utcPtr(c.ConfirmedAt),
			ApprovedAt: utcPtr(c.ApprovedAt), EditedAt: utcPtr(c.EditedAt),
		})
	}
	return jsonx.Indent(out)
}

func (a commentsAdmin) EraseCommenter(ctx context.Context, commenterID string) (int, int, error) {
	email, err := a.commenterEmail(ctx, commenterID)
	if err != nil {
		return 0, 0, err
	}
	res, err := a.svc.EraseByEmail(ctx, email)
	if err != nil {
		return 0, 0, mapCommentErr(err)
	}
	return res.Deleted, res.Anonymised, nil
}
