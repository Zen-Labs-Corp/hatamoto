package pluginhost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingBlocksPluginHTTP(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	allowed := []string{u.Hostname() + ":" + u.Port()}
	f := NewFetcherForTests()
	ctx := context.Background()
	sink := func(io.Reader) error { t.Error("sink called"); return nil }

	_, err := f.Do(ctx, "p", allowed, FetchRequest{URL: srv.URL, TimeoutMS: 3000})
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrFetchBlocked) || !strings.Contains(err.Error(), "staging mode") {
			t.Errorf("%s (%s): err = %v, want staging ErrFetchBlocked", name, runenv.PluginHTTP, err)
		}
	}
	check("Do", err)
	check("Download", f.Download(ctx, "p", allowed, srv.URL, 1<<20, sink))
	check("DownloadPublic", f.DownloadPublic(ctx, "p", allowed, "https://"+u.Host, 1<<20, func(io.Reader, int64) error { return nil }))
	if hits.Load() != 0 {
		t.Fatalf("receiver saw %d requests", hits.Load())
	}
}
