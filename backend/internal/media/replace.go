package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Replace swaps the bytes of an existing file and keeps everything that
// points at it: id, filename, storage path and public URL stay, so pages,
// links and embeds keep working. The new content must be the same kind of file
// (its sniffed type has to fit the stored file name's extension), an svg is
// sanitised, quota is checked on the size difference, and the variants are
// rebuilt. The old copies are set aside while the new ones are written and put
// back if the swap fails; they are removed only after the row names the new
// ones. Browsers and a CDN may keep the old bytes until the one-day cache
// revalidates.
func (s *Service) Replace(ctx context.Context, id uuid.UUID, file io.Reader, filename string, size int64) (*Media, error) {
	maxBytes := int64(s.config.MaxSizeMB) * 1024 * 1024
	if size > maxBytes {
		return nil, ErrFileTooLarge
	}
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// The room is the quota minus what is stored, plus the file being replaced.
	// Writing is also capped by it, so a body longer than declared cannot pass.
	limit := maxBytes
	if s.config.MaxTotalBytes > 0 {
		s.quotaMu.Lock()
		defer s.quotaMu.Unlock()
		room := s.config.MaxTotalBytes - dirSize(s.uploadRoot()) + m.FileSize
		if room <= 0 || size > room {
			return nil, ErrQuotaExceeded
		}
		limit = min(limit, room)
	}

	br := bufio.NewReaderSize(file, SniffLen)
	head, _ := br.Peek(SniffLen)
	sniffed := SniffUpload(head)
	if !ExtensionMatchesMIME(filepath.Ext(filename), sniffed) {
		return nil, ErrInvalidMimeType
	}
	if !ExtensionMatchesMIME(filepath.Ext(m.Filename), sniffed) {
		return nil, ErrReplaceKind
	}
	var body io.Reader = br
	var cleaned []string
	if sniffed == svgMIME {
		clean, removed, err := readSVG(br)
		if err != nil {
			return nil, err
		}
		body, cleaned = bytes.NewReader(clean), removed
	}

	if s.local == nil {
		return s.replaceInStore(ctx, m, body, sniffed, cleaned, limit, maxBytes)
	}

	tmpKey := path.Join(legacyMediaTenantDir, "replace-"+uuid.NewString()+".tmp")
	written, err := s.store.Put(ctx, tmpKey, io.LimitReader(body, limit+1))
	if err != nil {
		s.logger.Error("failed to store the replacement", slog.String("path", tmpKey), slog.String("error", err.Error()))
		return nil, ErrStorageFailed
	}
	committed := false
	defer func() {
		if !committed {
			s.removeBlob(ctx, tmpKey)
		}
	}()
	if written > limit {
		if limit < maxBytes {
			return nil, ErrQuotaExceeded
		}
		return nil, ErrFileTooLarge
	}

	// The live copies are moved aside while the new ones are written (they
	// share their names), and put back unless the swap completes.
	asideTag := "." + uuid.NewString() + ".old"
	var aside []string
	swapped := false
	defer func() {
		if swapped {
			return
		}
		for _, p := range aside {
			s.removeBlob(ctx, p)
			_ = s.local.rename(p+asideTag, p)
		}
	}()

	nm := *m
	nm.MimeType, nm.FileSize = sniffed, written
	nm.Width, nm.Height, nm.Variants, nm.Cleaned = nil, nil, []ImageVariant{}, cleaned
	if nm.IsImage() && sniffed != svgMIME {
		tmpPath := s.local.localPath(tmpKey)
		if err := s.enforcePixelCap(tmpPath); err != nil {
			return nil, err
		}
		if w, h, err := s.getImageDimensions(tmpPath); err == nil {
			nm.Width, nm.Height = &w, &h
		}
		if nm.SupportsVariants() {
			for _, v := range m.Variants {
				if err := s.local.rename(v.Path, v.Path+asideTag); err == nil {
					aside = append(aside, v.Path)
				}
			}
			variants, err := s.createVariants(ctx, &nm, tmpPath)
			if errors.Is(err, ErrImageTooLarge) {
				return nil, err
			} else if err != nil {
				s.logger.Warn("failed to create variants", slog.String("error", err.Error()))
			} else {
				nm.Variants = variants
			}
		}
	}

	// Swap: keep the old original aside until the row is updated, so a failure
	// puts it back.
	backupKey := tmpKey + ".old"
	hadOld := true
	if err := s.local.rename(m.StoragePath, backupKey); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, ErrStorageFailed
		}
		hadOld = false
	}
	if err := s.local.rename(tmpKey, m.StoragePath); err != nil {
		if hadOld {
			_ = s.local.rename(backupKey, m.StoragePath)
		}
		return nil, ErrStorageFailed
	}
	committed = true
	if err := s.repo.UpdateFile(ctx, &nm); err != nil {
		if hadOld {
			_ = s.local.rename(backupKey, m.StoragePath)
		} else {
			s.removeBlob(ctx, m.StoragePath)
		}
		return nil, err
	}
	swapped = true
	if hadOld {
		s.removeBlob(ctx, backupKey)
	}
	for _, p := range aside {
		s.removeBlob(ctx, p+asideTag)
	}

	// Copies the new list no longer names are dropped after cached pages have
	// been purged.
	keep := make(map[string]bool, len(nm.Variants))
	for _, v := range nm.Variants {
		keep[v.Path] = true
	}
	s.notifyChanged(ctx)
	for _, v := range m.Variants {
		if !keep[v.Path] {
			s.removeBlob(ctx, v.Path)
		}
	}
	return &nm, nil
}

