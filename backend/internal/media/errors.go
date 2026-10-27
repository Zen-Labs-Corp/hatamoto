package media

import "errors"

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
