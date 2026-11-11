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
	if s.local == nil {
		return s.stageInStore(ctx, key, src, sniffed, maxBytes)
	}
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

// stageInStore is the tail of StageBlob for a non-local store: the bytes are
// spooled and checked in a temporary file, and only a file that passes is
// stored. The total quota does not apply to a bucket.
func (s *Service) stageInStore(ctx context.Context, key string, src io.Reader, sniffed string, maxBytes int64) (string, error) {
	tmp, written, err := spoolTemp(src)
	if tmp != "" {
		defer os.Remove(tmp)
	}
	if err != nil {
		s.logger.Error("failed to stage file", "path", key, "error", err.Error())
		return "", ErrStorageFailed
	}
	if maxBytes > 0 && written > maxBytes {
		return "", ErrFileTooLarge
	}
	if strings.HasPrefix(sniffed, "image/") && sniffed != svgMIME {
		if err := s.enforcePixelCap(tmp); err != nil {
			return "", err
		}
	}
	if err := putFile(ctx, s.store, key, tmp); err != nil {
		s.removeBlobDetached(ctx, key) // the store may have kept the object of a call that failed
		s.logger.Error("failed to stage file", "path", key, "error", err.Error())
		return "", ErrStorageFailed
	}
	return key, nil
}

// AttachStagedTx publishes a staged file as a media record inside tx. The file
// is copied to its final place (with image variants), the row is inserted with
// the op's origin and no run, and the staged copy is left for DeleteStaged,
// which the caller runs after commit. If the caller's transaction later rolls
// back, it must call RemoveFilesDetached on the returned record.
func (s *Service) AttachStagedTx(ctx context.Context, tx pgx.Tx, op MediaOp) (*Media, error) {
	m, err := s.prepareAttach(ctx, op)
	if err != nil {
		return nil, err
	}
	if err := insertMediaTx(ctx, tx, m, op.Origin); err != nil {
		s.RemoveFilesDetached(ctx, m)
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
		if s.local == nil {
			s.removeBlobDetached(ctx, storagePath) // the store may have kept the object of a call that failed
		}
		return nil, ErrStorageFailed
	}
	if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) > s.config.MaxTotalBytes {
		s.removeBlobDetached(ctx, storagePath)
		return nil, ErrQuotaExceeded
	}

	now := time.Now()
	publicURL := s.store.URL(storagePath)
	folder, err := s.canonicalFolder(ctx, op.Folder)
	if err != nil {
		s.removeBlob(ctx, storagePath)
		return nil, err
	}
	m := &Media{
		ID:               uuid.New(),
		Filename:         name,
		OriginalFilename: op.Filename,
		MimeType:         mimeType,
		FileSize:         written,
		StorageProvider:  s.storageProvider(),
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
		err := s.withLocalSource(ctx, storagePath, func(fullPath string) error {
			if w, h, err := s.getImageDimensions(fullPath); err == nil {
				m.Width, m.Height = &w, &h
			}
			if m.SupportsVariants() {
				variants, err := s.createVariants(ctx, m, fullPath)
				if errors.Is(err, ErrImageTooLarge) {
					return err
				} else if err != nil {
					s.logger.Warn("failed to create variants", "error", err.Error())
				} else {
					m.Variants = variants
				}
			}
			return nil
		})
		if err != nil {
			s.RemoveFilesDetached(ctx, m)
			if errors.Is(err, ErrImageTooLarge) {
				return nil, err
			}
			return nil, ErrStorageFailed
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

// RemoveFilesDetached is RemoveFiles for a failure path whose context may be
// cancelled.
func (s *Service) RemoveFilesDetached(ctx context.Context, m *Media) {
	s.removeBlobDetached(ctx, m.StoragePath)
	for _, v := range m.Variants {
		s.removeBlobDetached(ctx, v.Path)
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
	if s.local == nil {
		l, ok := s.store.(Lister)
		if !ok {
			return nil, errors.New("the media store cannot list objects")
		}
		objs, err := l.List(ctx, path.Join(legacyMediaTenantDir, stagedDir)+"/")
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(objs))
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		return keys, nil
	}
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
	if s.local == nil {
		return s.storeModTime(key)
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
	return s.OpenStagedCtx(context.Background(), key)
}

// OpenStagedCtx is OpenStaged for a caller that can stop waiting: a download
// from object storage ends when ctx does (or after storeOpTimeout).
func (s *Service) OpenStagedCtx(ctx context.Context, key string) (*os.File, int64, error) {
	if err := checkKey(key); err != nil || !isStagedKey(key) {
		return nil, 0, fmt.Errorf("not a staged blob key %q", key)
	}
	var f *os.File
	var err error
	if s.local == nil {
		f, err = s.spoolStaged(ctx, key)
	} else {
		f, err = os.Open(s.local.localPath(key))
	}
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

// storeOpTimeout bounds the store calls of the methods that take no context.
const storeOpTimeout = 2 * time.Minute

// storeModTime is the last-write time of key in a non-local store. A store
// that does not report one yields "now", which keeps the sweeper from deleting
// a file whose age it cannot tell.
func (s *Service) storeModTime(key string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	if st, ok := s.store.(Stater); ok {
		info, err := st.Stat(ctx, key)
		if err != nil {
			return time.Time{}, err
		}
		if !info.ModTime.IsZero() {
			return info.ModTime, nil
		}
	}
	if l, ok := s.store.(Lister); ok {
		objs, err := l.List(ctx, key)
		if err != nil {
			return time.Time{}, err
		}
		for _, o := range objs {
			if o.Key == key && !o.ModTime.IsZero() {
				return o.ModTime, nil
			}
		}
	}
	return time.Now(), nil
}

// spoolStaged copies the staged object at key to a temporary file that is
// already unlinked: it vanishes when the caller closes it.
func (s *Service) spoolStaged(ctx context.Context, key string) (*os.File, error) {
	ctx, cancel := context.WithTimeout(ctx, storeOpTimeout)
	defer cancel()
	body, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	f, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
