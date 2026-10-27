package email

import (
	"io"
	"log/slog"
	"sync"
	"testing"
)

func TestReloadSwapsConfig(t *testing.T) {
	svc, err := NewService(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if svc.IsConfigured() {
		t.Fatal("empty config must not be configured")
	}

	svc.Reload(Config{SMTPHost: "smtp.example.com", FromAddress: "a@example.com", FromName: "A"})
	if !svc.IsConfigured() || svc.FromAddress() != "a@example.com" || svc.FromName() != "A" {
		t.Fatalf("reload not applied: %+v", svc.Config())
	}

	svc.Reload(Config{SMTPHost: "localhost"})
	if svc.IsConfigured() {
		t.Fatal("localhost must not count as configured by default")
	}
	svc.Reload(Config{SMTPHost: "localhost", AllowLocalhost: true})
	if !svc.IsConfigured() {
		t.Fatal("localhost must count as configured with AllowLocalhost")
	}
}

func TestReloadIsRaceFree(t *testing.T) {
	svc, err := NewService(Config{SMTPHost: "a"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				svc.Reload(Config{SMTPHost: "b", FromAddress: "x@y"})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = svc.IsConfigured()
				_ = svc.FromAddress()
			}
		}()
	}
	wg.Wait()
}
