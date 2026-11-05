package standing

import "testing"

// The stored fingerprints are compared against these hashes, so the canonical
// encoding is pinned byte for byte.
func TestManifestHashGolden(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"nested and numbers", `{"b":{"z":[1,1.5,1e21,0.1,null],"a":true},"a":"x"}`, "b3ecc32a5a3336c8d41b2dd3c0c1634d411bc621481f50080ccc32cfa78d8c6f"},
		{"html and unicode", `{"k":"<b>&","sep":"a\u2028b","u":"héllo ☃","arr":[[],{}]}`, "b6f149d5ae43b87012f4d921e4c730ae962c1934a5658197070b45bab7ef20b5"},
		{"null and scalars", `{"n":null,"i":-0,"f":1.0,"big":12345678901234567890,"e":1E-7}`, "a5b713f453de9790bed4d7c858bd40004e851f9d71b9b21db31d146505be0931"},
		{"non object", `[3,2,1,"<>"]`, "c9500ca549641fa47c59db690a15158946a5655c7a6791815bec0ff3e667edc0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ManifestHash([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("ManifestHash = %q, want %q", got, c.want)
			}
		})
	}
}

func TestManifestHashIgnoresWhitespaceAndKeyOrder(t *testing.T) {
	a, err := ManifestHash([]byte(`{"b":{"y":1,"x":[1, 2]},"a":"<b>"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ManifestHash([]byte("{\n  \"a\": \"<b>\",\n  \"b\": {\"x\": [1,2], \"y\": 1}\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("hashes differ: %s vs %s", a, b)
	}
}

func TestSchemaHashGolden(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"object schema", `{"type":"object","properties":{"title":{"type":"string","maxLength":80},"n":{"type":"number","minimum":0.5}},"required":["title"]}`, "46d8363dbfc94402e04c6ce6fd2905e5cdf82864429399170c4dec07db236411"},
		{"html in description", `{"description":"a<b>&c\u2028","type":"string"}`, "b1264c7e9aa46159e186336acd7fd59fd6b5de081b1a0261eb1f512af4cf4447"},
		{"empty object", `{}`, "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SchemaHash([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("SchemaHash = %q, want %q", got, c.want)
			}
		})
	}
}
