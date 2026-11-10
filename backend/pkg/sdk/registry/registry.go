// Package registry is the author side of a Hatamoto package registry: the
// index types and the signing a registry publishes with. SignIndex produces
// the same bytes as the server's own signer, so a server that trusts the
// signer key accepts the document.
package registry

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
)

// algorithm is the only signature algorithm an envelope names.
const algorithm = "ed25519"

// KeyID is the short identifier of a signer key: the hex of the first eight
// bytes of the SHA-256 of the public key. Signatures name their signer by it.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Index is the document a registry publishes (sign it with SignIndex).
//
// Revoked is reserved: hosts up to SDK 0.21 and the core release that ships it
// ignore it, so a listed revocation is not enforced yet. A later core release
// will enforce it.
type Index struct {
	Version   int          `json:"version"`
	IssuedAt  string       `json:"issued_at"`
	ExpiresAt string       `json:"expires_at"`
	Packages  []Entry      `json:"packages"`
	Revoked   []Revocation `json:"revoked,omitempty"`
}

// Revocation withdraws trust from a signer key, or from one package version
// it signed. It is reserved: hosts up to SDK 0.21 ignore it (see Index.Revoked).
type Revocation struct {
	SignerKey string `json:"signer_key,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Entry is one package a registry offers.
type Entry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// URL of the .hmp, absolute or relative to the registry index.
	URL string `json:"url"`
	// SHA256 is the hex hash of the .hmp (checked after the download).
	SHA256      string   `json:"sha256"`
	Size        int64    `json:"size,omitzero"`
	Permissions []string `json:"permissions,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	// Title is an optional display name for the directory page.
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Source string `json:"source,omitempty"`
	Commit string `json:"commit,omitempty"`
	Tier   string `json:"tier,omitempty"`
	// Screenshot is a registry-relative path to a theme preview image.
	Screenshot string `json:"screenshot,omitempty"`
	// About (plain text, paragraphs split by a single newline) and Features
	// feed the public detail page.
	About    string   `json:"about,omitempty"`
	Features []string `json:"features,omitempty"`
	// Category and Featured feed the admin Browse page.
	Category string `json:"category,omitempty"`
	Featured bool   `json:"featured,omitzero"`
	// SiteType and Flags feed the admin theme Browse page.
	SiteType string   `json:"site_type,omitempty"`
	Flags    []string `json:"flags,omitempty"`
	// Tags summarises the tag declarations of a tag pack.
	Tags []TagSummary `json:"tags,omitempty"`
	// SignerKey is the base64 ed25519 public key of the package's author. It is
	// reserved: hosts up to SDK 0.21 ignore it, a later core release will enforce
	// it.
	SignerKey string `json:"signer_key,omitempty"`
}

// TagSummary is one tag of a tag pack as a registry lists it.
type TagSummary struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Hosts    []string `json:"hosts"`
}

// envelope is registry.json: the index and a signature over its compact form.
type envelope struct {
	Index     jsontext.Value `json:"index"`
	Signer    string         `json:"signer"`
	Algorithm string         `json:"algorithm,omitempty"`
	Signature string         `json:"signature"`
}

// SignIndex wraps index (the JSON of an Index) in a signed envelope. The index
// is signed in its compact form, with <, >, & and U+2028/2029 written as \u
// escapes, and embedded in exactly those bytes. Numbers and other escapes are
// never rewritten. The result has no trailing newline.
func SignIndex(index []byte, priv ed25519.PrivateKey) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("registry: private key must be a 64-byte ed25519 key")
	}
	compact := jsontext.Value(bytes.Clone(index))
	if err := compact.Compact(
		jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true),
		jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true),
	); err != nil {
		return nil, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	return jsonv2.Marshal(envelope{
		Index:     compact,
		Signer:    KeyID(pub),
		Algorithm: algorithm,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, compact)),
	},
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true),
		jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true),
		jsontext.PreserveRawStrings(true), jsontext.WithIndent("  "))
}