// rollbackTimeout bounds the object storage calls of a replace rollback.
const rollbackTimeout = time.Minute

// replaceInStore is Replace for a store without rename. The new bytes are
// spooled to a temporary file and checked there; only then is the bucket
// touched. An object write is atomic, so the original is overwritten in place
// (no window with a partial file, and no temporary objects left to collect).
// The old original and variants are kept in temporary files and put back if
// the rebuild or the row update fails.
func (s *Service) replaceInStore(ctx context.Context, m *Media, body io.Reader, sniffed string, cleaned []string, limit, maxBytes int64) (*Media, error) {
	newTmp, written, err := spoolTemp(io.LimitReader(body, limit+1))
	if newTmp != "" {
		defer os.Remove(newTmp)
	}
	if err != nil {
		s.logger.Error("failed to spool the replacement", slog.String("error", err.Error()))
		return nil, ErrStorageFailed
	}
	if written > limit {
		if limit < maxBytes {
			return nil, ErrQuotaExceeded
		}
		return nil, ErrFileTooLarge
	}

	nm := *m
	nm.MimeType, nm.FileSize = sniffed, written
	nm.Width, nm.Height, nm.Variants, nm.Cleaned = nil, nil, []ImageVariant{}, cleaned
	if nm.IsImage() && sniffed != svgMIME {
		if err := s.enforcePixelCap(newTmp); err != nil {
			return nil, err
		}
		if w, h, err := s.getImageDimensions(newTmp); err == nil {
			nm.Width, nm.Height = &w, &h
		}
	}

	// Keep what is there now, so a failure can put it back.
	oldKeys := []string{m.StoragePath}
	for _, v := range m.Variants {
		oldKeys = append(oldKeys, v.Path)
	}
	snap, err := s.snapshot(ctx, oldKeys)
	defer snap.discard()
	if err != nil {
		s.logger.Error("failed to read the current file", slog.String("path", m.StoragePath), slog.String("error", err.Error()))
		return nil, ErrStorageFailed
	}
	// fail puts the old objects back and removes new variants the old list did
	// not have.
	fail := func(newVariants []ImageVariant, err error) (*Media, error) {
		// A client that went away must not stop the rollback: the bucket
		// would keep the new bytes next to the old row.
		rb, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		for _, v := range newVariants {
			if _, had := snap.files[v.Path]; !had {
				s.removeBlob(rb, v.Path)
			}
		}
		if !snap.has(m.StoragePath) {
			s.removeBlob(rb, m.StoragePath)
		}
		snap.restore(rb, s.store, s.logger)
		return nil, err
	}

	if err := putFile(ctx, s.store, m.StoragePath, newTmp); err != nil {
		s.logger.Error("failed to store the replacement", slog.String("path", m.StoragePath), slog.String("error", err.Error()))
		return fail(nil, ErrStorageFailed)
	}
	if nm.IsImage() && sniffed != svgMIME && nm.SupportsVariants() {
		// The new copies take the names of the live ones: let the cleanup of a
		// failed write see the live list, then start from none again.
		nm.Variants = m.Variants
		variants, err := s.createVariants(ctx, &nm, newTmp)
		nm.Variants = []ImageVariant{}
		if errors.Is(err, ErrImageTooLarge) {
			return fail(variants, err)
		} else if err != nil {
			s.logger.Warn("failed to create variants", slog.String("error", err.Error()))
		} else {
			nm.Variants = variants
		}
	}
	if err := s.repo.UpdateFile(ctx, &nm); err != nil {
		return fail(nm.Variants, err)
	}

	keep := make(map[string]bool, len(nm.Variants))
	for _, v := range nm.Variants {
		keep[v.Path] = true
	}
	s.notifyChanged(ctx)
	for _, v := range m.Variants {
		if !keep[v.Path] {
			s.removeBlob(ctx, v.Path)
		}
	}
	return &nm, nil
}
