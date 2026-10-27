//go:build integration

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/worker"
	"hatamoto.dev/sdk"
)

// These tests are the five scenarios of the former first-party webhooks plugin
// (from the removed first-party plugin test rig, last at 63a7d260) run against core: the real
// hook engine, entity sink, enqueue listeners and deliverer, no plugin.
// CI greps for their names; see .github/workflows/ci.yml.

type received struct {
	path   string
	header http.Header
	body   string
}

// receiver answers with the status script of the path (then 200) and records requests.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	got    []received
	script map[string][]int
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	rc := &receiver{script: map[string][]int{}}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		defer rc.mu.Unlock()
		status := 200
		if s := rc.script[r.URL.Path]; len(s) > 0 {
			status, rc.script[r.URL.Path] = s[0], s[1:]
		}
		rc.got = append(rc.got, received{path: r.URL.Path, header: r.Header.Clone(), body: string(b)})
		w.WriteHeader(status)
		if r.URL.Path == "/big" {
			_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
			return
		}
		_, _ = w.Write([]byte("answer " + http.StatusText(status)))
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) requests(path string) []received {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []received
	for _, g := range rc.got {
		if g.path == path {
			out = append(out, g)
		}
	}
	return out
}

// coreRig is the service, the hook engine with the enqueue listeners and a
// deliverer that may reach the loopback receivers (tests only).
type coreRig struct {
	svc   *Service
	hooks *hooks.Engine
	sink  *pluginhost.EntitySink
	del   *Deliverer
}

func newCoreRig(t *testing.T) *coreRig {
	t.Helper()
	svc, _ := testService(t)
	h := hooks.New(svc.log)
	Register(h, svc.repo, svc.log)
	return &coreRig{svc: svc, hooks: h, sink: pluginhost.NewEntitySink(h, nil), del: testDelivererOn(svc, Options{})}
}

