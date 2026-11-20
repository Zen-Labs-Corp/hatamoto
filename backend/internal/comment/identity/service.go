package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
)

// Comments is the part of the comment model the identity layer needs.
// *comment.Service satisfies it as is.
type Comments interface {
	Confirm(ctx context.Context, id uuid.UUID) (comment.Comment, error)
	Settings(ctx context.Context) (comment.Settings, error)
	MatchBlocks(ctx context.Context, p comment.Probe) ([]comment.Block, error)
}

// Mailer sends the confirmation mail.
type Mailer interface {
	SendCommentConfirmation(ctx context.Context, to string, d email.CommentConfirmationData) error
}

// SiteInfo returns the site name and the configured base URL (never a request
// host). An empty base means no link can be built.
type SiteInfo func(ctx context.Context) (name, baseURL string)

type store interface {
	Issue(ctx context.Context, r issueRow, l Limits) (bool, error)
	Peek(ctx context.Context, hash []byte, now time.Time) (commentID, entityID, commenterID uuid.UUID, ok bool, err error)
	Consume(ctx context.Context, hash []byte, now time.Time) (commentID, commenterID uuid.UUID, ok bool, err error)
	TokenVersion(ctx context.Context, commenterID uuid.UUID) (int, bool, error)
	Purge(ctx context.Context, now time.Time) (drafts, tokens, commenters int64, err error)
}

// Config wires a Service.
type Config struct {
	DB       *database.DB
	Comments Comments
	Mailer   Mailer
	Site     SiteInfo
	Secret   string
	Limits   Limits           // zero value: DefaultLimits
	Now      func() time.Time // nil: time.Now
	Logger   *slog.Logger
}

// Outcome is what RequestConfirmation reports. There is one value on purpose:
// sent, limited, failed and unconfigured look the same to the visitor.
type Outcome int

// OutcomeCheckEmail means: show the "check your email" message.
const OutcomeCheckEmail Outcome = 1

// ConfirmRequest describes one confirmation to send.
type ConfirmRequest struct {
	Comment     comment.Comment // from comment.Service.Store, Status draft
	Email       string          // recipient only, never stored or logged
	EntityTitle string
}

// Pending is what a GET of the confirm link may learn without consuming it.
type Pending struct{ CommentID, EntityID uuid.UUID }

// Confirmed is the result of a successful confirmation.
type Confirmed struct {
	Comment        comment.Comment
	CommenterID    uuid.UUID
	TokenVersion   int
	CookieLifetime time.Duration
}

var (
	ErrTokenInvalid = errors.New("identity: link invalid, expired or used")
	ErrNotDraft     = errors.New("identity: comment is not an unconfirmed draft with a commenter")
)

// Service is the commenter identity layer.
type Service struct {
	comments Comments
	mailer   Mailer
	site     SiteInfo
	store    store
	key      []byte
	limits   Limits
	now      func() time.Time
	log      *slog.Logger
}

