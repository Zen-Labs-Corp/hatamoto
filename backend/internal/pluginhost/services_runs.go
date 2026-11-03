package pluginhost

import (
	"context"
	"io"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

// UploadReader opens a file the owner uploaded for a run. It never exposes a
// filesystem path.
type UploadReader interface {
	// Open returns the upload's bytes and size. The upload must belong to
	// plugin, else the error is not-found.
	Open(ctx context.Context, plugin string, uploadID uuid.UUID) (io.ReaderAt, int64, error)
}

// SiteSource answers site.info.
type SiteSource interface {
	Info(ctx context.Context) (sdk.SiteInfo, error)
}

// StagedBlobs stores a downloaded file for a run until the changeset that
// attaches it is published or discarded.
type StagedBlobs interface {
	StageBlob(ctx context.Context, runID uuid.UUID, r io.Reader, filename string) (handle string, err error)
}
