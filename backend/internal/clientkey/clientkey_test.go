package clientkey_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/comment"
)

func TestClientKeyUsesOwnPurpose(t *testing.T) {
	t.Parallel()
	const secret, ip = "secret-a", "203.0.113.7"
	a, n := clientkey.New(secret).For("capa", ip)
	ca, cn := comment.NewKeys(secret).HashIP(ip)
	if a == "" || n == "" || a == ca || n == cn {
		t.Errorf("plugin keys %q %q must differ from comment hashes %q %q", a, n, ca, cn)
	}
}

func TestClientKeyPerPlugin(t *testing.T) {
	t.Parallel()
	d := clientkey.New("secret-a")
	a1, n1 := d.For("capa", "203.0.113.7")
	a2, n2 := d.For("other", "203.0.113.7")
	if a1 == a2 || n1 == n2 {
		t.Error("two plugins share a key")
	}
}

func TestClientKeyNetwork(t *testing.T) {
	t.Parallel()
	d := clientkey.New("secret-a")
	for _, c := range [][2]string{{"203.0.113.7", "203.0.113.99"}, {"2001:db8::1", "2001:db8::2"}} {
		a1, n1 := d.For("capa", c[0])
		a2, n2 := d.For("capa", c[1])
		if n1 != n2 || n1 == "" {
			t.Errorf("%v: net keys differ", c)
		}
		if a1 == a2 {
			t.Errorf("%v: addr keys equal", c)
		}
	}
}

func TestClientKeyEmpty(t *testing.T) {
	t.Parallel()
	if clientkey.New("") != nil {
		t.Fatal("empty secret must give a nil Deriver")
	}
	var nilD *clientkey.Deriver
	d := clientkey.New("secret-a")
	check := func(name string, a, n string) {
		if a != "" || n != "" {
			t.Errorf("%s: want empty keys, got %q %q", name, a, n)
		}
	}
	a, n := nilD.For("capa", "203.0.113.7")
	check("nil", a, n)
	a, n = d.For("", "203.0.113.7")
	check("empty plugin", a, n)
	a, n = d.For("capa", "not an ip")
	check("unparsable", a, n)
	a, n = d.For("capa", "203.0.113.7:1234")
	check("host:port", a, n)
}

func TestClientKeyBase64URL(t *testing.T) {
	t.Parallel()
	a, n := clientkey.New("secret-a").For("capa", "203.0.113.7")
	for _, k := range []string{a, n} {
		if strings.ContainsAny(k, "+/=") {
			t.Errorf("%q is not unpadded base64url", k)
		}
		if b, err := base64.RawURLEncoding.DecodeString(k); err != nil || len(b) != 32 {
			t.Errorf("%q decodes to %d bytes, err %v", k, len(b), err)
		}
	}
}

func TestClientKeyContext(t *testing.T) {
	t.Parallel()
	if got := clientkey.IPFrom(clientkey.WithIP(context.Background(), "203.0.113.7")); got != "203.0.113.7" {
		t.Errorf("IPFrom = %q", got)
	}
	if got := clientkey.IPFrom(context.Background()); got != "" {
		t.Errorf("clientkey.IPFrom(background) = %q", got)
	}
}
