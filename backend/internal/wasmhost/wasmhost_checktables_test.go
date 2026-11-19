package wasmhost

import "testing"

func TestCheckTablesFailsClosed(t *testing.T) {
	twoTables := concat(wasmHeader, section(0x04, 0x02,
		0x70, 0x00, 0x80, 0x80, 0x40, // 1<<20 entries
		0x70, 0x00, 0x01))
	cases := []struct {
		name string
		wasm []byte
		ok   bool
	}{
		{"empty", nil, false},
		{"bad magic", []byte("\x00bsm\x01\x00\x00\x00"), false},
		{"bad version", []byte("\x00asm\x02\x00\x00\x00"), false},
		{"truncated section size", concat(wasmHeader, []byte{0x04, 0x80}), false},
		{"size beyond data", concat(wasmHeader, []byte{0x04, 0x05, 0x01}), false},
		{"count without entries", concat(wasmHeader, section(0x04, 0x01)), false},
		{"unknown limits flag", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x03, 0x01)), false},
		{"non-funcref element type", concat(wasmHeader, section(0x04, 0x01, 0x6f, 0x00, 0x01)), false},
		{"overlong LEB", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x00, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00)), false},
		{"minimum beyond 32 bits", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x00, 0xff, 0xff, 0xff, 0xff, 0x1f)), false},
		{"trailing bytes", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x00, 0x01, 0x00)), false},
		{"missing maximum", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x01, 0x01)), false},
		{"sum across tables over the cap", twoTables, false},
		{"no table section", concat(wasmHeader, section(0x01, 0x01, 0x60, 0x00, 0x00)), true},
		{"table of 8000 entries", tableModule(0xc0, 0x3e), true},
		{"table at the cap with a maximum", concat(wasmHeader, section(0x04, 0x01, 0x70, 0x01, 0x80, 0x80, 0x40, 0x80, 0x80, 0x40)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkTables(c.wasm)
			if c.ok && err != nil {
				t.Fatalf("checkTables: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("checkTables accepted the module")
			}
		})
	}
}
