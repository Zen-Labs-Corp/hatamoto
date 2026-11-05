package twofactor

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Byte pins for the JSON this package stores and sends: the pending sign-in
// token, the WebAuthn options sent to the browser, the ceremony session and the
// stored credential. Values were produced by the encoder in place before the
// platform JSON cutover. The library picks a random challenge and its own
// expiry, so those two members are replaced by a marker before comparing.

var pinUID = uuid.MustParse("11111111-2222-3333-4444-555555555555")

func pinPending() Pending {
	return Pending{UserID: pinUID, Next: "/admin?a=1&b=<2> é", Expires: time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC), ID: "AAAAAAAAAAAAAAAA"}
}

const (
	pinSecret         = "pin-secret"
	headToken         = `eyJ1IjoiMTExMTExMTEtMjIyMi0zMzMzLTQ0NDQtNTU1NTU1NTU1NTU1IiwibiI6Ii9hZG1pbj9hPTFcdTAwMjZiPVx1MDAzYzJcdTAwM2Ugw6kiLCJlIjoiMjA5OS0wMS0wMlQwMzowNDowNVoiLCJpIjoiQUFBQUFBQUFBQUFBQUFBQSJ9.luL0dp_qRR3-Km4dhBq9WJ6wzZ1G6vtqaSa9WzthAbE`
	headPayload       = `{"u":"11111111-2222-3333-4444-555555555555","n":"/admin?a=1\u0026b=\u003c2\u003e é","e":"2099-01-02T03:04:05Z","i":"AAAAAAAAAAAAAAAA"}`
	headTokenNoNext   = `eyJ1IjoiMTExMTExMTEtMjIyMi0zMzMzLTQ0NDQtNTU1NTU1NTU1NTU1IiwiZSI6IjIwOTktMDEtMDJUMDM6MDQ6MDVaIiwiaSI6IkFBQUFBQUFBQUFBQUFBQUEifQ.DwffdiluULd3OLb43RUmbxkM70mDb9BzmQNgTHKRiWc`
	headPayloadNoNext = `{"u":"11111111-2222-3333-4444-555555555555","e":"2099-01-02T03:04:05Z","i":"AAAAAAAAAAAAAAAA"}`
)

