package webhook

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// Service is the management API of webhook subscriptions (the admin page
// calls it). It makes no network call.
type Service struct {
	repo *Repo
	log  *slog.Logger
}

// NewService returns a Service on db.
func NewService(db *database.DB, log *slog.Logger) *Service {
	return &Service{repo: NewRepo(db), log: log}
}

// Repo is the service's repository, for the delivery job and the hooks.
func (s *Service) Repo() *Repo { return s.repo }

// SubscriptionPatch is a partial update: nil fields keep their value.
type SubscriptionPatch struct {
	Name              *string
	URL               *string
	Events            *[]string
	Headers           *map[string]string
	Active            *bool
	MaxRetries        *int
	RetryDelaySeconds *int
	TimeoutSeconds    *int
	AllowHTTP         *bool
}

// Create validates and stores a subscription and returns it with its new
// secret, the only time the secret is shown. Callers start from
// Subscription.ApplyDefaults and fill the fields the user set.
func (s *Service) Create(ctx context.Context, in Subscription) (Subscription, string, error) {
	if err := in.Validate(); err != nil {
		return Subscription{}, "", err
	}
	n, err := s.repo.Count(ctx)
	if err != nil {
		return Subscription{}, "", err
	}
	if n >= MaxSubscriptions {
		return Subscription{}, "", &ValidationError{Msg: "at most 50 subscriptions"}
	}
	secret, err := NewSecret()
	if err != nil {
		return Subscription{}, "", err
	}
	in.ID, in.Secret = uuid.NewString(), secret
	if err := s.repo.Insert(ctx, &in); err != nil {
		return Subscription{}, "", err
	}
	created := in
	created.Secret = ""
	s.log.Info("webhook: created", "id", created.ID)
	return created, secret, nil
}

// Update merges patch onto the stored subscription and re-validates it. The id
// and the secret are not editable.
func (s *Service) Update(ctx context.Context, id string, p SubscriptionPatch) error {
	cur, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.Name != nil {
		cur.Name = *p.Name
	}
	if p.URL != nil {
		cur.URL = *p.URL
	}
	if p.Events != nil {
		cur.Events = *p.Events
	}
	if p.Headers != nil {
		cur.Headers = *p.Headers
	}
	if p.Active != nil {
		cur.Active = *p.Active
	}
	if p.MaxRetries != nil {
		cur.MaxRetries = *p.MaxRetries
	}
	if p.RetryDelaySeconds != nil {
		cur.RetryDelaySeconds = *p.RetryDelaySeconds
	}
	if p.TimeoutSeconds != nil {
		cur.TimeoutSeconds = *p.TimeoutSeconds
	}
	if p.AllowHTTP != nil {
		cur.AllowHTTP = *p.AllowHTTP
	}
	if err := cur.Validate(); err != nil {
		return err
	}
	return s.repo.Update(ctx, cur)
}

// RegenerateSecret replaces the secret and returns the new one.
func (s *Service) RegenerateSecret(ctx context.Context, id string) (string, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", err
	}
	if err := s.repo.SetSecret(ctx, id, secret); err != nil {
		return "", err
	}
	s.log.Info("webhook: secret regenerated", "id", id)
	return secret, nil
}

// SetActive pauses or resumes a subscription.
func (s *Service) SetActive(ctx context.Context, id string, active bool) error {
	return s.repo.SetActive(ctx, id, active)
}

// Delete removes a subscription and its deliveries.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.log.Info("webhook: deleted", "id", id)
	return nil
}

// List returns every subscription with its delivery counts, never the secret.
func (s *Service) List(ctx context.Context) ([]ListedSubscription, error) {
	return s.repo.List(ctx)
}

// CountFailedSince returns how many deliveries queued at or after since have failed.
func (s *Service) CountFailedSince(ctx context.Context, since time.Time) (int, error) {
	return s.repo.CountFailedSince(ctx, since)
}

// Get returns one subscription including its secret.
func (s *Service) Get(ctx context.Context, id string) (*Subscription, error) {
	return s.repo.Get(ctx, id)
}

// RetryDelivery queues a failed delivery for one more attempt. It reports
// false, changing nothing, for a delivery that has not failed, and ErrNotFound
// for an unknown id.
func (s *Service) RetryDelivery(ctx context.Context, id string) (bool, error) {
	row, err := s.repo.GetDelivery(ctx, id)
	if err != nil || row.Status != "failed" {
		return false, err
	}
	return true, s.repo.RetryFailed(ctx, id)
}
