package comment

import (
	"context"
	"testing"
)

var _ Observer = NopObserver{}

type panicObserver struct{ NopObserver }

func (panicObserver) OnApproved(context.Context, Comment) { panic("boom") }

type countObserver struct {
	NopObserver
	approved int
}

func (c *countObserver) OnApproved(context.Context, Comment) { c.approved++ }

func TestObserverPanicIsContained(t *testing.T) {
	t.Parallel()
	var o observers
	second := &countObserver{}
	o.add(panicObserver{})
	o.add(second)
	o.each("approved", func(x Observer) { x.OnApproved(context.Background(), Comment{}) })
	if second.approved != 1 {
		t.Errorf("second observer called %d times, want 1", second.approved)
	}
}

func TestObserverOrder(t *testing.T) {
	t.Parallel()
	var o observers
	var seen []int
	for i := 0; i < 3; i++ {
		i := i
		o.add(orderObserver{fn: func() { seen = append(seen, i) }})
	}
	o.each("created", func(x Observer) { x.OnCreated(context.Background(), Comment{}) })
	if len(seen) != 3 || seen[0] != 0 || seen[1] != 1 || seen[2] != 2 {
		t.Errorf("order = %v", seen)
	}
}

type orderObserver struct {
	NopObserver
	fn func()
}

func (o orderObserver) OnCreated(context.Context, Comment) { o.fn() }
