package media

import (
	"errors"
	"fmt"
)

// Error definitions for the media package.
var (
	ErrMediaNotFound   = errors.New("media not found")
	ErrInvalidMimeType = errors.New("invalid mime type")
	ErrFileTooLarge    = errors.New("file too large")
	ErrInvalidFile     = errors.New("invalid file")
	// ErrQuotaExceeded is returned when Config.MaxTotalBytes would be exceeded
	// (the public demo caps its total media size).
	ErrQuotaExceeded = errors.New("media storage quota exceeded")
	ErrStorageFailed = errors.New("storage operation failed")
	// ErrImageTooLarge is returned when an image's pixel count exceeds the
	// configured cap (decompression-bomb guard).
	ErrImageTooLarge = errors.New("image dimensions exceed the maximum allowed")
)

// ErrInvalidSVG is returned (wrapped in *SVGError) when an SVG cannot be made
// safe and is refused.
var ErrInvalidSVG = errors.New("invalid svg")

// SVGError says why an SVG was refused and on which line.
type SVGError struct {
	Reason string
	Line   int
}

func (e *SVGError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("invalid svg: %s (line %d)", e.Reason, e.Line)
	}
	return "invalid svg: " + e.Reason
}

// Is makes errors.Is(err, ErrInvalidSVG) true for every *SVGError.
func (e *SVGError) Is(target error) bool { return target == ErrInvalidSVG }
