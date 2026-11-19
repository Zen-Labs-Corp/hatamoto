package pluginhost

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dataRowHeader is the header of a DataRow message with a body of n bytes.
func dataRowHeader(n int) []byte {
	h := []byte{'D', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[1:], uint32(4+n))
	return h
}

func TestPluginConnConfigBoundsServerMessages(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://u:core@db.example.com/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cc := pluginConnConfig(cfg.ConnConfig, "capa", "hm_p_1234abcd_capa", "pw")

	var tooLong *pgproto3.ExceededMaxBodyLenErr
	_, err = cc.BuildFrontend(bytes.NewReader(dataRowHeader(maxDBMessageBytes+1)), io.Discard).Receive()
	if !errors.As(err, &tooLong) {
		t.Fatalf("a message over the cap must be refused at the header: %v", err)
	}

	// At the cap the header is accepted; the body is absent here.
	_, err = cc.BuildFrontend(bytes.NewReader(dataRowHeader(maxDBMessageBytes)), io.Discard).Receive()
	if errors.As(err, &tooLong) {
		t.Fatalf("a message at the cap must not be refused: %v", err)
	}

	// The core pool's config keeps the driver default.
	_, err = cfg.ConnConfig.BuildFrontend(bytes.NewReader(dataRowHeader(maxDBMessageBytes+1)), io.Discard).Receive()
	if errors.As(err, &tooLong) {
		t.Fatalf("the core config must not be capped: %v", err)
	}
}

func TestRawRowSize(t *testing.T) {
	if got := rawRowSize(nil); got != 0 {
		t.Fatalf("empty row: %d", got)
	}
	if got := rawRowSize([][]byte{[]byte("abc"), nil, []byte(""), []byte("wxyz")}); got != 7 {
		t.Fatalf("row size: %d", got)
	}
}
