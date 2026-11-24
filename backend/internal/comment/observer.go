package comment

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Observer receives committed changes. Calls run synchronously after commit,
// in registration order, and must return quickly. A panic is recovered and
// logged: an observer can never fail or undo the write.
type Observer interface {
	OnCreated(ctx context.Context, c Comment)
	OnConfirmed(ctx context.Context, c Comment)
	OnApproved(ctx context.Context, c Comment)
	OnSpam(ctx context.Context, c Comment)
	OnDeleted(ctx context.Context, c Comment) // trash and hard delete
	// OnUpdated fires for a change the other events do not cover: an edit, a
	// move to pending (an unapproval), and a move to or from dead. before is the
	// comment as it was.
	OnUpdated(ctx context.Context, before, after Comment)
	// OnBatch fires once after a batch write (plugin dead, restore, rank) with
	// the distinct entities it touched. The per-comment events of that batch ran
	// with a WithBatch context, so an observer may skip per-comment work and do
	// it here.
	OnBatch(ctx context.Context, entityIDs []uuid.UUID)
	OnSettingsChanged(ctx context.Context, s Settings)
	// OnModeChanged fires when a comment mode changed: one entity's override
	// (entityID), or a type default (uuid.Nil: any entity of the type may change).
	OnModeChanged(ctx context.Context, entityID uuid.UUID)
}

// NopObserver implements Observer with no-ops; embed it to handle a subset.
type NopObserver struct{}

// OnCreated does nothing.
func (NopObserver) OnCreated(context.Context, Comment) {}

// OnConfirmed does nothing.
func (NopObserver) OnConfirmed(context.Context, Comment) {}

// OnApproved does nothing.
func (NopObserver) OnApproved(context.Context, Comment) {}

// OnSpam does nothing.
func (NopObserver) OnSpam(context.Context, Comment) {}

// OnDeleted does nothing.
func (NopObserver) OnDeleted(context.Context, Comment) {}

// OnUpdated does nothing.
func (NopObserver) OnUpdated(context.Context, Comment, Comment) {}

// OnBatch does nothing.
func (NopObserver) OnBatch(context.Context, []uuid.UUID) {}

// OnSettingsChanged does nothing.
func (NopObserver) OnSettingsChanged(context.Context, Settings) {}

// OnModeChanged does nothing.
func (NopObserver) OnModeChanged(context.Context, uuid.UUID) {}

type batchKey struct{}

// WithBatch marks ctx as part of a batch write: observers see InBatch true on
// the per-comment events and get one OnBatch at the end.
func WithBatch(ctx context.Context) context.Context { return context.WithValue(ctx, batchKey{}, true) }

// InBatch reports whether ctx came from WithBatch.
func InBatch(ctx context.Context) bool { v, _ := ctx.Value(batchKey{}).(bool); return v }

type observers struct {
	mu   sync.RWMutex
	list []Observer
	log  *slog.Logger
}

func (o *observers) add(x Observer) {
	o.mu.Lock()
	o.list = append(o.list, x)
	o.mu.Unlock()
}

// each calls fn for every observer in registration order, recovering a panic
// per observer and logging its name and value.
func (o *observers) each(name string, fn func(Observer)) {
	o.mu.RLock()
	list := append([]Observer(nil), o.list...)
	o.mu.RUnlock()
	for _, x := range list {
		func() {
			defer func() {
				if r := recover(); r != nil {
					l := o.log
					if l == nil {
						l = slog.Default()
					}
					l.Error("comment observer panicked", "event", name, "observer", fmt.Sprintf("%T", x), "panic", fmt.Sprint(r))
				}
			}()
			fn(x)
		}()
	}
}
