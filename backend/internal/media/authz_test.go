package media

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
)

func TestMediaAuthzObject(t *testing.T) {
	id, up := uuid.New(), uuid.New()
	got := (&Media{ID: id, UploadedBy: &up}).AuthzObject()
	if want := (authz.Object{Kind: "media", ID: id.String(), CreatedBy: up.String()}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	got = (&Media{ID: id}).AuthzObject()
	if want := (authz.Object{Kind: "media", ID: id.String()}); got != want {
		t.Errorf("no uploader: got %+v, want %+v", got, want)
	}
}
