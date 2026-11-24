package pluginhost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

const draftSecret = "test-secret"

// configureKeys turns commenter keys on and answers the plugins' kv.set calls
// with a recording stub, so a test can see what a plugin was sent. Call it
// before loading plugins.
func (r *commentRig) configureKeys() *kvRecorder {
	r.rt.Configure(Config{CommenterKey: pluginkey.New(draftSecret).Commenter})
	rec := &kvRecorder{m: map[string]string{}}
	r.rt.host.SetHostCall(rec.call)
	return rec
}

// kvRecorder stores kv.set values as "<plugin>/<key>".
type kvRecorder struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kvRecorder) call(_ context.Context, plugin string, in []byte) []byte {
	var req sdk.CallRequest
	if err := jsonx.UnmarshalPinned(in, &req); err == nil && req.Fn == sdk.FnKVSet {
		var a struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := jsonx.UnmarshalPinned(req.Args, &a); err == nil {
			k.mu.Lock()
			k.m[plugin+"/"+a.Key] = a.Value
			k.mu.Unlock()
		}
	}
	out, _ := jsonx.MarshalPinned(sdk.CallResponse{OK: true, Result: []byte("null")})
	return out
}

func (k *kvRecorder) get(plugin, key string) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[plugin+"/"+key]
	return v, ok
}

// draft runs the chain the way commentsubmit.HookDrafter does.
func (r *commentRig) draft(ctx context.Context) (sdk.CommentDraft, error) {
	out, err := r.engine.ApplyClosed(ctx, sdk.CommentDraftF.Name, sdk.CommentDraft{Schema: "story"})
	if err != nil {
		return sdk.CommentDraft{}, err
	}
	return out.(sdk.CommentDraft), nil
}

func TestCommentDraftNotRegistered(t *testing.T) {
	r := newCommentRig(t)
	r.configureKeys()
	r.load(t, "draft-name", false)
	if n := r.engine.FilterCount(sdk.CommentDraftF.Name); n != 0 {
		t.Fatalf("not granted: %d filters", n)
	}
	r.grant(t, sdk.PermCommentsRead)
	if n := r.engine.FilterCount(sdk.CommentDraftF.Name); n != 1 {
		t.Fatalf("granted: %d filters", n)
	}
}

func TestCommentDraftName(t *testing.T) {
	r := newCommentRig(t)
	r.configureKeys()
	r.load(t, "draft-name", true)
	id := uuid.New()
	got, err := r.draft(pluginkey.WithCommenter(context.Background(), id))
	want := "name-" + pluginkey.New(draftSecret).Commenter("commentcheck", id)[:6]
	if err != nil || got.AuthorName != want || got.CommenterKey != "" || got.Reject != "" || got.Schema != "story" {
		t.Fatalf("got %+v %v, want name %q with no key or reject", got, err, want)
	}
}

func TestCommentDraftPayload(t *testing.T) {
	r := newCommentRig(t)
	r.configureKeys()
	r.load(t, "draft-echo", true)
	got, err := r.draft(pluginkey.WithCommenter(context.Background(), uuid.New()))
	if err != nil || got.AuthorName != "schema=story;name=;reject=" {
		t.Fatalf("the plugin saw %q (%v): want schema story and empty author_name and reject", got.AuthorName, err)
	}
}

func TestCommentDraftReject(t *testing.T) {
	ctx := pluginkey.WithCommenter(context.Background(), uuid.New())
	t.Run("public message", func(t *testing.T) {
		r := newCommentRig(t)
		r.configureKeys()
		r.load(t, "draft-reject", true)
		_, err := r.draft(ctx)
		var rej *sdk.Reject
		if !errors.As(err, &rej) || rej.Reason != "Pick a name first" {
			t.Fatalf("err = %v, want *sdk.Reject with the plugin's message", err)
		}
	})
	t.Run("cleaned and cut", func(t *testing.T) {
		r := newCommentRig(t)
		r.configureKeys()
		r.load(t, "draft-reject-long", true)
		_, err := r.draft(ctx)
		var rej *sdk.Reject
		if !errors.As(err, &rej) {
			t.Fatalf("err = %v, want *sdk.Reject", err)
		}
		if len(rej.Reason) > sdk.CommentDraftMaxReject || len(rej.Reason) < sdk.CommentDraftMaxReject-1 || !utf8.ValidString(rej.Reason) || strings.ContainsRune(rej.Reason, '\a') {
			t.Errorf("reason: %d bytes, valid=%v, control=%v", len(rej.Reason), utf8.ValidString(rej.Reason), strings.ContainsRune(rej.Reason, '\a'))
		}
	})
}

