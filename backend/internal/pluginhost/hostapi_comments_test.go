package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

type fakeCommentReader struct {
	thread     sdk.CommentThread
	counts     map[uuid.UUID]int
	byAuthor   sdk.CommentThread
	subtree    sdk.CommentSubtree
	subtreeErr error
	gotPage    int
	gotOpts    ThreadOpts
	gotKey     string
	gotID      uuid.UUID
	calls      int
}

func (f *fakeCommentReader) ByAuthor(_ context.Context, key string, page int, opts ThreadOpts) (sdk.CommentThread, error) {
	f.calls++
	f.gotKey, f.gotPage, f.gotOpts = key, page, opts
	return f.byAuthor, nil
}

func (f *fakeCommentReader) Subtree(_ context.Context, id uuid.UUID, page int, opts ThreadOpts) (sdk.CommentSubtree, error) {
	f.calls++
	f.gotID, f.gotPage, f.gotOpts = id, page, opts
	return f.subtree, f.subtreeErr
}

func (f *fakeCommentReader) Thread(_ context.Context, _ uuid.UUID, page int, opts ThreadOpts) (sdk.CommentThread, error) {
	f.gotPage, f.gotOpts = page, opts
	return f.thread, nil
}

func (f *fakeCommentReader) Counts(context.Context, []uuid.UUID) (map[uuid.UUID]int, error) {
	return f.counts, nil
}

// newCommentsRig is newCapaRig with a comment reader (nil: unavailable).
func newCommentsRig(t *testing.T, reader CommentReader) *capaRig {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	svc := entity.NewService(&memSchemas{}, &memEntities{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	services := Services{Entities: svc}
	if reader != nil {
		services.Comments = reader
	}
	rt.Configure(Config{Services: services, Scheduler: &fakeScheduler{}})
	return &capaRig{rt: rt, engine: engine, svc: svc, logs: logs}
}

func (r *capaRig) loadComments(t *testing.T) {
	t.Helper()
	r.load(t, func(m *sdk.Manifest) { m.Permissions = append(m.Permissions, sdk.PermCommentsRead) })
}

func TestCommentsHostDeniedUntilGranted(t *testing.T) {
	r := newCommentsRig(t, &fakeCommentReader{})
	r.loadComments(t)
	id := uuid.NewString()
	for fn, args := range map[string]any{
		sdk.FnCommentsThread: map[string]any{"entity_id": id},
		sdk.FnCommentsCounts: map[string]any{"entity_ids": []string{id}},
	} {
		if o := r.do(t, fn, args); o.Code != sdk.ErrCapabilityDenied || o.Cap != sdk.PermCommentsRead {
			t.Errorf("%s: %+v, want capability_denied for comments.read", fn, o)
		}
	}
}

func TestCommentsHostThread(t *testing.T) {
	id := uuid.New()
	fake := &fakeCommentReader{thread: sdk.CommentThread{
		Items: []sdk.CommentItem{{ID: "c1", AuthorName: "A", BodyHTML: "<p>x</p>", CreatedAt: time.Unix(0, 0).UTC()}},
		Page:  1, HasMore: true,
	}}
	r := newCommentsRig(t, fake)
	r.loadComments(t)
	r.grant(t, sdk.PermCommentsRead)

	o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id.String()})
	if o.Code != "" {
		t.Fatalf("thread: %+v", o)
	}
	var top map[string]jsontext.Value
	if err := jsonx.UnmarshalPinned(o.Result, &top); err != nil {
		t.Fatal(err)
	}
	for k := range top {
		if k != "items" && k != "page" && k != "has_more" {
			t.Errorf("unexpected key %q", k)
		}
	}
	var items []map[string]any
	if err := jsonx.UnmarshalPinned(top["items"], &items); err != nil || len(items) != 1 {
		t.Fatalf("items %s: %v", top["items"], err)
	}
	for k := range items[0] {
		switch k {
		case "id", "parent_id", "depth", "author_name", "staff", "body_html", "created_at":
		default:
			t.Errorf("item carries %q", k)
		}
	}
	for _, bad := range []string{"status", "email", "hash", "ip", "user_agent"} {
		if strings.Contains(string(o.Result), bad) {
			t.Errorf("result contains %q: %s", bad, o.Result)
		}
	}

	for page, want := range map[int]int{0: 1, 3: 3, 1 << 30: maxCommentPage} {
		r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id.String(), "page": page})
		if fake.gotPage != want {
			t.Errorf("page %d reached the reader as %d, want %d", page, fake.gotPage, want)
		}
	}
	if fake.gotOpts.Plugin == "" {
		t.Error("the calling plugin's name did not reach the reader")
	}
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": "nope"}); o.Code != sdk.ErrInvalidArgument {
		t.Errorf("bad id: %+v", o)
	}
}

