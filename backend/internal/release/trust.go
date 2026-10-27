package release

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"hatamoto.dev/core/internal/packages"
)

//go:embed keys/*.pub
var keyFS embed.FS

func parseKey(b []byte) (packages.Key, error) {
	var k packages.Key
	if err := json.Unmarshal(b, &k); err != nil {
		return k, err
	}
	if k.Private != "" {
		return k, errors.New("release: private key material in embedded key")
	}
	pub, err := k.PublicKey()
	if err != nil || k.ID != packages.KeyID(pub) {
		return k, fmt.Errorf("release: bad key %q", k.ID)
	}
	return k, nil
}

// LoadTrust reads the embedded public keys: files named release-*.pub form the
// release set, manifest.pub and manifest-*.pub the manifest set.
func LoadTrust() (release, manifest packages.TrustSet, err error) {
	return loadTrust(keyFS, "keys")
}

func loadTrust(fsys fs.FS, dir string) (release, manifest packages.TrustSet, err error) {
	release, manifest = packages.TrustSet{}, packages.TrustSet{}
	files, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		name := f.Name()
		b, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, nil, err
		}
		k, err := parseKey(b)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		pub, _ := k.PublicKey()
		switch {
		case strings.HasPrefix(name, "release-"):
			release.Add(pub)
		case name == "manifest.pub" || (strings.HasPrefix(name, "manifest-") && strings.HasSuffix(name, ".pub")):
			manifest.Add(pub)
		default:
			return nil, nil, fmt.Errorf("release: unexpected embedded key file %q", name)
		}
	}
	return release, manifest, nil
}

var embeddedRelease, embeddedManifest packages.TrustSet

func init() {
	var err error
	if embeddedRelease, embeddedManifest, err = LoadTrust(); err != nil {
		panic(err)
	}
}

func clone(t packages.TrustSet) packages.TrustSet {
	c := make(packages.TrustSet, len(t))
	for id, pub := range t {
		c[id] = pub
	}
	return c
}

// ReleaseTrust returns the keys that may sign release records.
func ReleaseTrust() packages.TrustSet { return clone(embeddedRelease) }

// ManifestTrust returns the keys that may sign channel manifests.
func ManifestTrust() packages.TrustSet { return clone(embeddedManifest) }
