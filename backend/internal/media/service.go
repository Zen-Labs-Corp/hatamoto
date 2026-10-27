package media

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // Register GIF decoder
	_ "image/jpeg" // Register JPEG decoder
	_ "image/png"  // Register PNG decoder
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	_ "golang.org/x/image/webp" // Register WebP decoder
)

// legacyMediaTenantDir is the on-disk subdirectory and URL-path
// component every uploaded media file lives under. It was originally a
// per-tenant UUID; post-Path-B it is preserved verbatim so existing
// upload paths, public URLs (/media/<id>/...) and database StoragePath
// values continue to resolve. The value has no tenant meaning.
const legacyMediaTenantDir = "2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3"

// Config holds media service configuration.
type Config struct {
	UploadDir string // Directory for storing uploaded files
	BaseURL   string // Base URL for serving files (e.g., "/media")
	MaxSizeMB int    // Maximum file size in MB
	MaxPixels int    // Maximum decoded pixel count (width*height); 0 = default
	// MaxTotalBytes caps everything under UploadDir (originals and variants);
	// 0 = no cap. Set by the public demo so visitors cannot fill the disk.
	MaxTotalBytes int64
}

// DefaultConfig returns the default media configuration.
// defaultMaxPixels caps decoded image area to guard against decompression
// bombs (a tiny file that claims an enormous resolution). ~40 megapixels is
// generous for real photography while rejecting pathological inputs.
const defaultMaxPixels = 40_000_000

func DefaultConfig() Config {
	return Config{
		UploadDir: "./uploads",
		BaseURL:   "/media",
		MaxSizeMB: 10,
		MaxPixels: defaultMaxPixels,
	}
}

// Service handles media operations.
type Service struct {
	repo   Repository
	config Config
	logger *slog.Logger
	// changed runs after metadata was updated or a file deleted: rendered
	// pages embed alt text, captions and image variants.
	changed func(ctx context.Context)
	// root is the upload directory with symlinks resolved once at start-up.
	// Everything below it is opened through an os.Root, which refuses symlinks
	// that lead out of it: a symlinked directory INSIDE the upload directory
	// (uploads/default -> /mnt/...) is therefore not supported. Point
	// MEDIA_UPLOAD_DIR itself at the other disk instead.
	root string
	// quotaMu serialises uploads while a total quota is set, so concurrent
	// uploads cannot each pass the check and together overrun it.
	quotaMu sync.Mutex
	// store holds every write. Image decoding reads through local (a path),
	// the one place the local driver leaks; OpenFile needs an *os.File too.
	store BlobStore
	local *localStore
}

// uploadRoot is the resolved upload directory.
func (s *Service) uploadRoot() string {
	if s.root != "" {
		return s.root
	}
	return s.config.UploadDir
}

// SetChangeObserver installs the callback run after Update or Delete; nil
// disables it.
func (s *Service) SetChangeObserver(f func(ctx context.Context)) { s.changed = f }

func (s *Service) notifyChanged(ctx context.Context) {
	if s.changed != nil {
		s.changed(ctx)
	}
}

// NewService creates a new media service.
func NewService(repo Repository, config Config, logger *slog.Logger) *Service {
	// Ensure upload directory exists
	if err := os.MkdirAll(config.UploadDir, 0755); err != nil {
		logger.Error("failed to create upload directory", slog.String("error", err.Error()))
	}

	resolved, err := filepath.EvalSymlinks(config.UploadDir)
	if err != nil {
		resolved = config.UploadDir
	}
	local := &localStore{root: resolved, baseURL: config.BaseURL}
	return &Service{
		repo:   repo,
		config: config,
		logger: logger,
		root:   resolved,
		store:  local,
		local:  local,
	}
}

// createUpload opens the destination of an upload; tests replace it to inject
// write failures.
var createUpload = func(path string) (io.WriteCloser, error) { return os.Create(path) }

