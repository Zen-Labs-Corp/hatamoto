package csvx

import "testing"

func TestSafeUnsafe(t *testing.T) {
	cases := []struct{ in, safe string }{
		{"", ""},
		{"abc", "abc"},
		{"=cmd", "'=cmd"},
		{"+1", "'+1"},
		{"-5", "'-5"},
		{"@x", "'@x"},
		{"\tx", "'\tx"},
		{"\rx", "'\rx"},
		{"'abc", "'abc"},
	}
	for _, c := range cases {
		if got := Safe(c.in); got != c.safe {
			t.Errorf("Safe(%q) = %q, want %q", c.in, got, c.safe)
		}
	}
	unsafe := []struct{ in, want string }{
		{"'-5", "-5"},
		{"'=cmd", "=cmd"},
		{"'abc", "'abc"},
		{"'", "'"},
		{"abc", "abc"},
		{"''=x", "''=x"},
	}
	for _, c := range unsafe {
		if got := Unsafe(c.in); got != c.want {
			t.Errorf("Unsafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, in := range []string{"=a", "-5", "plain", "@b"} {
		if got := Unsafe(Safe(in)); got != in {
			t.Errorf("round trip %q = %q", in, got)
		}
	}
}
