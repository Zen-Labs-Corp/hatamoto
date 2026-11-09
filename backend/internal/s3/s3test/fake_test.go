package s3test_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/s3"
	"hatamoto.dev/core/internal/s3/s3test"
)

func client(t *testing.T, srv *s3test.Server) *s3.Client {
	t.Helper()
	c, err := s3.New(s3.Config{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func put(c *s3.Client, key string) error {
	b := []byte("data")
	return c.Put(context.Background(), key, bytes.NewReader(b), int64(len(b)), nil)
}

func TestPutAtReportsItsTime(t *testing.T) {
	srv := s3test.NewServer(t)
	c := client(t, srv)
	at := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	srv.PutAt("old", []byte("x"), nil, at)
	srv.Put("plain", []byte("x"), nil)

	list, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Time{}
	for _, o := range list {
		got[o.Key] = o.LastModified
	}
	if !got["old"].Equal(at) {
		t.Fatalf("listed time of old = %v, want %v", got["old"], at)
	}
	if !got["plain"].Equal(s3test.DefaultModTime) {
		t.Fatalf("listed time of plain = %v, want the default", got["plain"])
	}
	head, err := c.Head(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if !head.LastModified.Equal(at) {
		t.Fatalf("Last-Modified of old = %v, want %v", head.LastModified, at)
	}
}

func TestFailPutStoresNothing(t *testing.T) {
	srv := s3test.NewServer(t)
	c := client(t, srv)
	srv.FailPut(func(key string) bool { return key == "bad" })
	if err := put(c, "bad"); err == nil {
		t.Fatal("PUT of a matching key succeeded")
	}
	if err := put(c, "good"); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Objects()["bad"]; ok {
		t.Fatal("a failed PUT stored the object")
	}
	if _, ok := srv.Objects()["good"]; !ok {
		t.Fatal("a non-matching PUT was not stored")
	}
}

func TestPutThenFailStoresAndErrors(t *testing.T) {
	srv := s3test.NewServer(t)
	c := client(t, srv)
	srv.PutThenFail(func(key string) bool { return key == "lost" })
	if err := put(c, "lost"); err == nil {
		t.Fatal("PUT whose reply is lost reported success")
	}
	if _, ok := srv.Objects()["lost"]; !ok {
		t.Fatal("the object was not stored")
	}
}