func (r *coreRig) run(t *testing.T) {
	t.Helper()
	if err := r.del.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (r *coreRig) rows(t *testing.T, subID string) []DeliveryRow {
	t.Helper()
	rows, err := r.svc.repo.ListDeliveries(context.Background(), subID, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(rows) // oldest first
	return rows
}

func (r *coreRig) updated(version int) {
	r.hooks.Fire(context.Background(), string(entity.EventUpdated), sdk.EntityEvent{ID: uuid.NewString(), SchemaSlug: "post", Slug: "p", Name: "P", Version: version})
}

func hexMAC(secret, msg string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhooksCoreDeliversSignedPayloadsAndRetries(t *testing.T) {
	r := newCoreRig(t)
	ctx := context.Background()
	rc := newReceiver(t)
	create := func(path string, events ...string) (Subscription, string) {
		s := validSub()
		s.URL, s.AllowHTTP, s.Events, s.MaxRetries, s.RetryDelaySeconds = rc.srv.URL+path, true, events, 2, 0
		s.Headers = map[string]string{"X-Team": "docs"}
		created, secret, err := r.svc.Create(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		return created, secret
	}
	ok, secret := create("/ok", EventEntityPublished, EventImportCompleted, EventFormSubmitted)
	flaky, flakySecret := create("/flaky", EventEntityPublished)
	rc.script["/flaky"] = []int{500, 500}
	big, _ := create("/big", EventEntityPublished)

	// Validation: plain http needs the opt-in, other schemes are refused.
	bad := validSub()
	bad.URL, bad.AllowHTTP = "http://example.com/h", false
	if _, _, err := r.svc.Create(ctx, bad); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http must be refused by default: %v", err)
	}
	bad.URL = "ftp://x"
	if _, _, err := r.svc.Create(ctx, bad); err == nil {
		t.Fatal("a bad url must be refused")
	}
	// The list never shows a secret.
	list, err := r.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(list); strings.Contains(string(b), "whsec_") {
		t.Fatalf("secrets leaked into the list: %s", b)
	}

	// entity.published only queues; the job sends, signed.
	id := uuid.New()
	r.sink.EntityPublished(ctx, id, "post", "hello", "Hello", 3)
	r.hooks.Wait()
	if n := len(rc.requests("/ok")); n != 0 {
		t.Fatalf("the hook must only queue, the receiver already got %d requests", n)
	}
	r.run(t)
	reqs := rc.requests("/ok")
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	h := got.header
	if h.Get("Content-Type") != "application/json" || h.Get("User-Agent") != "Hatamoto-Webhook/2.0" ||
		h.Get("X-Webhook-ID") != ok.ID || h.Get("X-Webhook-Event") != "entity.published" ||
		h.Get("X-Webhook-Delivery") == "" || h.Get("X-Webhook-Timestamp") == "" || h.Get("X-Team") != "docs" {
		t.Fatalf("headers: %v", h)
	}
	if want := "sha256=" + hexMAC(secret, got.body); h.Get("X-Webhook-Signature") != want {
		t.Fatalf("signature %q, want %q", h.Get("X-Webhook-Signature"), want)
	}
	ts, sig, _ := strings.Cut(strings.TrimPrefix(h.Get("X-Webhook-Signature-V2"), "t="), ",v1=")
	if sig != hexMAC(secret, ts+"."+got.body) {
		t.Fatalf("v2 signature %q does not verify", h.Get("X-Webhook-Signature-V2"))
	}
	// Envelope: key order id, event, timestamp, data.
	pos := []int{strings.Index(got.body, `"id"`), strings.Index(got.body, `"event"`), strings.Index(got.body, `"timestamp"`), strings.Index(got.body, `"data"`)}
	if !slices.IsSorted(pos) || pos[0] != 1 {
		t.Fatalf("envelope key order: %s", got.body)
	}
	var env struct {
		ID, Event, Timestamp string
		Data                 map[string]any
	}
	if err := json.Unmarshal([]byte(got.body), &env); err != nil {
		t.Fatal(err)
	}
	if env.Event != "entity.published" || env.Data["entity_id"] != id.String() || env.Data["entity_name"] != "Hello" || env.ID == "" {
		t.Fatalf("envelope: %s", got.body)
	}

	// The 500 receiver is retried until delivered, sending the same signed body.
	if rows := r.rows(t, flaky.ID); len(rows) != 1 || rows[0].Status != "pending" || rows[0].Attempts != 1 {
		t.Fatalf("after the first 500: %+v", rows)
	}
	r.run(t) // second 500
	r.run(t) // success
	if rows := r.rows(t, flaky.ID); rows[0].Status != "delivered" || rows[0].Attempts != 3 || len(rc.requests("/flaky")) != 3 {
		t.Fatalf("after the retries: %+v requests=%d", rows, len(rc.requests("/flaky")))
	}
	fr := rc.requests("/flaky")
	if fr[2].header.Get("X-Webhook-Signature") != "sha256="+hexMAC(flakySecret, fr[2].body) || fr[2].body != fr[0].body {
		t.Fatal("a retry must send the same signed body")
	}

	// The answer is stored clipped to 1024 bytes.
	if rows := r.rows(t, big.ID); rows[0].Status != "delivered" || len(rows[0].ResponseBody) != MaxResponseBody {
		t.Fatalf("big answer: status %s, stored %d bytes", rows[0].Status, len(rows[0].ResponseBody))
	}

	// Every attempt failing ends failed; a manual retry delivers.
	rc.script["/flaky"] = []int{500, 500, 500}
	r.sink.EntityPublished(ctx, uuid.New(), "post", "again", "Again", 1)
	r.hooks.Wait()
	r.run(t)
	r.run(t)
	r.run(t)
	var failed *DeliveryRow
	for _, d := range r.rows(t, flaky.ID) {
		if d.Status == "failed" {
			failed = &d
		}
	}
	if failed == nil || failed.Attempts != 3 || failed.ResponseStatus == nil || *failed.ResponseStatus != 500 {
		t.Fatalf("delivery log: %+v", r.rows(t, flaky.ID))
	}
	if err := r.svc.repo.RetryFailed(ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	r.run(t)
	for _, d := range r.rows(t, flaky.ID) {
		if d.ID == failed.ID && d.Status != "delivered" {
			t.Fatalf("manual retry left the delivery %s", d.Status)
		}
	}

	// import.completed and form.submitted are mapped.
	r.hooks.Fire(ctx, sdk.ImportCompleted.Name, sdk.ImportEvent{Source: "wordpress", Created: 4, Updated: 1})
	r.hooks.Fire(ctx, sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{FormID: "f1", FormSlug: "contact", SubmissionID: "s1", Fields: map[string]any{"email": "a@b.c"}})
	r.hooks.Wait()
	r.run(t)
	var events []string
	for _, g := range rc.requests("/ok") {
		events = append(events, g.header.Get("X-Webhook-Event"))
	}
	slices.Sort(events)
	if strings.Join(events, ",") != "entity.published,entity.published,form.submitted,import.completed" {
		t.Fatalf("events delivered to /ok: %v", events)
	}

	// Backoff values of the receiver contract.
	for _, c := range []struct {
		attempt int
		base    time.Duration
		want    time.Duration
	}{{1, 60 * time.Second, 2 * time.Minute}, {2, 60 * time.Second, 4 * time.Minute}, {3, 60 * time.Second, 8 * time.Minute}, {10, 60 * time.Second, time.Hour}, {1, 0, 0}} {
		if got := Backoff(c.attempt, c.base); got != c.want {
			t.Errorf("Backoff(%d, %s) = %s, want %s", c.attempt, c.base, got, c.want)
		}
	}
}

func TestWebhooksCoreBurstIsQueuedWholeAndDelivered(t *testing.T) {
	r := newCoreRig(t)
	rc := newReceiver(t)
	sub := subOn(t, r.svc, rc.srv.URL+"/burst", func(s *Subscription) { s.Events = []string{EventEntityUpdated} })

	for i := range 50 {
		r.updated(i + 1)
	}
	r.hooks.Wait()
	if n := len(rc.requests("/burst")); n != 0 {
		t.Fatalf("the hooks sent %d requests; they must only queue", n)
	}
	if rows := r.rows(t, sub.ID); len(rows) != 50 {
		t.Fatalf("%d delivery rows after 50 events, want 50 (an event was dropped)", len(rows))
	}
	for run := 0; run < 5 && len(rc.requests("/burst")) < 50; run++ {
		r.run(t)
	}
	if n := len(rc.requests("/burst")); n != 50 {
		t.Fatalf("receiver got %d of 50 events", n)
	}
	seen := map[string]bool{}
	for _, g := range rc.requests("/burst") {
		seen[g.header.Get("X-Webhook-Delivery")] = true
	}
	if len(seen) != 50 {
		t.Fatalf("%d distinct deliveries, want 50 (some were sent twice)", len(seen))
	}
}

func TestWebhooksCoreHungReceiverNeitherTrapsNorBlocksCapture(t *testing.T) {
	r := newCoreRig(t)
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-hang:
		case <-req.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); slow.Close() })
	rc := newReceiver(t)
	only := func(s *Subscription) {
		s.Events, s.TimeoutSeconds, s.MaxRetries, s.RetryDelaySeconds = []string{EventEntityUpdated}, 2, 1, 3600
	}
	subOn(t, r.svc, slow.URL, only)
	okSub := subOn(t, r.svc, rc.srv.URL+"/fine", only)

	start := time.Now()
	for i := range 6 {
		r.updated(i + 1)
	}
	r.hooks.Wait()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("capturing 6 events took %s; a hung receiver must not slow capture (timeout is 2s)", took)
	}
	start = time.Now()
	for range 3 {
		r.run(t)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("three runs took %s", d)
	}
	if n := len(rc.requests("/fine")); n != 6 {
		t.Fatalf("the healthy receiver got %d of 6 events; a hung one must not starve it", n)
	}
	before := len(r.rows(t, okSub.ID))
	for i := range 4 {
		r.updated(100 + i)
	}
	r.hooks.Wait()
	if after := len(r.rows(t, okSub.ID)); after != before+4 {
		t.Fatalf("captured %d new rows, want 4", after-before)
	}
}

