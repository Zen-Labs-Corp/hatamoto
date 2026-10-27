package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"hatamoto.dev/core/internal/packages"
)

// signer produces a raw ed25519 signature over msg without exposing the key.
type signer interface {
	PublicKey() ed25519.PublicKey
	Sign(msg []byte) ([]byte, error)
}

// fileSigner signs with a key read from a JSON key file.
type fileSigner struct {
	key packages.Key
	pub ed25519.PublicKey
}

func newFileSigner(path string) (*fileSigner, error) {
	k, err := loadKeyFile(path)
	if err != nil {
		return nil, err
	}
	pub, err := k.PublicKey()
	if err != nil {
		return nil, err
	}
	return &fileSigner{key: k, pub: pub}, nil
}

func (s *fileSigner) PublicKey() ed25519.PublicKey { return s.pub }

func (s *fileSigner) Sign(msg []byte) ([]byte, error) {
	sig, err := packages.SignBytes(s.key, msg)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(sig.Signature)
}

// agentSigner signs through an ssh-agent; the private key never leaves it.
type agentSigner struct {
	c   agent.ExtendedAgent
	key *agent.Key
	pub ed25519.PublicKey
}

// newAgentSigner picks the agent's ed25519 key whose raw public key is in
// trusted (and whose key id is wantID, when given).
func newAgentSigner(c agent.ExtendedAgent, trusted packages.TrustSet, wantID string) (*agentSigner, error) {
	keys, err := c.List()
	if err != nil {
		return nil, fmt.Errorf("list agent keys: %w", err)
	}
	for _, k := range keys {
		parsed, err := ssh.ParsePublicKey(k.Blob)
		if err != nil {
			continue
		}
		cp, ok := parsed.(ssh.CryptoPublicKey)
		if !ok {
			continue
		}
		pub, ok := cp.CryptoPublicKey().(ed25519.PublicKey)
		if !ok {
			continue
		}
		id := packages.KeyID(pub)
		if _, ok := trusted[id]; !ok || (wantID != "" && id != wantID) {
			continue
		}
		return &agentSigner{c: c, key: k, pub: pub}, nil
	}
	return nil, errors.New("the agent holds no ed25519 key that is a trusted release key")
}

func (s *agentSigner) PublicKey() ed25519.PublicKey { return s.pub }

func (s *agentSigner) Sign(msg []byte) ([]byte, error) {
	sig, err := s.c.Sign(s.key, msg)
	if err != nil {
		return nil, err
	}
	return sig.Blob, nil
}

// encodeRecordSig renders a record signature as "<key_id>:<base64>".
func encodeRecordSig(pub ed25519.PublicKey, raw []byte) string {
	s := packages.NewSignature(pub, raw)
	return s.Signer + ":" + s.Signature
}
