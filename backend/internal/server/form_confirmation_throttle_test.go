package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

func newThrottleTestServer(sender formConfirmationSender) *Server {
	return &Server{
		cfg:                    &config.Config{},
		logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		formConfirmationSender: sender,
	}
}

func TestFormConfirmation_NameOnlyWhenFormDefinesIt(t *testing.T) {
	data := map[string]interface{}{"name": "Your account is locked", "work_email": "a@example.com"}
	f := testForm(true, nil)
	f.Fields = f.Fields[1:] // drop the name field
	_, msg, ok := formConfirmation(f, data, "S")
	if !ok || msg.Name != "" {
		t.Fatalf("name echoed without a name field: ok=%v name=%q", ok, msg.Name)
	}
}

func TestFormConfirmation_NameTruncatedTo100(t *testing.T) {
	long := strings.Repeat("é", 150)
	_, msg, ok := formConfirmation(testForm(true, nil), map[string]interface{}{"name": long, "work_email": "a@example.com"}, "S")
	if !ok || len([]rune(msg.Name)) != 100 {
		t.Fatalf("ok=%v name runes=%d", ok, len([]rune(msg.Name)))
	}
}

func TestConfirmationThrottle_RecipientCooldown(t *testing.T) {
	var th confirmationThrottle
	now := time.Now()
	if !th.allow("f1", "A@Example.com", now) {
		t.Fatal("first send must be allowed")
	}
	if th.allow("f1", "a@example.com", now.Add(9*time.Minute)) {
		t.Fatal("same recipient+form within 10m must be skipped (case-insensitive)")
	}
	if !th.allow("f2", "a@example.com", now.Add(time.Minute)) {
		t.Fatal("other form is independent")
	}
	if !th.allow("f1", "a@example.com", now.Add(10*time.Minute)) {
		t.Fatal("allowed again after the cooldown")
	}
}

func TestSendFormConfirmationEmail_RecipientCooldown(t *testing.T) {
	fake := newFakeConfirmationSender()
	s := newThrottleTestServer(fake)
	f := testForm(true, nil)
	data := map[string]interface{}{"work_email": "ada@example.com"}
	for i := 0; i < 3; i++ {
		s.sendFormConfirmationEmail(f, &form.FormSubmission{ID: uuid.New()}, data)
	}
	time.Sleep(100 * time.Millisecond)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.to) != 1 {
		t.Fatalf("sent %d confirmations to one recipient, want 1", len(fake.to))
	}
}

type gateSender struct {
	gate    chan struct{}
	started chan struct{}
	mu      sync.Mutex
	sent    int
}

func (g *gateSender) SendFormConfirmation(_ context.Context, _ string, _ email.FormConfirmationData) error {
	g.started <- struct{}{}
	<-g.gate
	g.mu.Lock()
	g.sent++
	g.mu.Unlock()
	return nil
}

func TestSendFormConfirmationEmail_ConcurrencyCapDropsExcess(t *testing.T) {
	g := &gateSender{gate: make(chan struct{}), started: make(chan struct{}, 16)}
	s := newThrottleTestServer(g)
	f := testForm(true, nil)
	for i := 0; i < confirmationMaxInFlight+3; i++ {
		data := map[string]interface{}{"work_email": "u" + uuid.NewString()[:8] + "@example.com"}
		s.sendFormConfirmationEmail(f, &form.FormSubmission{ID: uuid.New()}, data)
	}
	for i := 0; i < confirmationMaxInFlight; i++ {
		select {
		case <-g.started:
		case <-time.After(2 * time.Second):
			t.Fatal("expected sends did not start")
		}
	}
	close(g.gate)
	time.Sleep(100 * time.Millisecond)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sent != confirmationMaxInFlight {
		t.Fatalf("sent %d, want %d (rest dropped)", g.sent, confirmationMaxInFlight)
	}
	// Slots are released: a new send goes through.
	if !s.confirmThrottle.tryAcquire() {
		t.Fatal("slots not released")
	}
}

// The route is wired to a per-IP limiter: burst 5, then 429.
func TestPublicFormSubmit_RouteIsRateLimited(t *testing.T) {
	s := newThrottleTestServer(nil)
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r = r.Group("", func(next http.Handler) http.Handler { // formService is nil here; swallow the handler panic
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer func() {
				if recover() != nil {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, req)
		})
	})
	s.mountCorePublicCMSRoutes(r)
	codes := make([]int, 0, 7)
	for i := 0; i < 7; i++ {
		req := httptest.NewRequest(http.MethodPost, "/forms/contact/submit", strings.NewReader(`{}`))
		req.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	for i, c := range codes {
		want := http.StatusInternalServerError // reached the handler
		if i >= 5 {
			want = http.StatusTooManyRequests
		}
		if c != want {
			t.Fatalf("request %d = %d, want %d (all: %v)", i, c, want, codes)
		}
	}
}
