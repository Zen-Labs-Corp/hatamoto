package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// stagedDir is the directory, under the media tenant directory, that holds
// the files of changesets that are not published yet, one subdirectory per
// run. Staged files have no database row, are not counted by the total quota
// and are not served from a media URL by anything that lists the library.
const stagedDir = "staged"

// stagedPrefix is the blob key prefix of the staged files of one run.
func stagedPrefix(runID uuid.UUID) string {
	return path.Join(legacyMediaTenantDir, stagedDir, runID.String())
}

// StagedPrefix is the blob key prefix of the staged files of one run: a key
// that does not start with it plus "/" is not one of that run's files.
func StagedPrefix(runID uuid.UUID) string { return stagedPrefix(runID) }

// isStagedKey reports whether key lives under a run's staged directory.
func isStagedKey(key string) bool {
	return strings.HasPrefix(key, legacyMediaTenantDir+"/"+stagedDir+"/")
}

// MediaOp describes a staged file to publish as a media record.
type MediaOp struct {
	// StagedKey is the key StageBlob returned.
	StagedKey string
	// Filename is the name the file had when it was staged; its extension must
	// match the sniffed type, as for an upload.
	Filename string
	// Origin owns the new record ("owner" or a plugin name).
	Origin  string
	AltText *string
	Caption *string
	Credit  *string
	// Folder defaults to "/".
	Folder     string
	UploadedBy *uuid.UUID
}

// StageBlob stores r under the staged directory of runID and returns its key.
// It applies the checks of Upload (size, sniffed type against the extension,
// total quota, pixel cap) but writes no database row: the file becomes media
// only through AttachStagedTx.
func (s *Service) StageBlob(ctx context.Context, runID uuid.UUID, r io.Reader, filename string) (string, error) {
	maxBytes := int64(s.config.MaxSizeMB) * 1024 * 1024
	br := bufio.NewReaderSize(r, SniffLen)
	head, _ := br.Peek(SniffLen)
	sniffed := SniffUpload(head)
	if !ExtensionMatchesMIME(filepath.Ext(filename), sniffed) {
		return "", ErrInvalidMimeType
	}

	src := io.Reader(br)
	if sniffed == svgMIME {
		clean, _, err := readSVG(br)
		if err != nil {
			return "", err
		}
		src = bytes.NewReader(clean)
	}
	if maxBytes > 0 {
		src = io.LimitReader(src, maxBytes+1)
	}
	key := path.Join(stagedPrefix(runID), s.generateFilename(filepath.Ext(filename)))
	written, err := s.store.Put(ctx, key, src)
	if err != nil {
		s.logger.Error("failed to stage file", "path", key, "error", err.Error())
		return "", ErrStorageFailed
	}
	if maxBytes > 0 && written > maxBytes {
		s.removeBlob(ctx, key)
		return "", ErrFileTooLarge
	}
	if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot())+written > s.config.MaxTotalBytes {
		s.removeBlob(ctx, key)
		return "", ErrQuotaExceeded
	}
	if strings.HasPrefix(sniffed, "image/") && sniffed != svgMIME {
		if err := s.enforcePixelCap(s.local.localPath(key)); err != nil {
			s.removeBlob(ctx, key)
			return "", err
		}
	}
	return key, nil
}

// AttachStagedTx publishes a staged file as a media record inside tx. The file
// is copied to its final place (with image variants), the row is inserted with
// the op's origin and no run, and the staged copy is left for DeleteStaged,
// which the caller runs after commit. If the caller's transaction later rolls
// back, it must call RemoveFiles on the returned record.
func (s *Service) AttachStagedTx(ctx context.Context, tx pgx.Tx, op MediaOp) (*Media, error) {
	m, err := s.prepareAttach(ctx, op)
	if err != nil {
		return nil, err
	}
	if err := insertMediaTx(ctx, tx, m, op.Origin); err != nil {
		s.RemoveFiles(ctx, m)
		return nil, err
	}
	return m, nil
}

