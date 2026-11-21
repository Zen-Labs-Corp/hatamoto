package frameticket

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func tk(nonce string, exp time.Time, payload []byte) Ticket {
	return Ticket{Nonce: nonce, Purpose: "preview", Session: uuid.New(), Target: "e", Expiry: exp, Payload: payload}
}

func TestMemoryRoundTripOnce(t *testing.T) {
	m, ctx := &Memory{}, context.Background()
	in := tk("n1", t0.Add(time.Minute), []byte("body"))
	if err := m.Put(ctx, in, t0); err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.Take(ctx, "n1", t0)
	if err != nil || !ok || got.Session != in.Session || string(got.Payload) != "body" {
		t.Fatalf("first take: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := m.Take(ctx, "n1", t0); ok {
		t.Error("second take succeeded")
	}
}

func TestMemoryExpiredIsRemovedAndMiss(t *testing.T) {
	m, ctx := &Memory{}, context.Background()
	_ = m.Put(ctx, tk("n1", t0.Add(time.Minute), nil), t0)
	if _, ok, _ := m.Take(ctx, "n1", t0.Add(time.Minute+time.Second)); ok {
		t.Error("expired ticket accepted")
	}
	if m.Len() != 0 {
		t.Errorf("Len = %d, want 0", m.Len())
	}
}

func TestMemoryCountCap(t *testing.T) {
	m, ctx := &Memory{}, context.Background()
	for i := range MaxTickets + 10 {
		_ = m.Put(ctx, tk("n"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Millisecond+time.Minute), nil), t0)
	}
	if m.Len() > MaxTickets {
		t.Fatalf("Len = %d, cap %d", m.Len(), MaxTickets)
	}
	if _, ok, _ := m.Take(ctx, "n0", t0); ok {
		t.Error("the oldest survived")
	}
}

func TestMemoryByteCap(t *testing.T) {
	m, ctx := &Memory{}, context.Background()
	big := make([]byte, MaxBytes/2+1)
	_ = m.Put(ctx, tk("a", t0.Add(time.Minute), big), t0)
	_ = m.Put(ctx, tk("b", t0.Add(time.Minute+time.Millisecond), big), t0)
	if _, ok, _ := m.Take(ctx, "a", t0); ok {
		t.Error("the byte bound did not evict the oldest")
	}
	if _, ok, _ := m.Take(ctx, "b", t0); !ok {
		t.Error("the newest was evicted")
	}
}

func TestMemoryNilPayloadStaysNil(t *testing.T) {
	m, ctx := &Memory{}, context.Background()
	_ = m.Put(ctx, tk("n", t0.Add(time.Minute), nil), t0)
	got, ok, _ := m.Take(ctx, "n", t0)
	if !ok || got.Payload != nil {
		t.Fatalf("ok=%v payload=%#v", ok, got.Payload)
	}
}
