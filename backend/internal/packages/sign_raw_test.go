package packages

import "testing"

// The embedded index keeps the escapes and bytes that were signed: \u00e9,
// \/, upper-case hex and invalid UTF-8 must still verify after SignIndex.
func TestSignIndexKeepsRawStrings(t *testing.T) {
	for name, desc := range map[string]string{
		"unicode escape":    `caf\u00e9`,
		"upper-case hex":    `caf\u00E9`,
		"escaped slash":     `a\/b`,
		"invalid utf-8":     "x\xffy",
		"html + escape mix": `a \u0026 b < \/ c`,
	} {
		t.Run(name, func(t *testing.T) {
			idx := `{"version":1,"issued_at":"2030-01-01T00:00:00Z","expires_at":"2030-02-01T00:00:00Z","packages":[{"description":"` + desc + `"}]}`
			doc, err := SignIndex([]byte(idx), pinKey())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pinTrust(t).VerifyIndex(doc); err != nil {
				t.Fatalf("signed index does not verify: %v", err)
			}
		})
	}
}