func TestWebhooksCoreDeadClaimCountsAsAnAttempt(t *testing.T) {
	r := newCoreRig(t)
	ctx := context.Background()
	rc := newReceiver(t)
	rc.script["/dead"] = []int{500, 500, 500}
	sub := subOn(t, r.svc, rc.srv.URL+"/dead", func(s *Subscription) {
		s.Events, s.MaxRetries, s.RetryDelaySeconds = []string{EventEntityUpdated}, 2, 0 // 3 attempts
	})
	r.updated(1)
	r.hooks.Wait()
	dieWhileClaimed := func() {
		runStart, err := r.svc.repo.DBNow(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if d, err := r.svc.repo.ClaimOne(ctx, runStart, nil); err != nil || d == nil {
			t.Fatal(d, err)
		}
		exec(t, r.svc, `UPDATE hatamoto_webhook_deliveries SET leased_until = now() - interval '1 second' WHERE subscription_id = $1`, sub.ID)
	}
	// A call claimed the row and died. The next run counts that attempt (1),
	// then makes a real one (2), which the receiver answers with a 500.
	dieWhileClaimed()
	r.run(t)
	if rows := r.rows(t, sub.ID); len(rows) != 1 || rows[0].Status != "pending" || rows[0].Attempts != 2 {
		t.Fatalf("after one dead claim: %+v", rows)
	}
	// A second dead claim uses up the third and last attempt.
	dieWhileClaimed()
	r.run(t)
	if rows := r.rows(t, sub.ID); len(rows) != 1 || rows[0].Status != "failed" || rows[0].Attempts != 3 {
		t.Fatalf("after two dead claims: %+v", rows)
	}
}

// A scheduled publish goes through the publish worker and the entity sink, not
// the entity service, and still enqueues entity.published.
func TestWebhooksCoreScheduledPublishEnqueues(t *testing.T) {
	r := newCoreRig(t)
	ctx := context.Background()
	rc := newReceiver(t)
	sub := subOn(t, r.svc, rc.srv.URL+"/sched", func(s *Subscription) { s.Events = []string{EventEntityPublished} })

	var schemaID, entityID string
	if err := r.svc.repo.db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema) VALUES ('Sched', 'sched', '{"type":"object"}') RETURNING id::text`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.repo.db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at)
		VALUES ($1, 'Later', 'later', 'scheduled', now() - interval '1 minute') RETURNING id::text`, schemaID).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	pw := worker.NewPublishWorker(r.svc.repo.db, r.svc.log)
	pw.SetEvents(r.sink)
	if promoted, err := pw.Run(ctx); err != nil || len(promoted) != 1 {
		t.Fatalf("publish worker: %v, %v", promoted, err)
	}
	r.hooks.Wait()
	if rows := r.rows(t, sub.ID); len(rows) != 1 || rows[0].Event != EventEntityPublished {
		t.Fatalf("rows after a scheduled publish: %+v", rows)
	}
	r.run(t)
	reqs := rc.requests("/sched")
	if len(reqs) != 1 || !strings.Contains(reqs[0].body, entityID) {
		t.Fatalf("receiver: %+v", reqs)
	}
}

