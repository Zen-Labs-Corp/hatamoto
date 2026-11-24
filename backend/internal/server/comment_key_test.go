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

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/webhook"
	"hatamoto.dev/sdk"
)

type captureQueue struct {
	mu   sync.Mutex
	data []string
}

func (q *captureQueue) Enqueue(_ context.Context, _ string, data any, _ time.Time) (int, error) {
	b, _ := jsonx.MarshalPinned(data)
	q.mu.Lock()
	q.data = append(q.data, string(b))
	q.mu.Unlock()
	return 1, nil
}

// TestCommentKeyNotOnWebhooks: the observer puts only the commenter id on the
// context of an event. An in-process action sees it (pluginhost derives each
// plugin's key from it per delivery); the shared event value stays keyless and
// a webhook delivery carries neither a key, the commenter id nor an email.
func TestCommentKeyNotOnWebhooks(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := hooks.New(log)
	q := &captureQueue{}
	webhook.Register(engine, q, log)

	var mu sync.Mutex
	var seen []uuid.UUID
	var events []sdk.CommentEvent
	for _, name := range []string{sdk.CommentCreated.Name, sdk.CommentApproved.Name, sdk.CommentSpam.Name} {
		engine.AddAction(name, 10, func(ctx context.Context, p any) {
			id, ok := pluginkey.CommenterFrom(ctx)
			mu.Lock()
			defer mu.Unlock()
			if ok {
				seen = append(seen, id)
			}
			events = append(events, p.(sdk.CommentEvent))
		})
	}
	o := newCommentEventObserver(engine)
	c := testComment(comment.StatusApproved)
	ctx := context.Background()
	o.OnCreated(ctx, c)
	o.OnApproved(ctx, c)
	o.OnSpam(ctx, c)
	engine.Wait()

	if len(seen) != 3 || seen[0] != *c.CommenterID {
		t.Errorf("actions saw commenters %v, want 3 x %s", seen, c.CommenterID)
	}
	for _, ev := range events {
		if ev.CommenterKey != "" {
			t.Errorf("the shared event carries a key: %+v", ev)
		}
	}
	if ev := commentEvent(c); ev.CommenterKey != "" {
		t.Errorf("commentEvent set a key: %q", ev.CommenterKey)
	}
	if len(q.data) != 3 {
		t.Fatalf("webhook deliveries = %d", len(q.data))
	}
	key := pluginkey.New("test-secret").Commenter("plugin-a", *c.CommenterID)
	for _, d := range q.data {
		for _, bad := range []string{"commenter_key", c.CommenterID.String(), key, "@"} {
			if strings.Contains(d, bad) {
				t.Errorf("webhook payload contains %q: %s", bad, d)
			}
		}
	}

	// A staff comment has no commenter: nothing is put on the context.
	seen = nil
	staff := c
	staff.CommenterID = nil
	o.OnApproved(ctx, staff)
	engine.Wait()
	if len(seen) != 0 {
		t.Errorf("a staff comment carried a commenter: %v", seen)
	}
}

// TestRenderPageResolverLazy drives the real render.page path: a filter that
// never asks for the commenter costs no identity lookup; one that asks does so
// once, and not at all without an hm_c cookie.
func TestRenderPageResolverLazy(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name   string
		cookie bool
		ask    bool
		calls  int
		want   bool
	}{
		{"filter does not ask", true, false, 0, false},
		{"no cookie", false, true, 0, false},
		{"cookie and ask", true, true, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := &Server{}
			s.commentPost.device = func(context.Context, string) (uuid.UUID, bool) {
				calls++
				return id, true
			}
			engine := hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
			var got uuid.UUID
			var ok bool
			hooks.OnFilter(engine.For("spy"), sdk.RenderPage, 10, func(ctx context.Context, in sdk.PageContext) (sdk.PageContext, error) {
				if tc.ask {
					got, ok = pluginkey.CommenterFrom(ctx)
					pluginkey.CommenterFrom(ctx)
				}
				return in, nil
			})
			h := &renderHandlers{pageFilter: pluginhost.NewPageFilter(nil, engine), commentDevice: s.commentDevice}
			r := httptest.NewRequest("GET", "/s", nil)
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "hm_c", Value: "v"})
			}
			h.applyPageFilter(r, "/s", &render.PageView{Entity: &render.EntityView{ID: "e1", Slug: "s", Title: "T", Schema: "story"}})
			if ok != tc.want || (ok && got != id) || calls != tc.calls {
				t.Errorf("commenter %v %v after %d lookups, want %v after %d", got, ok, calls, tc.want, tc.calls)
			}
		})
	}
}
