package contactlink

import "testing"

func TestPhone(t *testing.T) {
	cases := []struct {
		in     string
		digits string
		intl   bool
		ok     bool
	}{
		{"+41 79 123 45 67", "41791234567", true, true},
		{"+41 (0)79 123 45 67", "41791234567", true, true},
		{"0041 79 123 45 67", "41791234567", true, true},
		{"079 123 45 67", "0791234567", false, true},
		{"(079) 123-45-67", "0791234567", false, true},
		{"123", "123", false, false},
		{"1234567890123456", "1234567890123456", false, false},
	}
	for _, c := range cases {
		d, intl, ok := Phone(c.in)
		if d != c.digits || intl != c.intl || ok != c.ok {
			t.Errorf("Phone(%q) = %q %v %v, want %q %v %v", c.in, d, intl, ok, c.digits, c.intl, c.ok)
		}
	}
}
