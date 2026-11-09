package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

type fakeMediaProbe struct{ err error }

func (f fakeMediaProbe) Test(context.Context) error { return f.err }

func mediaStoreAt(t *testing.T, endpoint string) *media.S3Store {
	t.Helper()
	st, err := media.NewS3Store(media.S3Target{Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDashboardHealthMediaStorage(t *testing.T) {
	t.Parallel()

	fake := s3test.NewServer(t)
	closed := httptest.NewServer(nil)
	closedURL := closed.URL
	closed.Close()

	for _, tc := range []struct {
		name  string
		probe MediaStorageProbe
		value string
		kind  string
	}{
		{"reachable", mediaStoreAt(t, fake.URL), "Object storage, reachable", "success"},
		{"closed server", mediaStoreAt(t, closedURL), "Object storage: bucket unreachable", "danger"},
		{"invalid settings", media.UnavailableStore(errors.New("HATAMOTO_MEDIA_S3_BUCKET is not set")).(MediaStorageProbe), "Object storage: bucket unreachable", "danger"},
	} {
		g := newRig(t, func(d *Deps) { d.MediaStorage = tc.probe })
		items, _ := healthOf(t, g)
		it, ok := items["Media storage"]
		if !ok || it.Badge.Label != tc.value || it.Badge.Kind != tc.kind {
			t.Errorf("%s: %+v (found %v)", tc.name, it, ok)
		}
	}
}

func TestDashboardHealthMediaStorageAbsent(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	items, _ := healthOf(t, g)
	if _, ok := items["Media storage"]; ok {
		t.Error("row shown without object storage")
	}
}
