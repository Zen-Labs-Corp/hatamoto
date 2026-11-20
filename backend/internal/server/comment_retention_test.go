package server

import (
	"context"
	"slices"
	"testing"
)

type recordingAdder struct {
	names []string
	fns   []func(context.Context) (int64, error)
}

func (r *recordingAdder) AddPurge(name string, fn func(context.Context) (int64, error)) {
	r.names = append(r.names, name)
	r.fns = append(r.fns, fn)
}

func TestCommentRetentionRegistersBothPurges(t *testing.T) {
	var rec recordingAdder
	(&Server{}).addCommentRetention(&rec)
	if want := []string{"comment spam and trash", "comment notification queue"}; !slices.Equal(rec.names, want) {
		t.Fatalf("purges = %v, want %v", rec.names, want)
	}
	// The worker is built before setupComments: without the service a purge is a no-op.
	for i, fn := range rec.fns {
		if n, err := fn(context.Background()); n != 0 || err != nil {
			t.Errorf("purge %d without comments = %d, %v", i, n, err)
		}
	}
}
