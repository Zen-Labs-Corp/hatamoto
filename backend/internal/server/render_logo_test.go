package server

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/media"
)

func logoRow(storage, public string, w, h *int) *media.Media {
	m := &media.Media{StoragePath: storage, Width: w, Height: h}
	if public != "" {
		m.PublicURL = &public
	}
	return m
}

func TestLogoDimensionsWith(t *testing.T) {
	w, h := 640, 200
	zero := 0
	rows := []*media.Media{
		logoRow("2030/01/other.png", "", &w, &h),
		logoRow("2030/01/logo.png", "/media/2030/01/logo.png", &w, &h),
		logoRow("2030/01/logo.svg", "", nil, nil),
		logoRow("2030/01/flat.png", "", &zero, &zero),
	}
	var searched []string
	search := func(_ context.Context, q string) ([]*media.Media, error) {
		searched = append(searched, q)
		return rows, nil
	}
	tests := []struct {
		name, url string
		w, h      int
	}{
		{"hit by public url", "/media/2030/01/logo.png", 640, 200},
		{"hit with host and query", "https://example.test/media/2030/01/logo.png?v=2", 640, 200},
		{"hit by storage path", "https://cdn.example.test/media/2030/01/other.png", 640, 200},
		{"svg has no size", "/media/2030/01/logo.svg", 0, 0},
		{"zero size", "/media/2030/01/flat.png", 0, 0},
		{"miss", "/media/2030/01/missing.png", 0, 0},
		{"not a file", "https://example.test/", 0, 0},
	}
	for _, tt := range tests {
		if w, h := logoDimensionsWith(context.Background(), search, tt.url); w != tt.w || h != tt.h {
			t.Errorf("%s: got %dx%d, want %dx%d", tt.name, w, h, tt.w, tt.h)
		}
	}
	if len(searched) == 0 || searched[0] != "logo.png" {
		t.Errorf("searched = %v, want the file name first", searched)
	}
	failing := func(context.Context, string) ([]*media.Media, error) { return nil, errors.New("down") }
	if w, h := logoDimensionsWith(context.Background(), failing, "/media/2030/01/logo.png"); w != 0 || h != 0 {
		t.Errorf("lookup error: got %dx%d, want 0x0", w, h)
	}
}

func TestLogoSize(t *testing.T) {
	w, h := 10, 20
	calls := 0
	search := func(context.Context, string) ([]*media.Media, error) {
		calls++
		return []*media.Media{logoRow("logo.png", "", &w, &h)}, nil
	}
	if gw, gh := logoSize(context.Background(), false, search, "/media/logo.png"); gw != 0 || gh != 0 || calls != 0 {
		t.Errorf("off: got %dx%d after %d lookups, want 0x0 and none", gw, gh, calls)
	}
	if gw, gh := logoSize(context.Background(), true, search, "/media/logo.png"); gw != 10 || gh != 20 || calls != 1 {
		t.Errorf("on: got %dx%d after %d lookups, want 10x20 and one", gw, gh, calls)
	}
	if gw, gh := logoSize(context.Background(), true, (&Server{}).logoSearch(), "/media/logo.png"); gw != 0 || gh != 0 {
		t.Errorf("no media service: got %dx%d, want 0x0", gw, gh)
	}
}
