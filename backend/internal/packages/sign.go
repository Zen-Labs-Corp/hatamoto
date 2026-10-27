package packages

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Algorithm is the only signature algorithm of packages and registry indexes.
const Algorithm = "ed25519"

// Signature is signature.json inside a package: who signed and the ed25519
// signature over the package digest (see Package.Digest).
type Signature struct {
	Signer    string `json:"signer"` // KeyID of the signing key
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"` // base64
}

// KeyID names a public key: the first 16 hex characters of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Key is a signing key as `hatamoto keys generate` writes it.
type Key struct {
	ID      string `json:"key_id"`
	Public  string `json:"public"`            // base64, 32 bytes
	Private string `json:"private,omitempty"` // base64, 64 bytes; absent from a public key file
}

// GenerateKey makes a new signing key.
func GenerateKey() (Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	return Key{
		ID:      KeyID(pub),
		Public:  base64.StdEncoding.EncodeToString(pub),
		Private: base64.StdEncoding.EncodeToString(priv),
	}, nil
}

// PrivateKey decodes the signing half.
func (k Key) PrivateKey() (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(k.Private)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("packages: not an ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

// PublicKey decodes the verifying half.
func (k Key) PublicKey() (ed25519.PublicKey, error) { return ParsePublicKey(k.Public) }

// ParsePublicKey decodes a base64 ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("packages: not an ed25519 public key (want 32 bytes, base64)")
	}
	return ed25519.PublicKey(b), nil
}

// TrustSet is the signer keys the owner trusts, by key id.
type TrustSet map[string]ed25519.PublicKey

// ParseTrustSet reads a comma- or space-separated list of base64 public keys.
func ParseTrustSet(list string) (TrustSet, error) {
	t := TrustSet{}
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		pub, err := ParsePublicKey(f)
		if err != nil {
			return nil, err
		}
		t[KeyID(pub)] = pub
	}
	return t, nil
}

// Add trusts pub.
func (t TrustSet) Add(pub ed25519.PublicKey) { t[KeyID(pub)] = pub }

// Signature errors Verify answers; the manager shows them as they are.
var (
	ErrUnsigned      = errors.New("package is not signed")
	ErrUntrustedKey  = errors.New("package is signed by a key you do not trust")
	ErrBadSignature  = errors.New("package signature does not match its contents")
	errAlgorithmName = errors.New("unsupported signature algorithm")
)

// Verify checks the package's embedded signature against the trusted keys. It
// returns ErrUnsigned for a package without one, ErrUntrustedKey for an unknown
// signer and ErrBadSignature when the contents do not match.
func (t TrustSet) Verify(p *Package) error {
	s := p.Signature
	if s == nil {
		return ErrUnsigned
	}
	return t.VerifyBytes(s.Signer, s.Algorithm, s.Signature, p.Digest[:])
}

// VerifyBytes checks a base64 signature over msg made by the named signer.
// algorithm may be empty (ed25519). Errors are ErrUntrustedKey for a signer
// outside the set and ErrBadSignature when msg does not match.
func (t TrustSet) VerifyBytes(signer, algorithm, sig string, msg []byte) error {
	if algorithm != "" && algorithm != Algorithm {
		return fmt.Errorf("%w %q", errAlgorithmName, algorithm)
	}
	pub, ok := t[signer]
	if !ok {
		return fmt.Errorf("%w (key %s)", ErrUntrustedKey, signer)
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, msg, raw) {
		return ErrBadSignature
	}
	return nil
}

// SignBytes signs msg, any bytes, with k.
func SignBytes(k Key, msg []byte) (Signature, error) {
	priv, err := k.PrivateKey()
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(priv.Public().(ed25519.PublicKey), ed25519.Sign(priv, msg)), nil
}

// NewSignature wraps a raw ed25519 signature made by the holder of pub, for
// signers that never expose their private key (an ssh-agent).
func NewSignature(pub ed25519.PublicKey, rawSig []byte) Signature {
	return Signature{
		Signer: KeyID(pub), Algorithm: Algorithm,
		Signature: base64.StdEncoding.EncodeToString(rawSig),
	}
}

// --- the registry index envelope ---

// indexEnvelope is registry.json: the index and a signature over its compact
// JSON form.
type indexEnvelope struct {
	Index     json.RawMessage `json:"index"`
	Signer    string          `json:"signer"`
	Algorithm string          `json:"algorithm,omitempty"`
	Signature string          `json:"signature"`
}

// SignIndex wraps index (the JSON of an Index) in a signed envelope.
func SignIndex(index []byte, k Key) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, index); err != nil {
		return nil, err
	}
	s, err := SignBytes(k, compact.Bytes())
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(indexEnvelope{
		Index: compact.Bytes(), Signer: s.Signer, Algorithm: s.Algorithm, Signature: s.Signature,
	}, "", "  ")
}

// VerifyIndex checks a registry.json envelope and returns the index JSON.
func (t TrustSet) VerifyIndex(doc []byte) (json.RawMessage, error) {
	var env indexEnvelope
	if err := json.Unmarshal(doc, &env); err != nil {
		return nil, fmt.Errorf("registry index: %w", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, env.Index); err != nil || compact.Len() == 0 {
		return nil, errors.New("registry index: missing index")
	}
	if env.Signature == "" {
		return nil, fmt.Errorf("registry index: %w", ErrUnsigned)
	}
	if err := t.VerifyBytes(env.Signer, env.Algorithm, env.Signature, compact.Bytes()); err != nil {
		return nil, fmt.Errorf("registry index: %w", err)
	}
	return compact.Bytes(), nil
}

func marshalIndented(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
