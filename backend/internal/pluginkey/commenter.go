package pluginkey

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

type commenterCtxKey struct{}

// commenterVal holds either a known commenter or a resolver that runs at most
// once, and only when a key is asked for.
type commenterVal struct {
	once    sync.Once
	resolve func(context.Context) (uuid.UUID, bool)
	id      uuid.UUID
	ok      bool
}

// WithCommenter returns ctx carrying a known commenter (events, pipeline).
func WithCommenter(ctx context.Context, id uuid.UUID) context.Context {
	v := &commenterVal{id: id, ok: id != uuid.Nil}
	v.once.Do(func() {})
	return context.WithValue(ctx, commenterCtxKey{}, v)
}

// WithCommenterResolver returns ctx carrying a lazy lookup (render.page,
// public routes): f runs at most once, only when CommenterFrom is called.
func WithCommenterResolver(ctx context.Context, f func(context.Context) (uuid.UUID, bool)) context.Context {
	return context.WithValue(ctx, commenterCtxKey{}, &commenterVal{resolve: f})
}

// CommenterFrom returns the commenter carried by ctx, running a resolver once.
// No carrier, a nil resolver or uuid.Nil answer false.
func CommenterFrom(ctx context.Context) (uuid.UUID, bool) {
	v, _ := ctx.Value(commenterCtxKey{}).(*commenterVal)
	if v == nil {
		return uuid.Nil, false
	}
	v.once.Do(func() {
		if v.resolve == nil {
			return
		}
		if id, ok := v.resolve(ctx); ok && id != uuid.Nil {
			v.id, v.ok = id, true
		}
	})
	return v.id, v.ok
}
