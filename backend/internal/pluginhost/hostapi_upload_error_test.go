package pluginhost

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func TestUploadOpenError(t *testing.T) {
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"caller ctx ended", ended, context.Canceled, sdk.ErrTimeout},
		{"download deadline", context.Background(), context.DeadlineExceeded, sdk.ErrNotFound},
		{"download failed", context.Background(), errors.New("store down"), sdk.ErrNotFound},
		{"lookup ctx error, caller live", context.Background(), context.Canceled, sdk.ErrNotFound},
	} {
		if he := uploadOpenError(c.ctx, "u", c.err); he.Code != c.want {
			t.Errorf("%s: code = %s, want %s", c.name, he.Code, c.want)
		}
	}
}