// New builds a Service. It fails closed on an empty secret.
func New(cfg Config) (*Service, error) {
	if cfg.Secret == "" {
		return nil, errors.New("identity: empty server secret")
	}
	if cfg.DB == nil || cfg.Comments == nil || cfg.Mailer == nil || cfg.Site == nil {
		return nil, errors.New("identity: DB, Comments, Mailer and Site are required")
	}
	s := &Service{
		comments: cfg.Comments,
		mailer:   cfg.Mailer,
		site:     cfg.Site,
		store:    &pgStore{db: cfg.DB},
		key:      DeviceKey(cfg.Secret),
		limits:   cfg.Limits,
		now:      cfg.Now,
		log:      cfg.Logger,
	}
	if s.limits == (Limits{}) {
		s.limits = DefaultLimits
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// Enabled reports whether comments are on; it fails closed.
func (s *Service) Enabled(ctx context.Context) bool {
	set, err := s.comments.Settings(ctx)
	return err == nil && set.Enabled
}

// CheckEmail validates an address and rejects disposable domains.
func (s *Service) CheckEmail(ctx context.Context, addr string) error {
	return checkEmail(ctx, s.comments, addr)
}

// RequestConfirmation mails a single-use confirm link unless a send limit is
// hit. Every non-error path returns OutcomeCheckEmail.
func (s *Service) RequestConfirmation(ctx context.Context, req ConfirmRequest) (Outcome, error) {
	c := req.Comment
	if c.Status != comment.StatusDraft || c.CommenterID == nil {
		return 0, ErrNotDraft
	}
	if err := s.CheckEmail(ctx, req.Email); err != nil {
		return 0, err
	}
	name, base := s.site(ctx)
	if base == "" {
		s.log.Warn("comment confirmation not sent: no site address")
		return OutcomeCheckEmail, nil
	}
	raw, hash, err := NewToken()
	if err != nil {
		return 0, err
	}
	limited, err := s.store.Issue(ctx, issueRow{
		Hash: hash, CommentID: c.ID, CommenterID: *c.CommenterID,
		IPHash: c.IPHash, NetHash: c.NetHash, Now: s.now(),
	}, s.limits)
	if err != nil {
		return 0, err
	}
	if limited {
		s.log.Debug("comment confirmation not sent: limit reached")
		return OutcomeCheckEmail, nil
	}
	url := strings.TrimRight(base, "/") + "/comments/confirm/" + raw
	if err := s.mailer.SendCommentConfirmation(ctx, req.Email, email.CommentConfirmationData{
		SiteName: name, EntityTitle: req.EntityTitle, URL: url, ExpiresIn: "24 hours",
	}); err != nil {
		s.log.Warn("comment confirmation mail failed")
	}
	return OutcomeCheckEmail, nil
}

// Device returns the commenter a device cookie belongs to. The cookie must be
// correctly signed, unexpired and carry the commenter's current token_version.
func (s *Service) Device(ctx context.Context, val string) (uuid.UUID, bool) {
	claims, ok := VerifyDevice(s.key, val, s.now())
	if !ok {
		return uuid.Nil, false
	}
	v, found, err := s.store.TokenVersion(ctx, claims.CommenterID)
	if err != nil || !found || v != claims.TokenVersion {
		return uuid.Nil, false
	}
	return claims.CommenterID, true
}

// Peek looks at a token without consuming it (GET of the confirm link).
func (s *Service) Peek(ctx context.Context, raw string) (Pending, error) {
	hash, ok := HashToken(raw)
	if !ok {
		return Pending{}, ErrTokenInvalid
	}
	commentID, entityID, _, ok, err := s.store.Peek(ctx, hash, s.now())
	if err != nil {
		return Pending{}, err
	}
	if !ok {
		return Pending{}, ErrTokenInvalid
	}
	return Pending{CommentID: commentID, EntityID: entityID}, nil
}

// Confirm consumes the token and confirms the draft (POST of the confirm link).
func (s *Service) Confirm(ctx context.Context, raw string) (Confirmed, error) {
	hash, ok := HashToken(raw)
	if !ok {
		return Confirmed{}, ErrTokenInvalid
	}
	commentID, commenterID, ok, err := s.store.Consume(ctx, hash, s.now())
	if err != nil {
		return Confirmed{}, err
	}
	if !ok {
		return Confirmed{}, ErrTokenInvalid
	}
	c, err := s.comments.Confirm(ctx, commentID)
	if errors.Is(err, comment.ErrNotFound) || errors.Is(err, comment.ErrBadTransition) {
		return Confirmed{}, ErrTokenInvalid
	}
	if err != nil {
		return Confirmed{}, err
	}
	ver, found, err := s.store.TokenVersion(ctx, commenterID)
	if err != nil {
		return Confirmed{}, err
	}
	if !found {
		return Confirmed{}, ErrTokenInvalid
	}
	set, err := s.comments.Settings(ctx)
	if err != nil {
		s.log.Warn("comment settings unavailable, using defaults for the device cookie")
	}
	days := set.DeviceCookieDays
	if days < 1 {
		days = comment.Defaults().DeviceCookieDays
	}
	return Confirmed{
		Comment: c, CommenterID: commenterID, TokenVersion: ver,
		CookieLifetime: time.Duration(days) * 24 * time.Hour,
	}, nil
}

// Cookie builds the hm_c device cookie for a confirmation.
func (s *Service) Cookie(c Confirmed, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: CookieName,
		Value: SignDevice(s.key, DeviceClaims{
			CommenterID: c.CommenterID, TokenVersion: c.TokenVersion, Expires: s.now().Add(c.CookieLifetime),
		}),
		Path:     "/",
		MaxAge:   int(c.CookieLifetime / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// Purge deletes expired unconfirmed drafts, old tokens and idle draft-only commenters.
func (s *Service) Purge(ctx context.Context) error {
	drafts, tokens, commenters, err := s.store.Purge(ctx, s.now())
	if err != nil {
		return fmt.Errorf("identity: purge: %w", err)
	}
	s.log.Info("comment drafts purged", "drafts", drafts, "tokens", tokens, "commenters", commenters)
	return nil
}
