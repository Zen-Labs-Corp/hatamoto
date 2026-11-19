package server

import (
	"testing"

	"hatamoto.dev/core/internal/config"
)

func TestServerMaxHeaderBytes(t *testing.T) {
	t.Parallel()
	srv := (&Server{cfg: &config.Config{}}).newHTTPServer()
	if srv.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 64<<10)
	}
}
