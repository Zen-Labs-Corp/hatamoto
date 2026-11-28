package staticexport

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSuppressesStaticExportS3(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	tgt, srv := s3Fixture(t, "site/")
	staged, next := stageFiles(t, map[string]string{"index.html": "<p>hi</p>"})
	d := Diff{Changed: []string{"index.html"}}
	err := tgt.Publish(context.Background(), staged, d, next)
	if !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("%s: err = %v, want ErrSuppressed", runenv.StaticExportS3, err)
	}
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			t.Fatalf("receiver saw %s %s", r.Method, r)
		}
	}
	if len(srv.Objects()) != 0 {
		t.Fatalf("objects = %v", srv.Objects())
	}
}

func TestStagingSuppressesStaticExportSFTP(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	srv := newSFTPServer(t)
	tgt := newTestSFTPTarget(t, srv, nil)
	staged, next := stageFiles(t, map[string]string{"index.html": "<p>hi</p>"})
	err := tgt.Publish(context.Background(), staged, Diff{Changed: []string{"index.html"}}, next)
	if !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("%s: err = %v, want ErrSuppressed", runenv.StaticExportSFTP, err)
	}
	if srv.Conns() != 0 {
		t.Fatalf("connected %d times in staging", srv.Conns())
	}
	if _, statErr := os.Stat(filepath.Join(srv.Root, "public_html")); !os.IsNotExist(statErr) {
		t.Fatalf("the receiver was written: %v", statErr)
	}
}
