package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHashPassword_BoundedConcurrency(t *testing.T) {
	// Occupy every slot, as if maxConcurrentHashes hashes were running.
	for range maxConcurrentHashes {
		hashSlots <- struct{}{}
	}
	done := make(chan struct{})
	go func() {
		if _, err := HashPassword("correct horse"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("hash N+1 ran while all slots were busy")
	case <-time.After(150 * time.Millisecond):
	}
	<-hashSlots // one finishes: the waiting hash may proceed
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("waiting hash never ran after a slot freed")
	}
	for range maxConcurrentHashes - 1 {
		<-hashSlots
	}
}

func BenchmarkHashPassword(b *testing.B) {
	for b.Loop() {
		if _, err := HashPassword("correct horse"); err != nil {
			b.Fatal(err)
		}
	}
}

// A waiter whose request context is cancelled returns promptly and never hashes.
func TestHashPassword_CancelledWaiterReturnsPromptly(t *testing.T) {
	enc, err := HashPassword("correct horse") // before the slots are taken
	if err != nil {
		t.Fatal(err)
	}
	for range maxConcurrentHashes {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range maxConcurrentHashes {
			<-hashSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		ok  bool
		err error
	}
	verify := make(chan result, 1)
	hash := make(chan error, 1)
	go func() {
		ok, err := VerifyPasswordContext(ctx, "correct horse", enc)
		verify <- result{ok, err}
	}()
	go func() {
		_, err := HashPasswordContext(ctx, "correct horse")
		hash <- err
	}()
	select {
	case <-verify:
		t.Fatal("verify returned while it should still be queued")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case r := <-verify:
		if r.ok || !errors.Is(r.err, context.Canceled) {
			t.Errorf("verify = %v, %v", r.ok, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled verify still waiting")
	}
	select {
	case err := <-hash:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("hash err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled hash still waiting")
	}
	// Already-cancelled context never takes a slot even when one is free.
	<-hashSlots
	if _, err := HashPasswordContext(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("pre-cancelled hash err = %v", err)
	}
	hashSlots <- struct{}{}
}
