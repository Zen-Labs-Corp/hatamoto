package entity

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/testutil"
)

func TestFanoutSinkDeliversToEverySink(t *testing.T) {
	t.Parallel()
	a, b := &recordingSink{}, &recordingSink{}
	FanoutSink{a, nil, b}.Emit(context.Background(), Event{Kind: EventCreated})
	testutil.Equal(t, []EventKind{EventCreated}, a.kinds())
	testutil.Equal(t, []EventKind{EventCreated}, b.kinds())
}

func TestService_EventsCarryPrevStatusAndDeletedCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sr, er := newTestService()
	existing := seedEntity(t, er, seedSchema(t, sr))
	sink := &recordingSink{}
	svc.SetEvents(sink)

	_, err := svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusPublished)
	testutil.RequireNoError(t, err)
	_, err = svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusArchived)
	testutil.RequireNoError(t, err)
	testutil.RequireNoError(t, svc.DeleteEntity(ctx, existing.ID))

	testutil.RequireEqual(t, 3, len(sink.events))
	testutil.Equal(t, string(StatusDraft), sink.events[0].PrevStatus)
	testutil.Equal(t, string(StatusPublished), sink.events[1].PrevStatus)
	del := sink.events[2]
	testutil.Equal(t, EventDeleted, del.Kind)
	if del.Entity == nil || del.Entity.ID != existing.ID || del.Entity.Schema == nil {
		t.Fatalf("deleted event lacks the pre-read copy: %+v", del.Entity)
	}
}