func TestCommentsHostThreadIncludeDead(t *testing.T) {
	id := uuid.NewString()
	fake := &fakeCommentReader{}
	r := newCommentsRig(t, fake)
	r.load(t, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermCommentsRead, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
		m.EntitySchema = "story"
	})
	r.grant(t, sdk.PermCommentsRead)
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id, "include_dead": true}); o.Code != "" || fake.gotOpts.IncludeDead {
		t.Fatalf("without comments.moderate: %+v opts %+v, want include_dead dropped", o, fake.gotOpts)
	}
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id, "include_dead": true}); o.Code != "" || !fake.gotOpts.IncludeDead || fake.gotOpts.Plugin == "" {
		t.Fatalf("with comments.moderate: %+v opts %+v, want include_dead passed", o, fake.gotOpts)
	}
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id}); o.Code != "" || fake.gotOpts.IncludeDead {
		t.Fatalf("not asked: %+v opts %+v", o, fake.gotOpts)
	}
}

func TestCommentsHostCounts(t *testing.T) {
	id := uuid.New()
	r := newCommentsRig(t, &fakeCommentReader{counts: map[uuid.UUID]int{id: 3}})
	r.loadComments(t)
	r.grant(t, sdk.PermCommentsRead)

	many := make([]string, maxCommentCountIDs+1)
	for i := range many {
		many[i] = uuid.NewString()
	}
	for name, ids := range map[string][]string{"empty": {}, "too many": many, "bad uuid": {"nope"}} {
		if o := r.do(t, sdk.FnCommentsCounts, map[string]any{"entity_ids": ids}); o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %+v, want invalid_argument", name, o)
		}
	}
	o := r.do(t, sdk.FnCommentsCounts, map[string]any{"entity_ids": []string{id.String()}})
	if want := `{"counts":{"` + id.String() + `":3}}`; o.Code != "" || string(o.Result) != want {
		t.Fatalf("counts: %+v, want %s", o, want)
	}
}

func TestCommentsHostUnavailable(t *testing.T) {
	r := newCommentsRig(t, nil)
	r.loadComments(t)
	r.grant(t, sdk.PermCommentsRead)
	id := uuid.NewString()
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": id}); o.Code != sdk.ErrUnavailable {
		t.Errorf("thread: %+v", o)
	}
	if o := r.do(t, sdk.FnCommentsCounts, map[string]any{"entity_ids": []string{id}}); o.Code != sdk.ErrUnavailable {
		t.Errorf("counts: %+v", o)
	}
}

func TestCommentsHostByAuthor(t *testing.T) {
	fake := &fakeCommentReader{byAuthor: sdk.CommentThread{
		Items: []sdk.CommentItem{{ID: "c1", CommenterKey: "k", Status: "approved"}}, Page: 1, HasMore: true,
	}}
	r := newCommentsRig(t, fake)
	r.load(t, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermCommentsRead, sdk.PermEntitiesWriteOwn)
		m.EntitySchema = "item"
	})
	args := map[string]any{"commenter_key": "key"}
	if o := r.do(t, sdk.FnCommentsByAuthor, args); o.Code != sdk.ErrCapabilityDenied || o.Cap != sdk.PermCommentsRead {
		t.Fatalf("before grant: %+v", o)
	}
	r.grant(t, sdk.PermCommentsRead)
	o := r.do(t, sdk.FnCommentsByAuthor, args)
	if o.Code != "" || !strings.Contains(string(o.Result), `"commenter_key":"k"`) || !strings.Contains(string(o.Result), `"has_more":true`) {
		t.Fatalf("by_author: %+v", o)
	}
	if fake.gotKey != "key" || fake.gotOpts.Plugin != "capa" || fake.gotOpts.Schema != "item" {
		t.Errorf("reader saw key %q opts %+v", fake.gotKey, fake.gotOpts)
	}
	for page, want := range map[int]int{0: 1, -5: 1, 10001: maxCommentPage, 3: 3} {
		r.do(t, sdk.FnCommentsByAuthor, map[string]any{"commenter_key": "key", "page": page})
		if fake.gotPage != want {
			t.Errorf("page %d reached the reader as %d, want %d", page, fake.gotPage, want)
		}
	}
	calls := fake.calls
	for name, a := range map[string]any{
		"empty key":   map[string]any{"commenter_key": ""},
		"long key":    map[string]any{"commenter_key": strings.Repeat("k", maxCommenterKeyBytes+1)},
		"unknown arg": map[string]any{"commenter_key": "key", "bogus": 1},
	} {
		if o := r.do(t, sdk.FnCommentsByAuthor, a); o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %+v, want invalid_argument", name, o)
		}
	}
	if fake.calls != calls {
		t.Error("an invalid call reached the reader")
	}

	none := newCommentsRig(t, nil)
	none.loadComments(t)
	none.grant(t, sdk.PermCommentsRead)
	if o := none.do(t, sdk.FnCommentsByAuthor, args); o.Code != sdk.ErrUnavailable {
		t.Errorf("nil reader: %+v", o)
	}
}

