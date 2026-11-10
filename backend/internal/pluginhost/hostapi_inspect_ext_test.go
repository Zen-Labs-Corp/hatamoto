package pluginhost_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

const acmeManifest = `{"name":"acme","version":"1.2.0","sdk":"^0.7","description":"Acme tools","permissions":["http.fetch"],"allowed_hosts":["api.example.com"]}`

func acmeFiles() map[string][]byte {
	return map[string][]byte{
		packages.PluginManifest: []byte(acmeManifest),
		packages.PluginWasm:     []byte("\x00asm\x01\x00\x00\x00"),
	}
}

func newInspectKey(t *testing.T) packages.Key {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pack(t *testing.T, key *packages.Key) []byte {
	t.Helper()
	raw, err := packages.PackFiles(acmeFiles(), key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// tamper rewrites the wasm of a signed package and keeps its signature.
func tamper(t *testing.T, signed []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == packages.PluginWasm {
			body = append(body, 0)
		}
		w, _ := zw.Create(f.Name)
		_, _ = w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// inspectOnce serves body over TLS and runs packages.inspect against it.
func inspectOnce(t *testing.T, body []byte, in packages.Inspector, signerKey string) (sdk.InspectResult, []byte) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	fetcher := pluginhost.NewFetcherForTestsTLS(srv.Client().Transport.(*http.Transport).TLSClientConfig)
	g := pluginhost.NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", []sdk.Permission{sdk.PermPackagesInspect}); err != nil {
		t.Fatal(err)
	}
	m := sdk.Manifest{Name: "p", AllowedHosts: []string{srv.Listener.Addr().String()}}
	a := pluginhost.NewAPI(pluginhost.Services{Fetcher: fetcher, Packages: in}, g, func(string) (sdk.Manifest, bool) { return m, true }, nil)
	args, _ := jsonx.MarshalPinned(sdk.InspectRequest{URL: srv.URL + "/acme.hmp", SignerKey: signerKey})
	req, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: sdk.FnPackagesInspect, Args: jsontext.Value(args)})
	var resp sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(context.Background(), "p", req), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("call failed: %+v", resp.Error)
	}
	var res sdk.InspectResult
	if err := jsonx.UnmarshalPinned(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res, resp.Result
}

func TestInspectSignedPackage(t *testing.T) {
	k := newInspectKey(t)
	raw := pack(t, &k)
	res, wire := inspectOnce(t, raw, packages.Inspector{}, k.Public)
	if !res.SignatureOK || len(res.Problems) != 0 {
		t.Fatalf("verdict: %+v", res)
	}
	if res.Kind != "plugin" || res.Name != "acme" || res.Version != "1.2.0" || res.SDK != "^0.7" ||
		res.Description != "Acme tools" || res.Size != int64(len(raw)) || res.SignerKeyID != k.ID {
		t.Fatalf("fields: %+v", res)
	}
	if len(res.Permissions) != 1 || res.Permissions[0] != "http.fetch" || len(res.AllowedHosts) != 1 || res.AllowedHosts[0] != "api.example.com" {
		t.Fatalf("manifest fields: %+v", res)
	}
	if len(wire) > 2048 || bytes.Contains(wire, []byte("asm")) {
		t.Fatalf("result looks like it carries the body (%d bytes)", len(wire))
	}
}

func TestInspectOtherKeyNamesRealSigner(t *testing.T) {
	signer, other := newInspectKey(t), newInspectKey(t)
	res, _ := inspectOnce(t, pack(t, &signer), packages.Inspector{}, other.Public)
	if res.SignatureOK {
		t.Fatal("signature_ok for another key")
	}
	if !strings.Contains(strings.Join(res.Problems, "|"), signer.ID) {
		t.Fatalf("problems %v do not name the real signer %s", res.Problems, signer.ID)
	}
}

func TestInspectTamperedPackage(t *testing.T) {
	k := newInspectKey(t)
	res, _ := inspectOnce(t, tamper(t, pack(t, &k)), packages.Inspector{}, k.Public)
	if res.SignatureOK || len(res.Problems) == 0 {
		t.Fatalf("tampered package passed: %+v", res)
	}
}

func TestInspectUnsignedAndCorrupt(t *testing.T) {
	k := newInspectKey(t)
	res, _ := inspectOnce(t, pack(t, nil), packages.Inspector{}, k.Public)
	if res.SignatureOK || len(res.Problems) == 0 {
		t.Fatalf("unsigned: %+v", res)
	}
	res, _ = inspectOnce(t, []byte("not a zip"), packages.Inspector{}, k.Public)
	if res.SignatureOK || len(res.Problems) == 0 || res.Name != "" {
		t.Fatalf("corrupt: %+v", res)
	}
}

func TestInspectStopsAtTheArchiveLimit(t *testing.T) {
	k := newInspectKey(t)
	raw := pack(t, &k)
	limit := int64(len(raw) / 2)
	var sent atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 1024)
		for range 4096 { // 4 MiB on offer
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	g := pluginhost.NewGrantStore(nil)
	_ = g.Set(context.Background(), "p", []sdk.Permission{sdk.PermPackagesInspect})
	m := sdk.Manifest{Name: "p", AllowedHosts: []string{srv.Listener.Addr().String()}}
	svc := pluginhost.Services{
		Fetcher:  pluginhost.NewFetcherForTestsTLS(srv.Client().Transport.(*http.Transport).TLSClientConfig),
		Packages: packages.Inspector{Limits: packages.Limits{Archive: limit}},
	}
	a := pluginhost.NewAPI(svc, g, func(string) (sdk.Manifest, bool) { return m, true }, nil)
	args, _ := jsonx.MarshalPinned(sdk.InspectRequest{URL: srv.URL + "/big.hmp", SignerKey: k.Public})
	req, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: sdk.FnPackagesInspect, Args: jsontext.Value(args)})
	var resp sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(context.Background(), "p", req), &resp); err != nil || !resp.OK {
		t.Fatalf("call: %v %+v", err, resp.Error)
	}
	var res sdk.InspectResult
	if err := jsonx.UnmarshalPinned(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !strings.HasPrefix(res.Problems[0], "too large") {
		t.Fatalf("problems: %v", res.Problems)
	}
	if res.SignatureOK || res.Name != "" {
		t.Fatalf("verdict on an oversized body: %+v", res)
	}
}