// prepareAttach copies the staged file to its final key and builds the record.
func (s *Service) prepareAttach(ctx context.Context, op MediaOp) (*Media, error) {
	if err := checkKey(op.StagedKey); err != nil || !isStagedKey(op.StagedKey) {
		return nil, fmt.Errorf("not a staged blob key %q", op.StagedKey)
	}
	rc, err := s.store.Get(ctx, op.StagedKey)
	if err != nil {
		return nil, fmt.Errorf("open staged file: %w", err)
	}
	defer rc.Close()

	br := bufio.NewReaderSize(rc, SniffLen)
	head, _ := br.Peek(SniffLen)
	mimeType := SniffUpload(head)
	ext := filepath.Ext(op.Filename)
	if !ExtensionMatchesMIME(ext, mimeType) {
		return nil, ErrInvalidMimeType
	}
	var body io.Reader = br
	if mimeType == svgMIME { // staged bytes were sanitised; a file put there by hand is not trusted
		clean, _, err := readSVG(br)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(clean)
	}

	name := s.generateFilename(ext)
	storagePath := filepath.Join(legacyMediaTenantDir, name)
	written, err := s.store.Put(ctx, storagePath, body)
	if err != nil {
		return nil, ErrStorageFailed
	}
	if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) > s.config.MaxTotalBytes {
		s.removeBlob(ctx, storagePath)
		return nil, ErrQuotaExceeded
	}

	now := time.Now()
	publicURL := s.store.URL(storagePath)
	folder := op.Folder
	if folder == "" {
		folder = "/"
	}
	m := &Media{
		ID:               uuid.New(),
		Filename:         name,
		OriginalFilename: op.Filename,
		MimeType:         mimeType,
		FileSize:         written,
		StorageProvider:  "local",
		StoragePath:      storagePath,
		PublicURL:        &publicURL,
		AltText:          op.AltText,
		Caption:          op.Caption,
		Credit:           op.Credit,
		Folder:           folder,
		UploadedBy:       op.UploadedBy,
		CreatedAt:        now,
		UpdatedAt:        now,
		Variants:         []ImageVariant{},
	}
	if m.IsImage() && mimeType != svgMIME {
		fullPath := s.local.localPath(storagePath)
		if w, h, err := s.getImageDimensions(fullPath); err == nil {
			m.Width, m.Height = &w, &h
		}
		if m.SupportsVariants() {
			variants, err := s.createVariants(ctx, m, fullPath)
			if errors.Is(err, ErrImageTooLarge) {
				s.removeBlob(ctx, storagePath)
				return nil, err
			} else if err != nil {
				s.logger.Warn("failed to create variants", "error", err.Error())
			} else {
				m.Variants = variants
			}
		}
	}
	return m, nil
}

// RemoveFiles deletes the file and variants of m without touching the
// database: the undo of AttachStagedTx when the surrounding transaction rolls
// back. Missing files are ignored.
func (s *Service) RemoveFiles(ctx context.Context, m *Media) {
	s.removeBlob(ctx, m.StoragePath)
	for _, v := range m.Variants {
		s.removeBlob(ctx, v.Path)
	}
}

// DeleteStaged removes staged files; a file that is already gone is ignored.
// Only keys under the staged directory are accepted.
func (s *Service) DeleteStaged(ctx context.Context, keys []string) error {
	var errs []error
	for _, key := range keys {
		if err := checkKey(key); err != nil || !isStagedKey(key) {
			errs = append(errs, fmt.Errorf("not a staged blob key %q", key))
			continue
		}
		if err := s.store.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ListStagedKeys lists the key of every file under the staged directory, for
// the sweeper's orphan scan (the blob store itself cannot list).
func (s *Service) ListStagedKeys(ctx context.Context) ([]string, error) {
	base := filepath.Join(s.local.root, legacyMediaTenantDir, stagedDir)
	var keys []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if d.Type().IsRegular() {
			rel, rerr := filepath.Rel(s.local.root, p)
			if rerr != nil {
				return rerr
			}
			keys = append(keys, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return keys, nil
}

// StagedKeyModTime is when the staged file at key was last written, for the
// sweeper's grace period. Only keys under the staged directory are accepted.
func (s *Service) StagedKeyModTime(key string) (time.Time, error) {
	if err := checkKey(key); err != nil || !isStagedKey(key) {
		return time.Time{}, fmt.Errorf("not a staged blob key %q", key)
	}
	fi, err := os.Stat(s.local.localPath(key))
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// OpenStaged opens the staged file at key for random-access reads and returns
// it with its size. The caller closes it. Only keys under the staged
// directory are accepted.
func (s *Service) OpenStaged(key string) (*os.File, int64, error) {
	if err := checkKey(key); err != nil || !isStagedKey(key) {
		return nil, 0, fmt.Errorf("not a staged blob key %q", key)
	}
	f, err := os.Open(s.local.localPath(key))
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}
