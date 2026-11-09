package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/s3/s3test"
)

func newTestClient(t *testing.T, srv *s3test.Server) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = old })
}

func smallParts(t *testing.T, n int64) {
	t.Helper()
	old := partSize
	partSize = n
	t.Cleanup(func() { partSize = old })
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func TestPutSingleVerifiedByServer(t *testing.T) {
	srv := s3test.NewServer(t)
	c := newTestClient(t, srv)
	data := randBytes(1000)
	if err := c.Put(context.Background(), "p/one.tar", bytes.NewReader(data), int64(len(data)), map[string]string{"sha256": "abc"}); err != nil {
		t.Fatal(err)
	}
	got := srv.Objects()["p/one.tar"]
	if !bytes.Equal(got.Data, data) || got.Meta["sha256"] != "abc" {
		t.Fatalf("stored object differs: meta=%v", got.Meta)
	}
	o, err := c.Head(context.Background(), "p/one.tar")
	if err != nil || o.Size != 1000 || o.Meta["sha256"] != "abc" {
		t.Fatalf("head = %+v, %v", o, err)
	}
	rc, _, err := c.Get(context.Background(), "p/one.tar")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, data) {
		t.Fatal("get returned different bytes")
	}
}

func TestPutMultipart(t *testing.T) {
	smallParts(t, 100)
	srv := s3test.NewServer(t)
	c := newTestClient(t, srv)
	data := randBytes(1050)
	if err := c.Put(context.Background(), "big.tar", bytes.NewReader(data), int64(len(data)), map[string]string{"sha256": "x"}); err != nil {
		t.Fatal(err)
	}
	got := srv.Objects()["big.tar"]
	if !bytes.Equal(got.Data, data) || got.Meta["sha256"] != "x" {
		t.Fatal("multipart object differs")
	}
	if srv.OpenUploads() != 0 {
		t.Fatal("upload left open")
	}
}

func TestPutMultipartRetriesATransientPartFailure(t *testing.T) {
	smallParts(t, 100)
	fastRetries(t)
	srv := s3test.NewServer(t)
	srv.FlakyPart = 3
	c := newTestClient(t, srv)
	data := randBytes(1050)
	if err := c.Put(context.Background(), "big.tar", bytes.NewReader(data), 1050, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(srv.Objects()["big.tar"].Data, data) || srv.OpenUploads() != 0 {
		t.Fatal("object differs or upload left open")
	}
}

func TestPutMultipartAbortsOnFailure(t *testing.T) {
	smallParts(t, 100)
	fastRetries(t)
	srv := s3test.NewServer(t)
	srv.FailPart = 3
	c := newTestClient(t, srv)
	err := c.Put(context.Background(), "big.tar", bytes.NewReader(randBytes(1050)), 1050, nil)
	var e *Error
	if !errors.As(err, &e) || e.Status != 500 {
		t.Fatalf("err = %v", err)
	}
	if srv.OpenUploads() != 0 {
		t.Fatal("orphan multipart upload left behind")
	}
	if _, ok := srv.Objects()["big.tar"]; ok {
		t.Fatal("object exists after failed upload")
	}
}

func TestListPaginatesAndDelete(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.PageSize = 2
	c := newTestClient(t, srv)
	for i := range 5 {
		srv.Put(fmt.Sprintf("pre/k%d", i), []byte("x"), nil)
	}
	srv.Put("other/k", []byte("x"), nil)
	objs, err := c.List(context.Background(), "pre/")
	if err != nil || len(objs) != 5 || objs[0].Key != "pre/k0" || objs[0].Size != 1 {
		t.Fatalf("list = %+v, %v", objs, err)
	}
	if err := c.Delete(context.Background(), "pre/k0"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "pre/missing"); err != nil {
		t.Fatalf("delete of a missing key: %v", err)
	}
	if _, err := c.Head(context.Background(), "pre/k0"); !NotFound(err) {
		t.Fatalf("head after delete: %v", err)
	}
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWrongSecretIsRejectedWithoutLeakingIt(t *testing.T) {
	srv := s3test.NewServer(t)
	c, err := New(Config{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: "wrong-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Test(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret-value") {
		t.Fatal("error leaks the secret key")
	}
}

func TestNoChecksumHeadersAreSent(t *testing.T) {
	smallParts(t, 100)
	srv := s3test.NewServer(t)
	c := newTestClient(t, srv)
	if err := c.Put(context.Background(), "a", bytes.NewReader(randBytes(250)), 250, nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-amz-checksum-") {
				t.Fatalf("%s %s sent %s", r.Method, r.Path, k)
			}
		}
		if r.Header.Get("X-Amz-Content-Sha256") == "UNSIGNED-PAYLOAD" {
			t.Fatal("unsigned payload")
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	c, err := New(Config{Endpoint: redir.URL, Region: "r", Bucket: "bucket", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Test(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestSignatureUsesInjectedClock(t *testing.T) {
	srv := s3test.NewServer(t)
	c := newTestClient(t, srv)
	c.now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[0].Header.Get("X-Amz-Date"); got != "20300102T030405Z" {
		t.Fatalf("X-Amz-Date = %s", got)
	}
}

func TestValidateEndpoint(t *testing.T) {
	for ep, ok := range map[string]bool{
		"https://s3.example.com":       true,
		"https://s3.example.com:9000/": true,
		"http://localhost:9000":        true,
		"http://127.0.0.1:1234":        true,
		"http://10.1.2.3":              true,
		"http://s3.example.com":        false,
		"http://8.8.8.8":               false,
		"ftp://s3.example.com":         false,
		"https://u:p@s3.example.com":   false,
		"https://s3.example.com/x":     false,
		"https://s3.example.com?a=b":   false,
		"https://s3.example.com#f":     false,
		"":                             false,
		"s3.example.com":               false,
	} {
		_, err := ValidateEndpoint(ep)
		if (err == nil) != ok {
			t.Errorf("ValidateEndpoint(%q) err=%v, want ok=%v", ep, err, ok)
		}
	}
}

func TestErrorCodeIsCapped(t *testing.T) {
	huge := strings.Repeat("A", 60000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<Error><Code>"+huge+"\n</Code></Error>")
	}))
	defer srv.Close()
	c, err := New(Config{Endpoint: srv.URL, Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Head(context.Background(), "k")
	var e *Error
	if !errors.As(err, &e) || len(e.Code) > maxCode+3 || strings.ContainsAny(e.Code, "\n") || len(err.Error()) > 300 {
		t.Fatalf("err = %.200v", err)
	}
}

func TestGetRange(t *testing.T) {
	srv := s3test.NewServer(t)
	c := newTestClient(t, srv)
	srv.Put("k", []byte("0123456789"), nil)
	ctx := context.Background()
	for rng, want := range map[string]string{"bytes=2-4": "234", "bytes=7-": "789", "bytes=-3": "789", "bytes=8-99": "89"} {
		rc, o, err := c.GetRange(ctx, "k", rng)
		if err != nil {
			t.Fatalf("%s: %v", rng, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != want || o.Size != int64(len(want)) || !strings.HasSuffix(o.ContentRange, "/10") {
			t.Errorf("%s: got %q %+v", rng, b, o)
		}
	}
	_, _, err := c.GetRange(ctx, "k", "bytes=10-")
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("unsatisfiable range: err = %v", err)
	}
}
