package media

import (
	"bytes"
	"context"
	"testing"
)

func TestImageTypeAndExtensionAllowed(t *testing.T) {
	for _, c := range []struct {
		ext, mime string
		want      bool
	}{
		{".webp", "image/jpeg", true},
		{".JPG", "image/webp", true},
		{".png", "image/gif", true},
		{".html", "image/png", false},
		{".svg", "image/png", false},
		{".png", "text/html; charset=utf-8", false},
		{".png", "image/svg+xml", false},
		{".pdf", "image/png", false},
		{".png", "application/pdf", false},
	} {
		if got := ImageTypeAndExtensionAllowed(c.ext, c.mime); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.ext, c.mime, got, c.want)
		}
	}
}

func TestVariantExtKeepsTheOriginalExtension(t *testing.T) {
	if variantExt("image/webp", ".webp") != ".webp" || variantExt("image/png", ".png") != ".png" {
		t.Error("variantExt wrong")
	}
}

func avifHead() []byte { return []byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf") }

func TestSniffContentTypeKnowsAVIF(t *testing.T) {
	if got := SniffContentType(avifHead()); got != "image/avif" {
		t.Errorf("avif sniffed as %q", got)
	}
	if got := SniffContentType([]byte("\x00\x00\x00\x1cftypavis\x00\x00\x00\x00")); got != "image/avif" {
		t.Errorf("avis sniffed as %q", got)
	}
	if got := SniffContentType([]byte("\x89PNG\r\n\x1a\n")); got != "image/png" {
		t.Errorf("png sniffed as %q", got)
	}
	if !ExtensionMatchesMIME(".avif", "image/avif") || ExtensionMatchesMIME(".png", "image/avif") {
		t.Error("avif extension pairing wrong")
	}
}

func TestUploadStillRefusesAVIFOriginals(t *testing.T) {
	svc := newTestService(t, Config{})
	body := append(avifHead(), make([]byte, 64)...)
	if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(body), "a.avif", "", int64(len(body))); err == nil {
		t.Fatal("an AVIF upload must still be refused")
	}
}
