package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hatamoto.dev/core/internal/media"
)

// fileSteps restores what lives outside the database: media, secrets.env, then
// the config. A failure names the step; nothing is rolled back.
func (x *restorer) fileSteps(ctx context.Context, plan *RestorePlan) error {
	if err := x.restoreMedia(ctx); err != nil {
		return fmt.Errorf("restore stopped at media: %w", err)
	}
	if x.scan.hasSecrets {
		if err := x.writeSecrets(); err != nil {
			return fmt.Errorf("restore stopped at secrets.env: %w", err)
		}
	}
	if x.configTarget != "" {
		warn, err := x.writeConfig()
		if err != nil {
			return fmt.Errorf("restore stopped at config: %w", err)
		}
		if warn != "" {
			plan.Warnings = append(plan.Warnings, warn)
			plan.Would = withoutPrefix(plan.Would, "write config to ")
		}
	}
	return nil
}

func withoutPrefix(items []string, prefix string) []string {
	var out []string
	for _, s := range items {
		if !strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// entryReader opens the named archive entry; the caller closes it.
func (x *restorer) entryReader(name string) (io.Reader, io.Closer, error) {
	f, err := os.Open(x.src) // #nosec G304 -- operator supplied path
	if err != nil {
		return nil, nil, err
	}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			f.Close()
			return nil, nil, fmt.Errorf("%s is missing from the archive", name)
		}
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if h.Typeflag == tar.TypeReg && h.Name == name {
			return tr, f, nil
		}
	}
}

// restoreMedia writes the media/ entries of content.tar.gz through the local
// store, keyed by the name after the prefix. Unsafe names were refused by the
// checks; they are skipped here as well.
func (x *restorer) restoreMedia(ctx context.Context) error {
	if x.scan.mediaFiles == 0 {
		return nil
	}
	store, err := media.NewLocalStore(x.o.MediaDir, "/media")
	if err != nil {
		return err
	}
	r, c, err := x.entryReader(contentName)
	if err != nil {
		return err
	}
	defer c.Close()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		key, ok := strings.CutPrefix(h.Name, mediaPrefix)
		if !ok || h.Typeflag != tar.TypeReg || !safeKey(key) {
			continue
		}
		if _, err := store.Put(ctx, key, tr); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
}

// writeSecrets writes secrets.env (0600, dir 0700) atomically. The same bytes
// already there are a no-op; different bytes were refused by the checks and
// are refused again here.
func (x *restorer) writeSecrets() error {
	if x.o.SecretsDir == "" {
		return errors.New("no secrets dir")
	}
	target := filepath.Join(x.o.SecretsDir, secretsName)
	switch have, err := os.ReadFile(target); { // #nosec G304 -- operator supplied dir
	case err == nil && string(have) == string(x.scan.secrets):
		return nil
	case err == nil:
		return fmt.Errorf("%s already holds a different secrets.env", target)
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	if err := os.MkdirAll(x.o.SecretsDir, 0o700); err != nil {
		return err
	}
	tmp, err := writeTemp0600(x.o.SecretsDir, ".secrets-*.tmp", x.scan.secrets)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeConfig writes the recorded config to the target, only if it is free.
// A target that appeared since the checks is a warning, not a failure.
func (x *restorer) writeConfig() (warning string, err error) {
	r, c, err := x.entryReader(configName)
	if err != nil {
		return "", err
	}
	defer c.Close()
	out, err := os.OpenFile(x.configTarget, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) // #nosec G304 -- operator supplied path
	if errors.Is(err, os.ErrExist) {
		return fmt.Sprintf("%s exists and is not overwritten; the config is not written (use --config-out)", x.configTarget), nil
	}
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		os.Remove(x.configTarget)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(x.configTarget)
		return "", err
	}
	return "", nil
}
