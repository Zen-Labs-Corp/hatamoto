package webhook

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/safefetch"
)

const (
	userAgent = "Hatamoto-Webhook/2.0"
	// minAttempt is the least time left in a run for which another attempt is started.
	minAttempt = time.Second
	runBatch   = 100
	// testBudget bounds the inline attempt of "Send test".
	testBudget = 7 * time.Second
	// testPark keeps the Send-test row away from the job while it is attempted inline.
	testPark = 60 * time.Second
)

// Options configures a Deliverer.
type Options struct {
	// AllowPrivate lets the sender reach loopback/private addresses and any
	// port. For tests only: production wiring never sets it.
	AllowPrivate bool
	Lookup       safefetch.Lookup
	Now          func() time.Time
	// RunBudget is how long one Run spends sending; default 25s.
	RunBudget time.Duration
}

// Deliverer sends queued deliveries.
type Deliverer struct {
	repo    *Repo
	log     *slog.Logger
	now     func() time.Time
	budget  time.Duration
	anyPort bool            // tests only, with AllowPrivate
	clients [2]*http.Client // [0] https only, [1] plain http allowed
}

// NewDeliverer returns a Deliverer on repo.
func NewDeliverer(repo *Repo, log *slog.Logger, o Options) *Deliverer {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RunBudget == 0 {
		o.RunBudget = 25 * time.Second
	}
	return &Deliverer{repo: repo, log: log, now: o.Now, budget: o.RunBudget, anyPort: o.AllowPrivate,
		clients: [2]*http.Client{newClient(o, false), newClient(o, true)}}
}

// Run settles dead claims, then sends due deliveries one by one until none is
// due, runBatch were tried or the budget is nearly spent. A subscription whose
// request failed at the transport (timeout, refused) is left alone for the
// rest of the run, so one hung receiver cannot starve the others.
func (d *Deliverer) Run(ctx context.Context) error {
	deadline := d.now().Add(d.budget)
	if _, err := d.repo.SettleDead(ctx); err != nil {
		return err
	}
	runStart, err := d.repo.DBNow(ctx)
	if err != nil {
		return fmt.Errorf("reading the database clock: %w", err)
	}
	var skip []string
	for range runBatch {
		left := deadline.Sub(d.now())
		if left < minAttempt || ctx.Err() != nil {
			return ctx.Err() // the rest waits for the next run
		}
		del, err := d.repo.ClaimOne(ctx, runStart, skip)
		if err != nil || del == nil {
			return err
		}
		transport, err := d.attempt(ctx, *del, left)
		if err != nil {
			d.log.Warn("webhook: delivery failed to record", "delivery", del.ID, "error", err)
		}
		if transport {
			skip = append(skip, del.SubscriptionID)
		}
	}
	return nil
}

// SendTest queues a webhook.test event for one subscription, whatever events it
// listens to, and attempts it now. It returns the delivery id.
func (d *Deliverer) SendTest(ctx context.Context, subscriptionID string) (string, error) {
	if _, err := d.repo.Get(ctx, subscriptionID); err != nil {
		return "", err
	}
	id := uuid.NewString()
	body, err := NewEnvelope(id, EventTest, d.now(), map[string]string{"message": "test delivery from Hatamoto"})
	if err != nil {
		return "", err
	}
	if err := d.repo.InsertParked(ctx, id, subscriptionID, EventTest, string(body), 1, testPark); err != nil {
		return "", err
	}
	_, err = d.attempt(ctx, Delivery{ID: id, SubscriptionID: subscriptionID, Event: EventTest, Payload: string(body), MaxAttempts: 1}, testBudget)
	return id, err
}

// clip bounds a stored response body; it also drops bytes Postgres text cannot hold.
func clip(b []byte) string {
	if len(b) > MaxResponseBody {
		b = b[:MaxResponseBody]
	}
	return strings.ReplaceAll(strings.ToValidUTF8(string(b), ""), "\x00", "")
}

func statusPtr(code int) *int {
	if code == 0 {
		return nil
	}
	return &code
}

// buildRequest assembles the signed POST. Header order is part of the
// contract: the sender's own headers, then the subscription's custom ones,
// then both signatures last so a custom header can never replace them.
func buildRequest(ctx context.Context, sub *Subscription, del Delivery, now time.Time) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, strings.NewReader(del.Payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Webhook-ID", sub.ID)
	req.Header.Set("X-Webhook-Event", del.Event)
	req.Header.Set("X-Webhook-Delivery", del.ID)
	req.Header.Set("X-Webhook-Timestamp", now.UTC().Format(time.RFC3339))
	for k, v := range sub.Headers {
		if slices.Contains(forbiddenHeaders, strings.ToLower(k)) {
			continue // validated at save time; stale rows are filtered again
		}
		req.Header.Set(k, v)
	}
	if sub.Secret != "" {
		req.Header.Set("X-Webhook-Signature", Sign(sub.Secret, []byte(del.Payload)))
		req.Header.Set("X-Webhook-Signature-V2", SignV2(sub.Secret, now.Unix(), []byte(del.Payload)))
	}
	return req, nil
}

// attempt sends del once, for at most budget, and records the outcome:
// delivered on a 2xx answer, otherwise pending with a backoff until the
// attempts are used up. transport reports a request that got no answer.
func (d *Deliverer) attempt(ctx context.Context, del Delivery, budget time.Duration) (transport bool, err error) {
	// The outcome is recorded even if the run is being cancelled; otherwise the
	// lease would expire and settleDead would count the attempt blind.
	rec, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	sub, err := d.repo.Get(ctx, del.SubscriptionID)
	if err == ErrNotFound || (err == nil && !sub.Active) {
		return false, d.repo.MarkFailed(rec, del.ID, "subscription removed or paused")
	}
	if err != nil {
		return false, err
	}

	start := d.now()
	status, body, sendErr := d.send(ctx, sub, del, min(time.Duration(sub.TimeoutSeconds)*time.Second, budget))
	dur := d.now().Sub(start)
	attempts := del.Attempts + 1
	if sendErr == nil && status >= 200 && status < 300 {
		return false, d.repo.MarkDelivered(rec, del.ID, status, body, dur)
	}
	msg := fmt.Sprintf("receiver answered HTTP %d", status)
	if sendErr != nil {
		msg = errText(sendErr)
	}
	if attempts >= del.MaxAttempts {
		return sendErr != nil, d.repo.MarkExhausted(rec, del.ID, statusPtr(status), body, msg, dur)
	}
	wait := Backoff(attempts, time.Duration(sub.RetryDelaySeconds)*time.Second)
	return sendErr != nil, d.repo.MarkRetry(rec, del.ID, statusPtr(status), body, msg, dur, wait)
}

// send performs the request under the outbound policy. It returns the HTTP
// status and the clipped body, or an error when there was no answer.
func (d *Deliverer) send(ctx context.Context, sub *Subscription, del Delivery, timeout time.Duration) (int, string, error) {
	u, err := url.Parse(sub.URL)
	if err != nil {
		return 0, "", err
	}
	client := d.clients[0]
	if sub.AllowHTTP {
		client = d.clients[1]
	}
	if err := checkHop(u, sub.AllowHTTP, d.anyPort); err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := buildRequest(ctx, sub, del, d.now())
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBody))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused
	return resp.StatusCode, clip(b), nil
}
