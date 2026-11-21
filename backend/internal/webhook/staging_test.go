package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/runenv"
	"hatamoto.dev/sdk"
)

func TestStagingSuppressesWebhooks(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	reg := &fakeRegistrar{fns: map[string]func(context.Context, any){}, prio: map[string]int{}}
	q := &fakeQueue{}
	Register(reg, q, log)
	reg.fns["entity.created"](ctx, sdk.EntityEvent{ID: "e1", SchemaSlug: "post", Name: "Hello"})
	if len(q.events) != 0 {
		t.Fatalf("%s: enqueued %v in staging", runenv.Webhooks, q.events)
	}

	// A nil repo panics on any use, so returning means the repo was never touched.
	d := NewDeliverer(nil, log, Options{})
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if _, err := d.SendTest(ctx, "sub-1"); !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("SendTest err = %v, want ErrSuppressed", err)
	}
}
