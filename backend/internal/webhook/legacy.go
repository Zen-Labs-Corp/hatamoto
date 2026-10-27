package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/pluginhost"
)

// PluginSource reads the former webhooks plugin's own schema. pluginhost.PluginDB
// is one: it runs the statement as the plugin's database role.
type PluginSource interface {
	Query(ctx context.Context, plugin, sql string, args ...any) (*pluginhost.QueryResult, error)
}

// ImportPluginSubscriptions copies the subscriptions of the former webhooks
// plugin into hatamoto_webhooks in one transaction, keeping ids and secrets, so
// receivers notice nothing. Each row is sanitised like the migration sanitises
// the hand-over rows. A row whose id exists already is kept as is, so running
// it twice copies nothing the second time. Delivery history is not copied.
// With paused set every row is imported paused, whatever its own flag says (the
// plugin was not sending, so core must not start). Nothing is written unless
// every row could be read.
func (r *Repo) ImportPluginSubscriptions(ctx context.Context, src PluginSource, paused bool) (copied int, err error) {
	res, err := src.Query(ctx, "webhooks", `SELECT to_jsonb(s)::text FROM subscriptions s ORDER BY created_at, id`)
	if err != nil {
		return 0, fmt.Errorf("reading the plugin's subscriptions: %w", err)
	}
	if res == nil || res.Truncated {
		return 0, fmt.Errorf("reading the plugin's subscriptions: result is incomplete")
	}
	var subs []Subscription
	for _, row := range res.Rows {
		text, _ := row[0].(string)
		var s Subscription
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return 0, fmt.Errorf("a plugin subscription is unreadable: %w", err) // no row content: it holds a secret
		}
		if s.ID == "" || strings.TrimSpace(s.URL) == "" {
			continue
		}
		SanitizeImported(&s)
		if paused {
			s.Active = false
		}
		subs = append(subs, s)
	}

	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, s := range subs {
		tag, err := tx.Exec(ctx, `INSERT INTO hatamoto_webhooks (id, name, url, secret, events, headers, active,
				max_retries, retry_delay_seconds, timeout_seconds, allow_http)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (id) DO NOTHING`,
			s.ID, s.Name, s.URL, s.Secret, nonNilEvents(s.Events), nonNilHeaders(s.Headers), s.Active,
			s.MaxRetries, s.RetryDelaySeconds, s.TimeoutSeconds, s.AllowHTTP)
		if err != nil {
			return 0, fmt.Errorf("copying subscription %s: %w", s.ID, err)
		}
		copied += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return copied, nil
}

// SanitizeImported applies to an imported subscription the rules Validate
// applies to a new one: numbers are clamped, unknown and repeated events and
// headers the sender owns are dropped, the name falls back to the address and
// plain http is flagged (it worked before). A subscription that is still
// invalid afterwards (no events, an unusable address) is left paused.
func SanitizeImported(s *Subscription) {
	var events []string
	for _, e := range s.Events {
		if slices.Contains(eventTypes, e) && !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	s.Events = events
	headers := map[string]string{}
	for k, v := range s.Headers {
		if k == "" || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") ||
			slices.Contains(forbiddenHeaders, strings.ToLower(k)) || len(headers) >= 20 {
			continue
		}
		headers[k] = v
	}
	s.Headers = headers
	s.URL = strings.TrimSpace(s.URL)
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = s.URL
	}
	if r := []rune(s.Name); len(r) > 120 {
		s.Name = string(r[:120])
	}
	s.MaxRetries = min(max(s.MaxRetries, 0), 10)
	s.RetryDelaySeconds = min(max(s.RetryDelaySeconds, 0), 3600)
	s.TimeoutSeconds = min(max(s.TimeoutSeconds, 1), MaxTimeoutSeconds)
	s.AllowHTTP = strings.HasPrefix(strings.ToLower(s.URL), "http://")
	if len(events) == 0 {
		s.Active = false
		return
	}
	if s.problem() != "" {
		s.Active = false
	}
}