func TestWebhooksCoreFormSubmittedCarriesNoFields(t *testing.T) {
	r := newCoreRig(t)
	ctx := context.Background()
	rc := newReceiver(t)
	sub := subOn(t, r.svc, rc.srv.URL+"/form", func(s *Subscription) { s.Events = []string{EventFormSubmitted} })

	r.hooks.Fire(ctx, sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{
		FormID: "f1", FormSlug: "contact", SubmissionID: "s1",
		Fields: map[string]any{"email": "a@b.c", "message": "private words"},
	})
	r.hooks.Wait()
	r.run(t)

	var payload string
	if err := r.svc.repo.db.Pool().QueryRow(ctx, `SELECT payload FROM hatamoto_webhook_deliveries WHERE subscription_id = $1`, sub.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var env struct{ Data map[string]any }
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 3 || env.Data["form_id"] != "f1" || env.Data["form_slug"] != "contact" || env.Data["submission_id"] != "s1" {
		t.Fatalf("data = %v, want exactly form_id, form_slug, submission_id", env.Data)
	}
	reqs := rc.requests("/form")
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests", len(reqs))
	}
	for _, s := range []string{payload, reqs[0].body} {
		if strings.Contains(s, "a@b.c") || strings.Contains(s, "private words") || strings.Contains(strings.ToLower(s), "fields") {
			t.Fatalf("submitted fields leaked: %s", s)
		}
	}
}
