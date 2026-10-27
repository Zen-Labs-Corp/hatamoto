package backup

import (
	"errors"
	"testing"
	"time"
)

func TestTryLockExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("second TryLock = %v, want ErrBusy", err)
	}
	release()
	again, err := TryLock(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

func TestWaitLock(t *testing.T) {
	dir := t.TempDir()
	release, err := TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WaitLock(t.Context(), dir, 300*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("WaitLock timeout = %v, want ErrBusy", err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		release()
	}()
	got, err := WaitLock(t.Context(), dir, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitLock after release: %v", err)
	}
	got()
}
