// Package geotest builds tiny country databases for tests, without a writer
// dependency.
package geotest

import "bytes"

func mmdbStr(s string) []byte { return append([]byte{byte(0x40 + len(s))}, s...) }

// countryRecord encodes {"country": {"iso_code": code}} in the mmdb data format.
func countryRecord(code string) []byte {
	var b bytes.Buffer
	b.WriteByte(0xE1)
	b.Write(mmdbStr("country"))
	b.WriteByte(0xE1)
	b.Write(mmdbStr("iso_code"))
	b.Write(mmdbStr(code))
	return b.Bytes()
}

// TinyMMDB builds a one-node IPv4 database: addresses whose first bit is 0
// (for example 1.2.3.4) map to low, the others (for example 200.1.1.1) to high.
// Codes are stored as given, so a lower-case code exercises normalisation.
func TinyMMDB(low, high string) []byte {
	de, fr := countryRecord(low), countryRecord(high)
	rec := func(off int) []byte {
		v := 1 + 16 + off // node_count + data section separator + offset
		return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
	}
	var b bytes.Buffer
	b.Write(rec(0))
	b.Write(rec(len(de)))
	b.Write(make([]byte, 16))
	b.Write(de)
	b.Write(fr)
	b.WriteString("\xab\xcd\xefMaxMind.com")
	b.WriteByte(0xE6)
	for _, kv := range []struct {
		k string
		v []byte
	}{
		{"node_count", []byte{0xC1, 1}},
		{"record_size", []byte{0xA1, 24}},
		{"ip_version", []byte{0xA1, 4}},
		{"binary_format_major_version", []byte{0xA1, 2}},
		{"binary_format_minor_version", []byte{0xA0}},
		{"database_type", mmdbStr("test")},
	} {
		b.Write(mmdbStr(kv.k))
		b.Write(kv.v)
	}
	return b.Bytes()
}
