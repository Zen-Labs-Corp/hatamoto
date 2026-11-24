package comment

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

// Service is the comment store: writes, reads, commenters and the blocklist.
// It holds no spam logic and no HTTP code; callers pass a formatted, scored draft.
type Service struct {
	db       *database.DB
	keys     Keys
	settings *SettingsStore
	obs      observers
	now      func() time.Time
}

// New returns a Service on db. serverSecret keys the email and network hashes
// and the sealed emails.
func New(db *database.DB, serverSecret string, log *slog.Logger) *Service {
	return &Service{
		db:       db,
		keys:     NewKeys(serverSecret),
		settings: NewSettingsStore(db.Pool()),
		obs:      observers{log: log},
		now:      time.Now,
	}
}

// Register adds an observer. Observers run in registration order after commit.
func (s *Service) Register(o Observer) { s.obs.add(o) }

// Keys returns the derived hashing keys, so callers hash exactly like the service.
func (s *Service) Keys() Keys { return s.keys }

// Settings returns the site settings from a 30 second cache. On an error it
// returns defaults (comments off) and the error.
func (s *Service) Settings(ctx context.Context) (Settings, error) { return s.settings.Get(ctx) }

// SaveSettings stores the site settings and tells the observers. Who may
// change them (the owner only) is the caller's rule.
func (s *Service) SaveSettings(ctx context.Context, set Settings, by *uuid.UUID) error {
	if err := s.settings.Save(ctx, set, by); err != nil {
		return err
	}
	s.obs.each("settings", func(o Observer) { o.OnSettingsChanged(ctx, set) })
	return nil
}

func (s *Service) utcNow() time.Time { return s.now().UTC() }

// commentColumns is the column list scanComment reads.
const commentColumns = `id, entity_id, parent_id, path, depth, status, body_raw, body_html, has_links,
	link_domains, body_hash, commenter_id, author_name, author_user_id, ip_hash, net_hash, user_agent,
	spam_score, spam_verdict, spam_reasons, created_at, updated_at, confirmed_at, approved_at,
	approved_by, edited_at, locale, notify_replies, rank, dead_by`

func scanComment(row pgx.Row) (Comment, error) {
	var c Comment
	var status string
	var reasons []byte
	var deadBy *string
	err := row.Scan(&c.ID, &c.EntityID, &c.ParentID, &c.Path, &c.Depth, &status, &c.BodyRaw, &c.BodyHTML,
		&c.HasLinks, &c.LinkDomains, &c.BodyHash, &c.CommenterID, &c.AuthorName, &c.AuthorUserID,
		&c.IPHash, &c.NetHash, &c.UserAgent, &c.SpamScore, &c.SpamVerdict, &reasons, &c.CreatedAt,
		&c.UpdatedAt, &c.ConfirmedAt, &c.ApprovedAt, &c.ApprovedBy, &c.EditedAt, &c.Locale, &c.NotifyReplies,
		&c.Rank, &deadBy)
	if err != nil {
		return Comment{}, err
	}
	c.Status = Status(status)
	if deadBy != nil {
		c.DeadBy = *deadBy
	}
	if len(reasons) > 0 {
		if err := jsonx.Unmarshal(reasons, &c.SpamReasons, jsonx.StoredLoose); err != nil {
			c.SpamReasons = nil
		}
	}
	return c, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func reasonsJSON(r []string) (string, error) {
	b, err := jsonx.Marshal(nonNil(r))
	return string(b), err
}
