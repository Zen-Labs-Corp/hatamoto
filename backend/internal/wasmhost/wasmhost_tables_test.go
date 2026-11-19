package wasmhost

import (
	"context"
	"testing"

	"hatamoto.dev/sdk"
)

// section frames a payload as a wasm section; the payloads used here stay
// below 128 bytes so the size is a single LEB byte.
func section(id byte, payload ...byte) []byte {
	return append([]byte{id, byte(len(payload))}, payload...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var wasmHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// tableModule is a module with one funcref table (no maximum) of the given
// minimum, given as LEB128 bytes, and an empty function body.
func tableModule(minLEB ...byte) []byte {
	return concat(wasmHeader,
		section(0x01, 0x01, 0x60, 0x00, 0x00),
		section(0x03, 0x01, 0x00),
		section(0x04, append([]byte{0x01, 0x70, 0x00}, minLEB...)...),
		section(0x0a, 0x01, 0x02, 0x00, 0x0b),
	)
}

func TestTablesAreBounded(t *testing.T) {
	growModule := concat(wasmHeader,
		section(0x01, 0x01, 0x60, 0x00, 0x00),
		section(0x03, 0x01, 0x00),
		section(0x04, 0x01, 0x70, 0x00, 0x01),
		// ref.null func; i32.const 1; table.grow 0; drop
		section(0x0a, 0x01, 0x0a, 0x00, 0xd0, 0x70, 0x41, 0x01, 0xfc, 0x0f, 0x00, 0x1a, 0x0b),
	)
	cases := []struct {
		name string
		wasm []byte
		ok   bool
	}{
		{"grow instruction", growModule, false},
		{"minimum of 2^24 entries", tableModule(0x80, 0x80, 0x80, 0x08), false},
		{"minimum of 8000 entries", tableModule(0xc0, 0x3e), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			err := h.load(context.Background(), manifest("tables", sdk.Limits{}), c.wasm)
			if c.ok {
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if got := h.Loaded(); len(got) != 1 {
					t.Fatalf("Loaded() = %v, want the module", got)
				}
				return
			}
			if err == nil {
				t.Fatal("load succeeded, want a refusal")
			}
			if got := h.Loaded(); len(got) != 0 {
				t.Fatalf("Loaded() = %v, want empty", got)
			}
		})
	}
}
