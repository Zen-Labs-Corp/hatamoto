package statichost

import (
	"context"
	"testing"
)

func TestWithHostingNormalises(t *testing.T) {
	in := StaticHosting{Static: true, Live: "https://Live.Example.com/", Site: "HTTPS://WWW.example.com/"}
	got := HostingFrom(WithHosting(context.Background(), in), StaticHosting{})
	if !got.Static || got.Live != "https://live.example.com" || got.Site != "https://www.example.com" {
		t.Fatalf("got %+v", got)
	}
}

func TestHostingFromFallsBack(t *testing.T) {
	fb := StaticHosting{Live: "https://fallback.example.com"}
	if got := HostingFrom(context.Background(), fb); got != fb {
		t.Fatalf("got %+v, want fallback", got)
	}
}
