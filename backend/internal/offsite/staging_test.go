package offsite

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/runenv"
	"hatamoto.dev/core/internal/s3/s3test"
)

func TestUploadIsSuppressedInStaging(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	srv := s3test.NewServer(t)
	c := testClient(t, srv, "site/")
	srv.Put("site/"+idN(1)+".tar", []byte("old"), nil)
	srv.Put("site/"+idN(2)+".tar", []byte("old"), nil)
	path, _ := writeArchive(t, 1024)
	res, err := Upload(t.Context(), c, "site/", idN(9), path, 1, nil)
	if !errors.Is(err, runenv.ErrSuppressed) || res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !strings.Contains(logs.String(), string(runenv.OffsiteBackup)) {
		t.Fatalf("suppression not logged for %q: %s", runenv.OffsiteBackup, logs.String())
	}
	if got := srv.Objects(); len(got) != 2 {
		t.Fatalf("bucket changed: %d objects (no upload, no prune expected)", len(got))
	}
}
