// Package modern encodes resized images as WebP and AVIF. The encoders are
// libwebp and libavif compiled to WebAssembly and run on a wazero runtime
// owned by this package, so the product binary stays pure Go and statically
// linked. Build with the nodynamic tag so the libraries never try to load a
// system copy (see docs/DEPENDENCIES.md).
//
// Callers only encode already resized images (at most 1024 px on a side), so
// memory use is bounded by the upload pixel cap plus the resize step.
package modern

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
)

// Format is the MIME type of an encoded copy.
type Format string

// Supported output formats.
const (
	WebP Format = "image/webp"
	AVIF Format = "image/avif"
)

// maxConcurrent caps simultaneous encodes: each one holds a wasm instance.
const maxConcurrent = 2

var slots = make(chan struct{}, maxConcurrent)

// Available reports whether f can be encoded by this build.
func Available(f Format) bool {
	switch f {
	case WebP, AVIF:
		return true
	}
	return false
}

// Ext returns the file extension, with the dot, for f.
func Ext(f Format) string {
	switch f {
	case WebP:
		return ".webp"
	case AVIF:
		return ".avif"
	}
	return ""
}

// Encode writes img to w in format f. quality is 0..100. It blocks until an
// encode slot is free and honours ctx before and after the encode.
func Encode(ctx context.Context, w io.Writer, img image.Image, f Format, quality int) error {
	if !Available(f) {
		return fmt.Errorf("modern: unsupported format %q", f)
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	switch f {
	case WebP:
		err = webp.Encode(w, img, webp.Options{Quality: quality, Method: 4})
	case AVIF:
		err = avif.Encode(w, img, avif.Options{Quality: quality, QualityAlpha: quality, Speed: 8})
	default:
		err = errors.New("unreachable")
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}