func TestCommentDraftFailures(t *testing.T) {
	for _, mode := range []string{"draft-reject-err", "draft-empty", "draft-sleep"} {
		t.Run(mode, func(t *testing.T) {
			r := newCommentRig(t)
			r.configureKeys()
			r.load(t, mode, true)
			_, err := r.draft(pluginkey.WithCommenter(context.Background(), uuid.New()))
			if err == nil {
				t.Fatal("the chain must fail")
			}
			var rej *sdk.Reject
			if errors.As(err, &rej) {
				t.Errorf("%v must not be a public *sdk.Reject", err)
			}
		})
	}
	t.Run("no commenter", func(t *testing.T) {
		r := newCommentRig(t)
		r.configureKeys()
		r.load(t, "draft-name", true)
		if _, err := r.draft(context.Background()); err == nil {
			t.Fatal("a draft without a commenter must fail")
		}
	})
}

func TestCommentDraftPerPluginKey(t *testing.T) {
	r := newCommentRig(t)
	r.configureKeys()
	r.load(t, "draft-name", true)
	r.loadAs(t, "commentcheck2", "draft-name", true)
	id := uuid.New()
	// The chain runs the plugins in name order; the last name wins. Each plugin
	// was sent its own key: the second's name derives from its own, not the first's.
	got, err := r.draft(pluginkey.WithCommenter(context.Background(), id))
	d := pluginkey.New(draftSecret)
	k1, k2 := d.Commenter("commentcheck", id), d.Commenter("commentcheck2", id)
	if k1[:6] == k2[:6] {
		t.Fatal("the two keys must differ")
	}
	if err != nil || got.AuthorName != "name-"+k2[:6] {
		t.Fatalf("got %+v %v, want the second plugin's name %q", got, err, "name-"+k2[:6])
	}
}

func TestCommentEventKeyPerPlugin(t *testing.T) {
	r := newCommentRig(t)
	rec := r.configureKeys()
	r.load(t, "empty", true)
	r.loadAs(t, "commentcheck2", "empty", true)
	r.loadAs(t, "commentcheck3", "empty", false) // not granted
	id := uuid.New()
	d := pluginkey.New(draftSecret)
	ctx := pluginkey.WithCommenter(context.Background(), id)
	ev := sdk.CommentEvent{ID: "c1", EntityID: "e1", AuthorName: "A", BodyHTML: "<p>x</p>", Status: "approved"}

	for _, h := range []string{sdk.CommentCreated.Name, sdk.CommentApproved.Name, sdk.CommentUpdated.Name, sdk.CommentDeleted.Name} {
		r.engine.Fire(ctx, h, ev)
	}
	r.engine.Wait()
	if ev.CommenterKey != "" {
		t.Fatalf("the shared event value carries key %q", ev.CommenterKey)
	}
	for _, plugin := range []string{"commentcheck", "commentcheck2"} {
		want := d.Commenter(plugin, id)
		for _, key := range []string{"k:c1", sdk.CommentCreated.Name + ":c1", sdk.CommentUpdated.Name + ":c1"} {
			if got, ok := rec.get(plugin, key); !ok || got != want {
				t.Errorf("%s %s = %q (delivered %v), want %q", plugin, key, got, ok, want)
			}
		}
		if got, ok := rec.get(plugin, sdk.CommentDeleted.Name+":c1"); !ok || got != "" {
			t.Errorf("%s comment.deleted = %q (delivered %v), want delivered without a key", plugin, got, ok)
		}
	}
	if a, b := d.Commenter("commentcheck", id), d.Commenter("commentcheck2", id); a == b {
		t.Fatal("keys must differ per plugin")
	}
	for k := range rec.m {
		if strings.HasPrefix(k, "commentcheck3/") {
			t.Errorf("ungranted plugin received %s", k)
		}
	}

	// No commenter in the context: delivered, keyless.
	r.engine.Fire(context.Background(), sdk.CommentUpdated.Name, sdk.CommentEvent{ID: "c2"})
	r.engine.Wait()
	if got, ok := rec.get("commentcheck", sdk.CommentUpdated.Name+":c2"); !ok || got != "" {
		t.Errorf("no commenter: %q (delivered %v)", got, ok)
	}
}
