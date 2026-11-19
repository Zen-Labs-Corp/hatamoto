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
	// A looser core value must not carry over to the plugin's connections:
	// the driver applies this field after BuildFrontend.
	const looser = 64 << 20
	cfg.ConnConfig.MaxProtocolMessageBodyLen = looser
	cc := pluginConnConfig(cfg.ConnConfig, "capa", "hm_p_1234abcd_capa", "pw")

	if cc.MaxProtocolMessageBodyLen != maxDBMessageBytes {
		t.Fatalf("plugin cap = %d, want %d", cc.MaxProtocolMessageBodyLen, maxDBMessageBytes)
	}
	if cfg.ConnConfig.MaxProtocolMessageBodyLen != looser {
		t.Fatalf("the core config must keep its own value: %d", cfg.ConnConfig.MaxProtocolMessageBodyLen)
	}

	// The effective limit, as the driver applies it, refuses a message over
	// the cap at the header and accepts one at the cap (body absent here).
	var tooLong *pgproto3.ExceededMaxBodyLenErr
	fe := cc.BuildFrontend(bytes.NewReader(dataRowHeader(maxDBMessageBytes+1)), io.Discard)
	fe.SetMaxBodyLen(cc.MaxProtocolMessageBodyLen)
	if _, err = fe.Receive(); !errors.As(err, &tooLong) {
		t.Fatalf("a message over the cap must be refused at the header: %v", err)
	}
	fe = cc.BuildFrontend(bytes.NewReader(dataRowHeader(maxDBMessageBytes)), io.Discard)
	fe.SetMaxBodyLen(cc.MaxProtocolMessageBodyLen)
	if _, err = fe.Receive(); errors.As(err, &tooLong) {
		t.Fatalf("a message at the cap must not be refused: %v", err)
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
