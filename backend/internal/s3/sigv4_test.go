package s3

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// AWS Signature Version 4 test suite values (service "service", us-east-1).
const (
	vecAccess = "AKIDEXAMPLE"
	vecSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

var vecTime = time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

func vecRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestSignAWSVectors(t *testing.T) {
	for _, tc := range []struct{ name, method, sig string }{
		{"get-vanilla", http.MethodGet, "5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"post-vanilla", http.MethodPost, "5da7c1a2acd57cee7505fc6676e4e544621c30862966e37dddb68e92efbe5d6b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := vecRequest(t, tc.method)
			sign(req, "service", emptySHA256, "us-east-1", vecAccess, vecSecret, vecTime)
			want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
				"SignedHeaders=host;x-amz-date, Signature=" + tc.sig
			if got := req.Header.Get("Authorization"); got != want {
				t.Fatalf("Authorization\n got %s\nwant %s", got, want)
			}
		})
	}
}

// Headers are signed lower-cased, sorted by name, with trimmed and collapsed values.
func TestSignHeaderSorting(t *testing.T) {
	req := vecRequest(t, http.MethodGet)
	req.Header.Set("Zeta", "  b   c ")
	req.Header.Set("My-Header1", "value1")
	req.Header.Set("alpha", "x")
	canonical, signed := canonicalRequest(req, emptySHA256)
	if signed != "alpha;host;my-header1;zeta" {
		t.Fatalf("signed headers = %q", signed)
	}
	want := "GET\n/\n\nalpha:x\nhost:example.amazonaws.com\nmy-header1:value1\nzeta:b c\n\n" + signed + "\n" + emptySHA256
	if canonical != want {
		t.Fatalf("canonical\n got %q\nwant %q", canonical, want)
	}
}

func TestUriEncode(t *testing.T) {
	if got := uriEncode("a b/ü~.-_+", false); got != "a%20b/%C3%BC~.-_%2B" {
		t.Fatalf("path: %s", got)
	}
	if got := uriEncode("a/b", true); got != "a%2Fb" {
		t.Fatalf("query: %s", got)
	}
	if got := canonicalQuery(url.Values{"b": {"2"}, "a": {"1"}}); got != "a=1&b=2" {
		t.Fatalf("query not sorted: %s", got)
	}
}
