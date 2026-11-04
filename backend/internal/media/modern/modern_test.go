package modern

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"testing"

	"golang.org/x/image/draw"
	xwebp "golang.org/x/image/webp"
)

// photo builds a deterministic photo-like image: smooth gradients plus
// texture, so lossy encoders have something real to compress.
func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			n := math.Sin(fx*40)*math.Cos(fy*37)*18 + float64((x*7+y*13)%11)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(clamp(120 + 100*fx + n)),
				G: uint8(clamp(90 + 120*fy + n)),
				B: uint8(clamp(160 - 80*fx*fy + n)),
				A: 255,
			})
		}
	}
	return img
}

func clamp(v float64) float64 { return math.Max(0, math.Min(255, v)) }

func scaled(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func TestEncodeWebP(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(context.Background(), &buf, photo(320, 240), WebP, 80); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		t.Fatalf("not a WebP stream: % x", b[:min(12, len(b))])
	}
	img, err := xwebp.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 320 {
		t.Fatalf("decode: %v", err)
	}
}

func TestEncodeAVIF(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(context.Background(), &buf, photo(160, 120), AVIF, 60); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		t.Fatalf("not an ISO BMFF stream")
	}
}

func TestEncodeHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Encode(ctx, &bytes.Buffer{}, photo(8, 8), WebP, 80); err == nil {
		t.Fatal("expected context error")
	}
}

func TestEncodeUnknownFormat(t *testing.T) {
	if Available("image/gif") || Ext("image/gif") != "" {
		t.Fatal("gif must not be available")
	}
	if err := Encode(context.Background(), &bytes.Buffer{}, photo(8, 8), "image/gif", 80); err == nil {
		t.Fatal("expected error")
	}
}

var big = photo(4000, 3000) // 12 MP

func benchEncode(b *testing.B, f Format, q, side int) {
	img := scaled(big, side, side*3/4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := Encode(context.Background(), &buf, img, f, q); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(buf.Len()), "bytes")
	}
}

func BenchmarkEncodeWebP1024(b *testing.B) { benchEncode(b, WebP, 80, 1024) }
func BenchmarkEncodeWebP300(b *testing.B)  { benchEncode(b, WebP, 80, 300) }
func BenchmarkEncodeAVIF1024(b *testing.B) { benchEncode(b, AVIF, 60, 1024) }
func BenchmarkEncodeAVIF300(b *testing.B)  { benchEncode(b, AVIF, 60, 300) }

func BenchmarkDecodeWebP1024(b *testing.B) {
	var buf bytes.Buffer
	if err := Encode(context.Background(), &buf, scaled(big, 1024, 768), WebP, 80); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := xwebp.Decode(bytes.NewReader(buf.Bytes())); err != nil {
			b.Fatal(err)
		}
	}
}