func TestCommentsHostSubtree(t *testing.T) {
	id := uuid.New()
	fake := &fakeCommentReader{subtree: sdk.CommentSubtree{
		Ancestors: []sdk.CommentItem{{ID: "r"}}, Items: []sdk.CommentItem{{ID: "c1"}}, Page: 1,
	}}
	r := newCommentsRig(t, fake)
	r.loadComments(t)
	args := map[string]any{"comment_id": id.String()}
	if o := r.do(t, sdk.FnCommentsSubtree, args); o.Code != sdk.ErrCapabilityDenied || o.Cap != sdk.PermCommentsRead {
		t.Fatalf("before grant: %+v", o)
	}
	r.grant(t, sdk.PermCommentsRead)
	if o := r.do(t, sdk.FnCommentsSubtree, map[string]any{"comment_id": "nope"}); o.Code != sdk.ErrInvalidArgument {
		t.Errorf("bad id: %+v", o)
	}
	o := r.do(t, sdk.FnCommentsSubtree, map[string]any{"comment_id": id.String(), "page": 99999})
	if o.Code != "" || !strings.Contains(string(o.Result), `"ancestors":[`) || fake.gotID != id || fake.gotPage != maxCommentPage {
		t.Fatalf("subtree: %+v id %s page %d", o, fake.gotID, fake.gotPage)
	}
	fake.subtreeErr = ErrCommentNotFound
	if o := r.do(t, sdk.FnCommentsSubtree, args); o.Code != sdk.ErrNotFound || o.Msg != "comment not found" {
		t.Errorf("not found: %+v", o)
	}

	none := newCommentsRig(t, nil)
	none.loadComments(t)
	none.grant(t, sdk.PermCommentsRead)
	if o := none.do(t, sdk.FnCommentsSubtree, args); o.Code != sdk.ErrUnavailable {
		t.Errorf("nil reader: %+v", o)
	}
}

func TestCommentsHostReadsIncludeDeadNeedsModerate(t *testing.T) {
	fake := &fakeCommentReader{}
	r := newCommentsRig(t, fake)
	r.load(t, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermCommentsRead, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
		m.EntitySchema = "item"
	})
	r.grant(t, sdk.PermCommentsRead)
	calls := map[string]func(dead any) map[string]any{
		sdk.FnCommentsByAuthor: func(d any) map[string]any { return map[string]any{"commenter_key": "key", "include_dead": d} },
		sdk.FnCommentsSubtree:  func(d any) map[string]any { return map[string]any{"comment_id": uuid.NewString(), "include_dead": d} },
	}
	for fn, mk := range calls {
		if o := r.do(t, fn, mk(true)); o.Code != "" || fake.gotOpts.IncludeDead {
			t.Fatalf("%s without comments.moderate: %+v opts %+v", fn, o, fake.gotOpts)
		}
	}
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	for fn, mk := range calls {
		if o := r.do(t, fn, mk(true)); o.Code != "" || !fake.gotOpts.IncludeDead || fake.gotOpts.Plugin != "capa" || fake.gotOpts.Schema != "item" {
			t.Fatalf("%s with comments.moderate: %+v opts %+v", fn, o, fake.gotOpts)
		}
		if o := r.do(t, fn, mk(false)); o.Code != "" || fake.gotOpts.IncludeDead {
			t.Fatalf("%s not asked: %+v opts %+v", fn, o, fake.gotOpts)
		}
	}
}

func TestCommentsHostReadLimiter(t *testing.T) {
	r := newCommentsRig(t, &fakeCommentReader{})
	r.loadComments(t)
	r.grant(t, sdk.PermCommentsRead)
	api := r.rt.caps.api
	if l := api.limits("capa").reads; l.Burst() != 60 || l.Limit() != 30 {
		t.Fatalf("read limiter = %v / %d, want 30 / 60", l.Limit(), l.Burst())
	}
	reads := api.limits("capa").reads
	reads.AllowN(time.Now(), 60) // drain the bucket
	reads.SetLimit(0)            // and stop the refill, so a slow runner cannot get a token back
	by := map[string]any{"commenter_key": "key"}
	sub := map[string]any{"comment_id": uuid.NewString()}
	if o := r.do(t, sdk.FnCommentsByAuthor, by); o.Code != sdk.ErrRateLimited || o.Msg != "comments.by_author: 1800 per minute" {
		t.Fatalf("by_author: %+v", o)
	}
	if o := r.do(t, sdk.FnCommentsSubtree, sub); o.Code != sdk.ErrRateLimited {
		t.Fatalf("subtree shares the bucket: %+v", o)
	}
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": uuid.NewString()}); o.Code != "" {
		t.Fatalf("thread is never limited: %+v", o)
	}
	if l := api.limits("other").reads; !l.Allow() {
		t.Fatal("another plugin shares the bucket")
	}
}
