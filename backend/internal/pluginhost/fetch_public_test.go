package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func publicTestServer(t *testing.T, h http.Handler) (*httptest.Server, *Fetcher) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, NewFetcherForTestsTLS(srv.Client().Transport.(*http.Transport).TLSClientConfig)
}

func readAll(dst *[]byte) func(io.Reader) error {
	return func(r io.Reader) (err error) {
		*dst, err = io.ReadAll(r)
		return err
	}
}

func TestDownloadPublicOK(t *testing.T) {
	srv, f := publicTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("package-bytes"))
	}))
	var got []byte
	if err := f.DownloadPublic(context.Background(), "acme", srv.URL+"/a.hmp", 1<<10, readAll(&got)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("package-bytes")) {
		t.Fatalf("body %q", got)
	}
}

func TestDownloadPublicStatus(t *testing.T) {
	srv, f := publicTestServer(t, http.NotFoundHandler())
	err := f.DownloadPublic(context.Background(), "acme", srv.URL, 1<<10, readAll(new([]byte)))
	var se *DownloadStatusError
	if !errors.As(err, &se) || se.Status != 404 {
		t.Fatalf("err %v", err)
	}
}

func TestDownloadPublicOverCap(t *testing.T) {
	srv, f := publicTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
	}))
	err := f.DownloadPublic(context.Background(), "acme", srv.URL, 10, readAll(new([]byte)))
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("err %v", err)
	}
}

func TestDownloadPublicRefusesHTTP(t *testing.T) {
	f := NewFetcherForTests()
	err := f.DownloadPublic(context.Background(), "acme", "http://example.com/a.hmp", 10, readAll(new([]byte)))
	if !errors.Is(err, errNotHTTPS) {
		t.Fatalf("err %v", err)
	}
}

func TestDownloadPublicRefusesHTTPRedirect(t *testing.T) {
	srv, f := publicTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/a.hmp", http.StatusFound)
	}))
	err := f.DownloadPublic(context.Background(), "acme", srv.URL, 10, readAll(new([]byte)))
	if !errors.Is(err, ErrFetchBlocked) {
		t.Fatalf("err %v", err)
	}
}

func TestDownloadPublicRefusals(t *testing.T) {
	f := NewFetcher()
	for _, u := range []string{"https://user:pw@example.com/a", "https://example.com:8443/a", "https:///a"} {
		err := f.DownloadPublic(context.Background(), "acme", u, 10, readAll(new([]byte)))
		if !errors.Is(err, ErrFetchBlocked) {
			t.Errorf("%s: err %v", u, err)
		}
	}
}

func TestDownloadPublicBlocksLoopback(t *testing.T) {
	err := NewFetcher().DownloadPublic(context.Background(), "acme", "https://127.0.0.1/a.hmp", 10, readAll(new([]byte)))
	if !errors.Is(err, ErrFetchBlocked) {
		t.Fatalf("err %v", err)
	}
}