func tokenPayload(t *testing.T, token string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPendingOpensHeadToken(t *testing.T) {
	key := PendingKeyFor(pinSecret)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got, err := key.Open(headToken, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := pinPending(); got.UserID != want.UserID || got.Next != want.Next || !got.Expires.Equal(want.Expires) || got.ID != want.ID {
		t.Fatalf("pending = %+v, want %+v", got, want)
	}
	got, err = key.Open(headTokenNoNext, now)
	if err != nil || got.Next != "" || got.UserID != pinUID {
		t.Fatalf("pending without next = %+v %v", got, err)
	}
	if _, err := key.Open(headToken, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("an expired token must be refused")
	}
}

func TestPendingPayloadBytes(t *testing.T) {
	key := PendingKeyFor(pinSecret)
	p := pinPending()
	tok := key.Seal(p)
	if tok != headToken {
		t.Fatalf("token changed:\n got %s\nwant %s", tok, headToken)
	}
	if got := tokenPayload(t, tok); got != headPayload {
		t.Fatalf("payload changed:\n got %s\nwant %s", got, headPayload)
	}
	p.Next = ""
	tok = key.Seal(p)
	if tok != headTokenNoNext || tokenPayload(t, tok) != headPayloadNoNext {
		t.Fatalf("payload without next changed: %s", tokenPayload(t, tok))
	}
}

var (
	challengeRe = regexp.MustCompile(`"challenge":"[^"]*"`)
	expiresRe   = regexp.MustCompile(`"expires":"[^"]*"`)
	attestRe    = regexp.MustCompile(`,"attestation":\{.*\}\}$`)
)

func normalise(b []byte) string {
	s := challengeRe.ReplaceAllString(string(b), `"challenge":"CHALLENGE"`)
	return expiresRe.ReplaceAllString(s, `"expires":"EXPIRES"`)
}

const (
	headRegistrationOptions = `{"publicKey":{"rp":{"name":"Example","id":"example.com"},"user":{"name":"ana \u0026 \u003cco\u003e@example.com","displayName":"Ana é","id":"ERERESIiMzNERFVVVVVVVQ"},"challenge":"CHALLENGE","pubKeyCredParams":[{"type":"public-key","alg":-7},{"type":"public-key","alg":-35},{"type":"public-key","alg":-36},{"type":"public-key","alg":-257},{"type":"public-key","alg":-258},{"type":"public-key","alg":-259},{"type":"public-key","alg":-37},{"type":"public-key","alg":-38},{"type":"public-key","alg":-39},{"type":"public-key","alg":-8}],"timeout":120000,"authenticatorSelection":{"requireResidentKey":true,"residentKey":"required","userVerification":"required"},"attestation":"none"}}`
	headRegistrationSession = `{"challenge":"CHALLENGE","rpId":"example.com","user_id":"ERERESIiMzNERFVVVVVVVQ==","expires":"EXPIRES","userVerification":"required","credParams":[{"type":"public-key","alg":-7},{"type":"public-key","alg":-35},{"type":"public-key","alg":-36},{"type":"public-key","alg":-257},{"type":"public-key","alg":-258},{"type":"public-key","alg":-259},{"type":"public-key","alg":-37},{"type":"public-key","alg":-38},{"type":"public-key","alg":-39},{"type":"public-key","alg":-8}]}`
	headLoginOptions        = `{"publicKey":{"challenge":"CHALLENGE","timeout":120000,"rpId":"example.com","allowCredentials":[{"type":"public-key","id":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"}],"userVerification":"required"}}`
	headLoginSession        = `{"challenge":"CHALLENGE","rpId":"example.com","user_id":"ERERESIiMzNERFVVVVVVVQ==","allowed_credentials":["MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="],"expires":"EXPIRES","userVerification":"required"}`
	headDiscoverableOptions = `{"publicKey":{"challenge":"CHALLENGE","timeout":120000,"rpId":"example.com","userVerification":"required"}}`
	headDiscoverableSession = `{"challenge":"CHALLENGE","rpId":"example.com","expires":"EXPIRES","userVerification":"required"}`
	// headCredential is what registering the fixed-key authenticator stored;
	// headCredentialAfterLogin is the same row after one sign-in (sign count 1).
	headCredential           = `{"id":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=","publicKey":"pQECAyYgASFYIFFcPW6545a5BNP+yn9U/c0MwemXvzddylFa0KbDtANfIlggRTa+OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q=","attestationType":"none","attestationFormat":"none","flags":{"userPresent":true,"userVerified":true,"backupEligible":false,"backupState":false},"authenticator":{"AAGUID":"AAAAAAAAAAAAAAAAAAAAAA=="},"attestation":{"clientDataJSON":"eyJjaGFsbGVuZ2UiOiJQQmNoUEVPa1hMYWVUSDcyMzJXSlBmTU96WWdZUmJaYmtfOGFaX2c3RGxjIiwiY3Jvc3NPcmlnaW4iOmZhbHNlLCJvcmlnaW4iOiJodHRwczovL2V4YW1wbGUuY29tIiwidHlwZSI6IndlYmF1dGhuLmNyZWF0ZSJ9","clientDataHash":"sZTp10YlCiCna6qib7lBYMmfThUMRmZk1r/y4Dv6ba8=","object":"o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YViko3mm9u6vuaVeN4wRgDTidR5oL6ufLTCrE9ISVYbOGUdFAAAAAAAAAAAAAAAAAAAAAAAAAAAAIDAxMjM0NTY3ODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmpQECAyYgASFYIFFcPW6545a5BNP+yn9U/c0MwemXvzddylFa0KbDtANfIlggRTa+OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q="}}`
	headCredentialAfterLogin = `{"id":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=","publicKey":"pQECAyYgASFYIFFcPW6545a5BNP+yn9U/c0MwemXvzddylFa0KbDtANfIlggRTa+OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q=","attestationType":"none","attestationFormat":"none","flags":{"userPresent":true,"userVerified":true,"backupEligible":false,"backupState":false},"authenticator":{"AAGUID":"AAAAAAAAAAAAAAAAAAAAAA==","signCount":1},"attestation":{"clientDataJSON":"eyJjaGFsbGVuZ2UiOiJQQmNoUEVPa1hMYWVUSDcyMzJXSlBmTU96WWdZUmJaYmtfOGFaX2c3RGxjIiwiY3Jvc3NPcmlnaW4iOmZhbHNlLCJvcmlnaW4iOiJodHRwczovL2V4YW1wbGUuY29tIiwidHlwZSI6IndlYmF1dGhuLmNyZWF0ZSJ9","clientDataHash":"sZTp10YlCiCna6qib7lBYMmfThUMRmZk1r/y4Dv6ba8=","object":"o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YViko3mm9u6vuaVeN4wRgDTidR5oL6ufLTCrE9ISVYbOGUdFAAAAAAAAAAAAAAAAAAAAAAAAAAAAIDAxMjM0NTY3ODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmpQECAyYgASFYIFFcPW6545a5BNP+yn9U/c0MwemXvzddylFa0KbDtANfIlggRTa+OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q="}}`
	// headCredentialNoAttestation is headCredential without the attestation
	// member, which carries the random registration challenge.
	headCredentialNoAttestation = `{"id":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=","publicKey":"pQECAyYgASFYIFFcPW6545a5BNP+yn9U/c0MwemXvzddylFa0KbDtANfIlggRTa+OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q=","attestationType":"none","attestationFormat":"none","flags":{"userPresent":true,"userVerified":true,"backupEligible":false,"backupState":false},"authenticator":{"AAGUID":"AAAAAAAAAAAAAAAAAAAAAA=="}}`
)

// pinRig is a passkey rig with a fixed user and a fixed-key authenticator.
func pinRig(t *testing.T) *pkRig {
	t.Helper()
	r := newPKRig(t)
	r.uid = pinUID
	d := make([]byte, 32)
	for i := range d {
		d[i] = byte(i + 1)
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		t.Fatal(err)
	}
	r.va.key = k
	r.va.credID = []byte("0123456789abcdef0123456789abcdef")
	return r
}

func ceremonyData(t *testing.T, r *pkRig, id string) string {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	return normalise(r.repo.ceremony[u].Data)
}

func expect(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s changed:\n got %s\nwant %s", what, got, want)
	}
}

func TestPasskeyOptionsBytes(t *testing.T) {
	ctx := context.Background()
	r := pinRig(t)
	opts, cid, err := r.pk.BeginRegistration(ctx, r.uid, "ana & <co>@example.com", "Ana é")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "registration options", normalise(opts), headRegistrationOptions)
	expect(t, "registration session", ceremonyData(t, r, cid), headRegistrationSession)

	if _, err := r.pk.FinishRegistration(ctx, cid, r.uid, "Laptop", r.va.register(opts, testOrigin)); err != nil {
		t.Fatal(err)
	}
	list, _ := r.repo.ListPasskeys(ctx, r.uid)
	if len(list) != 1 {
		t.Fatalf("passkeys = %d", len(list))
	}
	expect(t, "stored credential", attestRe.ReplaceAllString(string(list[0].Credential), "}"), headCredentialNoAttestation)

	opts, cid, err = r.pk.BeginLogin(ctx, &r.uid)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "login options", normalise(opts), headLoginOptions)
	expect(t, "login session", ceremonyData(t, r, cid), headLoginSession)

	opts, cid, err = r.pk.BeginLogin(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "discoverable options", normalise(opts), headDiscoverableOptions)
	expect(t, "discoverable session", ceremonyData(t, r, cid), headDiscoverableSession)
}

// A credential row written before the cutover reads back: it is listed in the
// allow list, a sign-in with its key succeeds and the row is written back
// byte for byte except the new sign count.
func TestHeadCredentialReadsBackAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	r := pinRig(t)
	if _, err := r.repo.AddPasskey(ctx, Passkey{UserID: r.uid, CredentialID: r.va.credID, Credential: []byte(headCredential), Name: "Laptop"}); err != nil {
		t.Fatal(err)
	}
	opts, cid, err := r.pk.BeginLogin(ctx, &r.uid)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "login options", normalise(opts), headLoginOptions)
	r.va.counter = 0
	got, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: testOrigin}))
	if err != nil || got != r.uid {
		t.Fatalf("login = %v %v", got, err)
	}
	list, _ := r.repo.ListPasskeys(ctx, r.uid)
	expect(t, "credential after sign-in", string(list[0].Credential), headCredentialAfterLogin)
}
