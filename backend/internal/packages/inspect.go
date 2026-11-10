package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"hatamoto.dev/sdk"
)

// Inspector gives the verdict of the packages.inspect host function: what a
// downloaded .hmp is and whether a given key signed it. It reports trouble as
// Problems, never as an error, and never returns any of the package bytes.
type Inspector struct {
	Limits Limits // a zero field means the DefaultLimits value
}

func (i Inspector) limits() Limits {
	l, d := i.Limits, DefaultLimits
	if l.Archive == 0 {
		l.Archive = d.Archive
	}
	if l.Entries == 0 {
		l.Entries = d.Entries
	}
	if l.File == 0 {
		l.File = d.File
	}
	if l.Total == 0 {
		l.Total = d.Total
	}
	if l.Ratio == 0 {
		l.Ratio = d.Ratio
	}
	if l.RatioFloor == 0 {
		l.RatioFloor = d.RatioFloor
	}
	if l.Path == 0 {
		l.Path = d.Path
	}
	return l
}

// ArchiveLimit is the largest archive Inspect accepts, in bytes.
func (i Inspector) ArchiveLimit() int64 { return i.limits().Archive }

// Inspect parses data and checks its signature against signerKey (a base64
// ed25519 public key). The package is never installed or trusted by this call.
func (i Inspector) Inspect(data []byte, signerKey string) sdk.InspectResult {
	lim := i.limits()
	sum := sha256.Sum256(data)
	res := sdk.InspectResult{
		SHA256:   hex.EncodeToString(sum[:]),
		Size:     int64(len(data)),
		Problems: []string{},
	}
	if res.Size > lim.Archive {
		res.Problems = append(res.Problems, fmt.Sprintf("too large: over %d bytes", lim.Archive))
		return res
	}
	p, err := ParseNew(data, lim)
	if err != nil {
		msg := err.Error()
		var pe *ParseError
		if errors.As(err, &pe) {
			msg = pe.Msg
		}
		res.Problems = append(res.Problems, msg)
		return res
	}
	res.Kind, res.Name, res.Version = string(p.Kind), p.Name, p.Version
	if problem := describe(&res, p); problem != "" {
		res.Problems = append(res.Problems, problem)
	}
	if p.Signature != nil {
		res.SignerKeyID = p.Signature.Signer
	}
	if problem := checkSigner(p, signerKey); problem != "" {
		res.Problems = append(res.Problems, problem)
		return res
	}
	res.SignatureOK = true
	return res
}

// describe fills the manifest fields of res; it returns a problem when the
// manifest cannot be read.
func describe(res *sdk.InspectResult, p *Package) string {
	switch p.Kind {
	case KindPlugin:
		m, err := p.PluginManifest()
		if err != nil {
			return fmt.Sprintf("%s: %v", PluginManifest, err)
		}
		res.SDK, res.Description, res.AllowedHosts = m.SDK, m.Description, m.AllowedHosts
		for _, perm := range m.Permissions {
			res.Permissions = append(res.Permissions, string(perm))
		}
	case KindTheme:
		m, err := p.ThemeMeta()
		if err != nil {
			return fmt.Sprintf("%s: %v", ThemeManifest, err)
		}
		res.Description = m.Description
	}
	return ""
}

// checkSigner answers "" when signerKey signed p, else why not.
func checkSigner(p *Package, signerKey string) string {
	if p.Signature == nil {
		return "package is unsigned"
	}
	if signerKey == "" {
		return "no signer key given"
	}
	pub, err := ParsePublicKey(signerKey)
	if err != nil {
		return "signer_key: " + err.Error()
	}
	if id := KeyID(pub); id != p.Signature.Signer {
		return fmt.Sprintf("signed by %s not by the key you gave (%s)", p.Signature.Signer, id)
	}
	if err := (TrustSet{KeyID(pub): pub}).Verify(p); err != nil {
		return "signature does not match the package contents"
	}
	return ""
}
