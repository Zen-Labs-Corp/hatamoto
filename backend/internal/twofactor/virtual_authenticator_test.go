package twofactor

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// virtualAuthenticator is a test-only software authenticator: ES256 keys,
// "none" attestation, a settable sign counter and user verification flag.
type virtualAuthenticator struct {
	t        *testing.T
	key      *ecdsa.PrivateKey
	credID   []byte
	userID   []byte
	counter  uint32
	verified bool // sets the UV flag
}

func newVirtualAuthenticator(t *testing.T) *virtualAuthenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &virtualAuthenticator{t: t, key: k, credID: id, verified: true}
}

var b64 = base64.RawURLEncoding

const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

func (v *virtualAuthenticator) flags(extra byte) byte {
	f := byte(flagUP) | extra
	if v.verified {
		f |= flagUV
	}
	return f
}

func (v *virtualAuthenticator) coseKey() []byte {
	pub, err := v.key.PublicKey.Bytes() // uncompressed point: 0x04 || X || Y
	if err != nil {
		v.t.Fatal(err)
	}
	x, y := pub[1:33], pub[33:65]
	b, err := cbor.CTAP2EncOptions().EncMode()
	if err != nil {
		v.t.Fatal(err)
	}
	out, err := b.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		v.t.Fatal(err)
	}
	return out
}

func rpHash(rpID string) []byte { h := sha256.Sum256([]byte(rpID)); return h[:] }

// creationOptions is the part of the begin response the authenticator reads.
type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Selection struct {
			UserVerification string `json:"userVerification"`
			ResidentKey      string `json:"residentKey"`
		} `json:"authenticatorSelection"`
		Attestation string `json:"attestation"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge        string `json:"challenge"`
		RPID             string `json:"rpId"`
		UserVerification string `json:"userVerification"`
		Allow            []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
	} `json:"publicKey"`
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// register answers a creation ceremony and returns the request body to post.
func (v *virtualAuthenticator) register(optionsJSON []byte, origin string) *http.Request {
	v.t.Helper()
	var o creationOptions
	if err := json.Unmarshal(optionsJSON, &o); err != nil {
		v.t.Fatal(err)
	}
	v.userID, _ = b64.DecodeString(o.PublicKey.User.ID)
	authData := append(rpHash(o.PublicKey.RP.ID), v.flags(flagAT))
	authData = binary.BigEndian.AppendUint32(authData, v.counter)
	authData = append(authData, make([]byte, 16)...) // AAGUID
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(v.credID)))
	authData = append(authData, v.credID...)
	authData = append(authData, v.coseKey()...)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	if err != nil {
		v.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(v.credID), "rawId": b64.EncodeToString(v.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", o.PublicKey.Challenge, origin)),
			"attestationObject": b64.EncodeToString(att),
		},
	})
	return httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
}

// assertOptions are the knobs a test turns to make a bad assertion.
type assertOptions struct {
	Origin     string
	UserHandle bool
	SetCounter *uint32 // overrides the auto-incremented counter
}

// assert answers a request ceremony.
func (v *virtualAuthenticator) assert(optionsJSON []byte, o assertOptions) *http.Request {
	v.t.Helper()
	var ro requestOptions
	if err := json.Unmarshal(optionsJSON, &ro); err != nil {
		v.t.Fatal(err)
	}
	if o.SetCounter != nil {
		v.counter = *o.SetCounter
	} else {
		v.counter++
	}
	authData := append(rpHash(ro.PublicKey.RPID), v.flags(0))
	authData = binary.BigEndian.AppendUint32(authData, v.counter)
	cd := clientData("webauthn.get", ro.PublicKey.Challenge, o.Origin)
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, v.key, digest[:])
	if err != nil {
		v.t.Fatal(err)
	}
	resp := map[string]any{
		"clientDataJSON":    b64.EncodeToString(cd),
		"authenticatorData": b64.EncodeToString(authData),
		"signature":         b64.EncodeToString(sig),
	}
	if o.UserHandle {
		resp["userHandle"] = b64.EncodeToString(v.userID)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(v.credID), "rawId": b64.EncodeToString(v.credID), "type": "public-key", "response": resp,
	})
	return httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
}