// Upload stores a new media file.
func (s *Service) Upload(ctx context.Context, userID *uuid.UUID, file io.Reader, filename, _ string, fileSize int64) (*Media, error) {
	// Validate file size
	maxBytes := int64(s.config.MaxSizeMB) * 1024 * 1024
	if fileSize > maxBytes {
		return nil, ErrFileTooLarge
	}
	// Total quota: checked before anything is written, and again before the
	// variants (which add to the disk use) are made.
	var remaining int64 = -1
	if s.config.MaxTotalBytes > 0 {
		s.quotaMu.Lock()
		defer s.quotaMu.Unlock()
		remaining = s.config.MaxTotalBytes - dirSize(s.uploadRoot())
		if remaining <= 0 || fileSize > remaining {
			return nil, ErrQuotaExceeded
		}
	}

	// Whoever calls (the HTTP handler or a plugin), the stored type is the one
	// sniffed from the bytes, and it must be an allowed type whose extension
	// matches the name: stored HTML or script would be served from the site's
	// origin. The caller's mimeType is advisory and ignored.
	br := bufio.NewReaderSize(file, 512)
	head, _ := br.Peek(512)
	sniffed := http.DetectContentType(head)
	if !ExtensionMatchesMIME(filepath.Ext(filename), sniffed) {
		return nil, ErrInvalidMimeType
	}
	file, mimeType := io.Reader(br), sniffed

	// Generate unique filename
	ext := filepath.Ext(filename)
	uniqueName := s.generateFilename(ext)

	// Save file. The store creates the directory, refuses one that leaves the
	// upload root (a symlink to another disk) and removes a partial file.
	storagePath := filepath.Join(legacyMediaTenantDir, uniqueName)
	fullPath := s.local.localPath(storagePath)

	src := file
	if remaining >= 0 { // a body longer than it declared still cannot pass the quota
		src = io.LimitReader(file, remaining+1)
	}
	written, err := s.store.Put(ctx, storagePath, src)
	if err != nil {
		s.logger.Error("failed to store file (a symlink inside MEDIA_UPLOAD_DIR that leaves it is refused; point MEDIA_UPLOAD_DIR itself at the other disk)",
			slog.String("path", storagePath), slog.String("error", err.Error()))
		return nil, ErrStorageFailed
	}
	if remaining >= 0 && written > remaining {
		s.removeBlob(ctx, storagePath)
		return nil, ErrQuotaExceeded
	}

	// Create media record
	now := time.Now()
	publicURL := s.store.URL(storagePath)

	m := &Media{
		ID:               uuid.New(),
		Filename:         uniqueName,
		OriginalFilename: filename,
		MimeType:         mimeType,
		FileSize:         written,
		StorageProvider:  "local",
		StoragePath:      storagePath,
		PublicURL:        &publicURL,
		Folder:           "/",
		UploadedBy:       userID,
		CreatedAt:        now,
		UpdatedAt:        now,
		Variants:         []ImageVariant{},
	}

	// Detect image dimensions
	if m.IsImage() {
		// Decompression-bomb guard: reject images whose pixel area exceeds the
		// cap BEFORE any full decode/resize happens in createVariants. NOTE: for
		// extended WebP the header dimensions can understate the real frame size,
		// so createVariants performs a second post-DECODE bounds check.
		if err := s.enforcePixelCap(fullPath); err != nil {
			s.removeBlob(ctx, storagePath)
			return nil, err
		}

		// NOTE: EXIF/metadata stripping of the ORIGINAL is intentionally NOT done
		// here yet. A blind decode+re-encode drops the EXIF orientation tag
		// without baking the rotation into pixels (portrait photos render
		// sideways) and re-compresses JPEGs. Resize variants already strip EXIF
		// as a side effect. Proper orientation-preserving strip is a follow-up.

		width, height, err := s.getImageDimensions(fullPath)
		if err == nil {
			m.Width = &width
			m.Height = &height
		}

		// Create image variants for supported formats
		if m.SupportsVariants() {
			variants, err := s.createVariants(ctx, m, fullPath)
			if errors.Is(err, ErrImageTooLarge) {
				// The post-decode bounds check caught a bomb the header check
				// missed (extended WebP). Delete the original and fail the upload.
				s.removeBlob(ctx, storagePath)
				return nil, err
			} else if err != nil {
				s.logger.Warn("failed to create variants", slog.String("error", err.Error()))
			} else {
				m.Variants = variants
			}
		}
	}

	// Save to database
	if err := s.repo.Create(ctx, m); err != nil {
		// Cleanup file on database error
		s.removeBlob(ctx, storagePath)
		return nil, err
	}

	return m, nil
}

// Get retrieves a media record by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Media, error) {
	return s.repo.GetByID(ctx, id)
}

// List retrieves media records with filtering and pagination.
func (s *Service) List(ctx context.Context, filter *ListMediaFilter, limit, offset int) (*PagedResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.List(ctx, filter, limit, offset)
}

// Update modifies media metadata.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input *UpdateMediaInput) (*Media, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.AltText != nil {
		m.AltText = input.AltText
	}
	if input.Caption != nil {
		m.Caption = input.Caption
	}
	if input.Folder != nil {
		m.Folder = *input.Folder
	}

	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	s.notifyChanged(ctx)

	return m, nil
}

// Delete removes a media file and its database record.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	// Get media to find file path
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Delete from database first
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.notifyChanged(ctx)

	// Delete main file and variants; a missing file is not a problem.
	if err := s.store.Delete(ctx, m.StoragePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logger.Warn("failed to delete file", slog.String("path", m.StoragePath), slog.String("error", err.Error()))
	}
	for _, variant := range m.Variants {
		if err := s.store.Delete(ctx, variant.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("failed to delete variant", slog.String("path", variant.Path), slog.String("error", err.Error()))
		}
	}

	return nil
}

// removeBlob drops a stored file after a failed upload; a missing file is fine.
func (s *Service) removeBlob(ctx context.Context, key string) {
	if err := s.store.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logger.Warn("failed to remove file", slog.String("path", key), slog.String("error", err.Error()))
	}
}

// OpenFile opens a stored file by its path relative to the upload directory.
// The path is resolved inside an os.Root: "..", absolute paths and symlinks
// that lead outside the directory are refused. Close the returned file.
func (s *Service) OpenFile(rel string) (*os.File, error) {
	return s.local.openFile(rel)
}

