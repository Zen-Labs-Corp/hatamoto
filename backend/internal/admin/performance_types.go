package admin

import (
	"context"
	"time"
)

// PageCacheService is the rendered-page cache as Settings > Performance uses
// it. The server implements it and owns persistence; the admin only validates
// the form and calls through.
type PageCacheService interface {
	Stats(ctx context.Context) PageCacheStats
	Settings(ctx context.Context) (PageCacheSettings, error)
	Save(ctx context.Context, s PageCacheSettings) error
	Clear(ctx context.Context) (pages int, err error)
	PreloadNow(ctx context.Context) (started bool, err error)
	PreloadStatus() PreloadStatus
}

// PageCacheSettings are the owner's stored choices. TTLSeconds 0 means "use
// the server default", MaxMB 0 means no limit, PreloadIntervalSeconds 0 means
// "same as the lifetime".
type PageCacheSettings struct {
	Enabled                bool
	TTLSeconds, MaxMB      int
	PreloadEnabled         bool
	PreloadIntervalSeconds int
	// PreloadMode is "missing" (skip cached pages) or "all" (refresh).
	PreloadMode string
	// PreloadAfterChanges schedules a preload shortly after content changes.
	PreloadAfterChanges bool
}

// PageCacheStats is the live state shown next to the form. TTLSource is
// "admin" or "server".
type PageCacheStats struct {
	Pages                int
	Bytes, CapBytes      int64
	EffectiveTTL         time.Duration
	ServerDefaultTTL     time.Duration
	TTLSource            string
	ServerDefaultFromEnv bool
	BaseURLPinned        bool
	SchedulerAvailable   bool
}

// PreloadStatus is the in-memory state of the preloader and its last result.
type PreloadStatus struct {
	Running       bool
	Done, Total   int
	HasResult     bool
	Finished      time.Time
	Pages, Errors int
	Duration      time.Duration
	StoppedEarly  bool
	Reason        string
	Skipped       int // pages already cached and left alone
	Failures      []PreloadFailure
}

// PreloadFailure names a page the last preload could not build and why.
type PreloadFailure struct {
	Path, Reason string
}
