package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

func testDeliverer(o Options) *Deliverer {
	return NewDeliverer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), o)
}

func testSubscription(u string) *Subscription {
	s := validSub()
	s.ID, s.URL, s.Secret = "sub-1", u, "whsec_test"
	s.AllowHTTP = strings.HasPrefix(u, "http://")
	return &s
}

func TestSendHeadersAndSignatures(t *testing.T) {
	var got http.Header
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(202)
		_, _ = w.Write([]byte("thanks"))
	}))
	defer srv.Close()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	d := testDeliverer(Options{AllowPrivate: true, Now: func() time.Time { return now }})
	sub := testSubscription(srv.URL)
	sub.Headers = map[string]string{
		"X-Custom":               "yes",
		"Content-Type":           "text/plain", // protected
		"User-Agent":             "evil",       // protected
		"X-Webhook-Signature":    "forged",     // signatures win
		"X-Webhook-Signature-V2": "forged",
	}
	payload := `{"id":"d1","event":"entity.created","timestamp":"x","data":{}}`
	status, resp, err := d.send(context.Background(), sub, Delivery{ID: "d1", Event: EventEntityCreated, Payload: payload}, 2*time.Second)
	if err != nil || status != 202 || resp != "thanks" {
		t.Fatalf("send = %d %q %v", status, resp, err)
	}
	if body != payload {
		t.Fatalf("body = %q", body)
	}
	want := map[string]string{
		"Content-Type": "application/json", "User-Agent": "Hatamoto-Webhook/2.0", "X-Webhook-Id": "sub-1",
		"X-Webhook-Event": "entity.created", "X-Webhook-Delivery": "d1", "X-Webhook-Timestamp": "2026-01-02T03:04:05Z",
		"X-Custom":               "yes",
		"X-Webhook-Signature":    Sign("whsec_test", []byte(payload)),
		"X-Webhook-Signature-V2": SignV2("whsec_test", now.Unix(), []byte(payload)),
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("header %s = %q, want %q", k, got.Get(k), v)
		}
	}
}

func TestNoSignatureHeadersWithoutSecret(t *testing.T) {
	sub := testSubscription("https://example.com/h")
	sub.Secret = ""
	req, err := buildRequest(context.Background(), sub, Delivery{ID: "d", Event: "e", Payload: "{}"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Webhook-Signature") != "" || req.Header.Get("X-Webhook-Signature-V2") != "" {
		t.Fatal("signature headers set without a secret")
	}
}

func TestResponseBodyClippedTo1024(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("é", 2000))) // 4000 bytes, clipped to 1024
	}))
	defer srv.Close()
	d := testDeliverer(Options{AllowPrivate: true})
	_, body, err := d.send(context.Background(), testSubscription(srv.URL), Delivery{ID: "d", Event: "e", Payload: "{}"}, time.Second)
	if err != nil || len(body) != MaxResponseBody {
		t.Fatalf("body len = %d, %v", len(body), err)
	}
	if got := clip([]byte("ab\xffcd\x00e")); got != "abcde" {
		t.Fatalf("clip = %q", got)
	}
}

func TestPortOtherThan80Or443RefusedBeforeDial(t *testing.T) {
	var lookups atomic.Int32
	d := testDeliverer(Options{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		lookups.Add(1)
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}})
	_, _, err := d.send(context.Background(), testSubscription("https://example.com:8080/h"), Delivery{ID: "d", Event: "e", Payload: "{}"}, time.Second)
	if !errors.Is(err, safefetch.ErrBlocked) || !strings.Contains(err.Error(), "8080") {
		t.Fatalf("err = %v", err)
	}
	if lookups.Load() != 0 {
		t.Fatal("the resolver was consulted before the port check")
	}
}

func TestPlainHTTPRefusedWithoutAllowHTTP(t *testing.T) {
	d := testDeliverer(Options{AllowPrivate: true})
	sub := testSubscription("http://example.com/h")
	sub.AllowHTTP = false
	if _, _, err := d.send(context.Background(), sub, Delivery{ID: "d", Event: "e", Payload: "{}"}, time.Second); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrivateAddressRefusedAtDialTime(t *testing.T) {
	d := testDeliverer(Options{}) // production policy
	_, _, err := d.send(context.Background(), testSubscription("https://127.0.0.1/h"), Delivery{ID: "d", Event: "e", Payload: "{}"}, time.Second)
	if !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func TestCheckHop(t *testing.T) {
	for _, tc := range []struct {
		url            string
		allowHTTP, any bool
		ok             bool
	}{
		{"https://a.example/x", false, false, true},
		{"https://a.example:443/x", false, false, true},
		{"https://a.example:8443/x", false, false, false},
		{"https://a.example:8443/x", false, true, true},
		{"http://a.example:80/x", true, false, true},
		{"http://a.example/x", false, false, false},
		{"http://a.example:8080/x", true, false, false},
		{"ftp://a.example/x", true, true, false},
		{"https://u:p@a.example/x", false, false, false},
	} {
		if err := checkHop(mustURL(tc.url), tc.allowHTTP, tc.any); (err == nil) != tc.ok {
			t.Errorf("checkHop(%s, http=%v, any=%v) = %v", tc.url, tc.allowHTTP, tc.any, err)
		}
	}
}

func TestRedirectHopPolicy(t *testing.T) {
	https := &http.Request{URL: mustURL("https://a.example/start")}
	down := &http.Request{URL: mustURL("http://a.example/next")}
	strict, loose := newClient(Options{}, false), newClient(Options{}, true)
	if err := strict.CheckRedirect(down, []*http.Request{https}); err == nil {
		t.Error("https->http downgrade followed")
	}
	if err := loose.CheckRedirect(down, []*http.Request{https}); err != nil {
		t.Errorf("allow_http client refused http hop: %v", err)
	}
	odd := &http.Request{URL: mustURL("https://a.example:8443/next")}
	if err := strict.CheckRedirect(odd, []*http.Request{https}); err == nil {
		t.Error("redirect to port 8443 followed")
	}
	via := make([]*http.Request, 6)
	if err := strict.CheckRedirect(&http.Request{URL: mustURL("https://a.example/n")}, via); err == nil {
		t.Error("6th redirect followed")
	}
}

func TestRedirectChainLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if n == 0 {
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("%s/%d", srv.URL, n-1), http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	d := testDeliverer(Options{AllowPrivate: true})
	sub := testSubscription(srv.URL + "/5")
	if status, _, err := d.send(context.Background(), sub, Delivery{ID: "d", Event: "e", Payload: "{}"}, 2*time.Second); err != nil || status != 200 {
		t.Fatalf("5 redirects: %d %v", status, err)
	}
	sub = testSubscription(srv.URL + "/6")
	if _, _, err := d.send(context.Background(), sub, Delivery{ID: "d", Event: "e", Payload: "{}"}, 2*time.Second); err == nil {
		t.Fatal("6 redirects followed")
	}
}

func TestErrTextDropsTheURL(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://x.example/h?token=SECRET", Err: errors.New("connection refused")}
	if got := errText(err); got != "connection refused" || strings.Contains(got, "SECRET") {
		t.Fatalf("errText = %q", got)
	}
}