// Config returns the media service configuration.
func (s *Service) Config() Config {
	return s.config
}

// generateFilename creates a unique filename with the given extension.
func (s *Service) generateFilename(ext string) string {
	// Generate random bytes for unique name
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes) + strings.ToLower(ext)
}

// getImageDimensions returns the width and height of an image file.
// enforcePixelCap reads only the image header (cheap) and rejects images whose
// width*height exceeds the configured cap.
func (s *Service) enforcePixelCap(path string) error {
	maxPixels := int64(s.config.MaxPixels)
	if maxPixels <= 0 {
		maxPixels = defaultMaxPixels
	}

	// WebP first: read the REAL VP8/VP8L frame dimensions from the container
	// without decoding, by seeking through every chunk. image.DecodeConfig only
	// returns the VP8X canvas size, which an extended-WebP bomb makes tiny while
	// the real frame is enormous. If the file IS a WebP we decide here and never
	// fall through to the canvas-based check (fail closed): px==0 means we could
	// not size the frame, so reject rather than let image.Decode allocate it.
	if px, isWebP := webpFramePixels(path); isWebP {
		if px <= 0 || px > maxPixels {
			return ErrImageTooLarge
		}
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		// Not a decodable image header; leave it to the rest of the pipeline.
		return nil
	}
	// Guard against int overflow on absurd dimensions.
	if config.Width <= 0 || config.Height <= 0 {
		return ErrInvalidFile
	}
	if int64(config.Width)*int64(config.Height) > maxPixels {
		return ErrImageTooLarge
	}
	return nil
}

func (s *Service) getImageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
}

// createVariants generates resized versions of an image.
func (s *Service) createVariants(ctx context.Context, m *Media, sourcePath string) ([]ImageVariant, error) {
	var variants []ImageVariant

	// Open source image
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer srcFile.Close()

	srcImg, _, err := image.Decode(srcFile)
	if err != nil {
		return nil, err
	}

	srcBounds := srcImg.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()

	// Post-decode bomb guard: the DECODED bounds are authoritative (header
	// dimensions can lie for extended WebP). Reject before any resize allocates.
	maxPixels := s.config.MaxPixels
	if maxPixels <= 0 {
		maxPixels = defaultMaxPixels
	}
	if int64(srcWidth)*int64(srcHeight) > int64(maxPixels) {
		return nil, ErrImageTooLarge
	}

	for sizeName, sizeConfig := range VariantSizes {
		// Skip if source is smaller than variant
		if srcWidth <= sizeConfig.MaxWidth && srcHeight <= sizeConfig.MaxHeight && !sizeConfig.Crop {
			continue
		}

		// Calculate new dimensions
		var newWidth, newHeight int
		if sizeConfig.Crop {
			// For crop, use the smallest dimension to create a square
			newWidth = sizeConfig.MaxWidth
			newHeight = sizeConfig.MaxHeight
		} else {
			// For fit, maintain aspect ratio
			ratio := float64(srcWidth) / float64(srcHeight)
			if ratio > 1 {
				newWidth = sizeConfig.MaxWidth
				newHeight = int(float64(newWidth) / ratio)
			} else {
				newHeight = sizeConfig.MaxHeight
				newWidth = int(float64(newHeight) * ratio)
			}
		}

		// Generate variant filename
		variantFilename := fmt.Sprintf("%s_%s%s", strings.TrimSuffix(m.Filename, filepath.Ext(m.Filename)), sizeName, variantExt(m.MimeType, filepath.Ext(m.Filename)))
		variantPath := filepath.Join(filepath.Dir(m.StoragePath), variantFilename)

		// Create resized image using simple nearest-neighbor for now
		// In production, use golang.org/x/image/draw with better interpolation
		resizedImg := resizeImage(srcImg, newWidth, newHeight, sizeConfig.Crop)

		// Save variant; under a total quota only while there is room, and a
		// variant that crosses it is taken back.
		if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) >= s.config.MaxTotalBytes {
			break
		}
		var encoded bytes.Buffer
		err := encodeImage(&encoded, resizedImg, m.MimeType)
		if err == nil {
			_, err = s.store.Put(ctx, variantPath, &encoded)
		}
		if err != nil {
			s.logger.Warn("failed to save variant",
				slog.String("size", sizeName),
				slog.String("error", err.Error()))
			continue
		}

		if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) > s.config.MaxTotalBytes {
			s.removeBlob(ctx, variantPath)
			break
		}
		variantURL := s.store.URL(variantPath)
		variants = append(variants, ImageVariant{
			Size:   sizeName,
			Path:   variantPath,
			URL:    variantURL,
			Width:  newWidth,
			Height: newHeight,
		})
	}

	return variants, nil
}

// variantExt is the extension of a resized copy. WebP originals are resized to
// JPEG bytes (no pure-Go WebP encoder), so the copy is named .jpg: the name
// must say what the bytes are.
func variantExt(mimeType, origExt string) string {
	if mimeType == "image/webp" {
		return ".jpg"
	}
	return origExt
}

// dirSize is the total size of the regular files under dir.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
