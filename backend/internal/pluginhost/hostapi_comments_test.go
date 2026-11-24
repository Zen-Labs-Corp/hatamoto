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
	thread  sdk.CommentThread
	counts  map[uuid.UUID]int
	gotPage int
	gotOpts ThreadOpts
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
