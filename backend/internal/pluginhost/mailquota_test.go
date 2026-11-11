package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMailQuotaStoreDefaultSetForget(t *testing.T) {
	s := NewMailQuotaStore(nil)
	if got := s.MailPerHour("p"); got != DefaultMailPerHour {
		t.Fatalf("default = %d, want %d", got, DefaultMailPerHour)
	}
	if err := s.SetMailPerHour(context.Background(), "p", 3600); err != nil {
		t.Fatal(err)
	}
	if got := s.MailPerHour("p"); got != 3600 {
		t.Fatalf("after set = %d", got)
	}
	if got := s.MailPerHour("other"); got != DefaultMailPerHour {
		t.Fatalf("other plugin = %d", got)
	}
	s.Forget("p")
	if got := s.MailPerHour("p"); got != DefaultMailPerHour {
		t.Fatalf("after forget = %d", got)
	}
}

func TestMailQuotaStoreRange(t *testing.T) {
	s := NewMailQuotaStore(nil)
	for _, n := range []int{0, 3601, -1} {
		err := s.SetMailPerHour(context.Background(), "p", n)
		if !errors.Is(err, ErrMailQuotaRange) {
			t.Errorf("%d: err = %v, want ErrMailQuotaRange", n, err)
		}
	}
	if got := s.MailPerHour("p"); got != DefaultMailPerHour {
		t.Fatalf("refused value stored: %d", got)
	}
	for _, n := range []int{1, 3600} {
		if err := s.SetMailPerHour(context.Background(), "p", n); err != nil {
			t.Errorf("%d: %v", n, err)
		}
	}
}

func TestMailQuotaStoreConcurrent(t *testing.T) {
	s := NewMailQuotaStore(nil)
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("p%d", i%3)
			_ = s.SetMailPerHour(context.Background(), name, i)
			_ = s.MailPerHour(name)
			s.Forget(name)
		}()
	}
	wg.Wait()
}

func TestValidMailPerHour(t *testing.T) {
	for _, n := range []int{0, -5, 3601, 2000000000} {
		if validMailPerHour(n) {
			t.Errorf("validMailPerHour(%d) = true, want false (Load must fall back to the default)", n)
		}
	}
	for _, n := range []int{1, 360, 3600} {
		if !validMailPerHour(n) {
			t.Errorf("validMailPerHour(%d) = false, want true", n)
		}
	}
}
