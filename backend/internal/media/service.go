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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/media/modern"

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
	// AVIFBudget bounds the AVIF encode time of one upload; 0 = 10 s. Copies
	// that do not fit are made by the rebuild job.
	AVIFBudget time.Duration
	// Store holds the bytes. nil = the local disk under UploadDir. With any
	// other store UploadDir is not touched and MaxTotalBytes is ignored (the
	// quota measures a local directory).
	Store BlobStore
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
	// local is nil when the bytes live in another store.
	store BlobStore
	local *localStore
	// formats decides which modern formats are written; nil = WebP only.
	formats func(context.Context) Formats

	// stale holds copies a rebuild replaced. They stay on disk until
	// RemoveStale, because cached pages still link to them.
	staleMu sync.Mutex
	stale   []string
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

// NotifyChanged runs the change observer; callers that change many images at
// once (the rebuild job) call it once at the end.
func (s *Service) NotifyChanged(ctx context.Context) { s.notifyChanged(ctx) }

func (s *Service) notifyChanged(ctx context.Context) {
	if s.changed != nil {
		s.changed(ctx)
	}
}

// NewService creates a new media service.
func NewService(repo Repository, config Config, logger *slog.Logger) *Service {
	if config.Store != nil {
		if ls, ok := config.Store.(*localStore); ok {
			return &Service{repo: repo, config: config, logger: logger, root: ls.root, store: ls, local: ls}
		}
		// The quota walks a local directory; it does not apply to a bucket.
		config.MaxTotalBytes = 0
		return &Service{repo: repo, config: config, logger: logger, store: config.Store}
	}
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
	br := bufio.NewReaderSize(file, SniffLen)
	head, _ := br.Peek(SniffLen)
	sniffed := SniffUpload(head)
	if !ExtensionMatchesMIME(filepath.Ext(filename), sniffed) {
		return nil, ErrInvalidMimeType
	}
	file, mimeType := io.Reader(br), sniffed
	var cleaned []string
	if sniffed == svgMIME {
		// The stored bytes are the sanitised ones, never the upload.
		clean, removed, err := readSVG(br)
		if err != nil {
			return nil, err
		}
		file, cleaned = bytes.NewReader(clean), removed
	}

	// Generate unique filename
	ext := filepath.Ext(filename)
	uniqueName := s.generateFilename(ext)

	// Save file. The store creates the directory, refuses one that leaves the
	// upload root (a symlink to another disk) and removes a partial file.
	storagePath := filepath.Join(legacyMediaTenantDir, uniqueName)
	fullPath := ""
	if s.local != nil {
		fullPath = s.local.localPath(storagePath)
	}

	src := file
	if remaining >= 0 { // a body longer than it declared still cannot pass the quota
		src = io.LimitReader(file, remaining+1)
	}
	var written int64
	var err error
	if s.local != nil {
		written, err = s.store.Put(ctx, storagePath, src)
	} else {
		// Another store has no file to decode from: the upload is spooled to a
		// temporary file, stored from it and decoded from it.
		var spooled string
		spooled, written, err = s.putSpooled(ctx, storagePath, src)
		if spooled != "" {
			defer os.Remove(spooled)
			fullPath = spooled
		}
	}
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
		Cleaned:          cleaned,
	}

	// Detect image dimensions (not for svg: there are no pixels)
	if m.IsImage() && mimeType != svgMIME {
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
	if filter != nil && filter.Folder != "" {
		// A stored value that breaks today's naming rules stays filterable:
		// an invalid filter is matched as given.
		if folder, err := NormalizeFolder(filter.Folder); err == nil {
			f := *filter
			f.Folder, filter = folder, &f
		}
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
	if input.Credit != nil {
		m.Credit = input.Credit
	}
	if input.Folder != nil {
		folder, err := s.canonicalFolder(ctx, *input.Folder)
		if err != nil {
			return nil, err
		}
		m.Folder = folder
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
	if s.local == nil {
		return nil, errors.New("media files are not on the local disk")
	}
	return s.local.openFile(rel)
}

// ObjectStorage reports whether the bytes live in a store other than the local
// disk.
func (s *Service) ObjectStorage() bool { return s.local == nil }

// StatObject reports size and validator of a stored file by its path relative
// to the store, without reading it. A missing file satisfies
// errors.Is(err, fs.ErrNotExist).
func (s *Service) StatObject(ctx context.Context, rel string) (ObjectInfo, error) {
	st, ok := s.store.(Stater)
	if !ok {
		return ObjectInfo{}, errors.New("the media store cannot report object details")
	}
	return st.Stat(ctx, rel)
}

// GetObject streams a stored file by its path relative to the store and
// reports the object the bytes belong to. Close the returned reader. A missing
// file satisfies errors.Is(err, fs.ErrNotExist).
func (s *Service) GetObject(ctx context.Context, rel string) (ObjectInfo, io.ReadCloser, error) {
	g, ok := s.store.(InfoGetter)
	if !ok {
		return ObjectInfo{}, nil, errors.New("the media store cannot stream objects")
	}
	return g.GetWithInfo(ctx, rel)
}

// OpenObject opens a stored file for random-access reads. Close the returned
// file. OpenFile is the local-disk variant of the same thing.
func (s *Service) OpenObject(ctx context.Context, rel string) (io.ReadSeekCloser, ObjectInfo, error) {
	op, ok := s.store.(Opener)
	if !ok {
		return nil, ObjectInfo{}, errors.New("the media store cannot open objects")
	}
	info, rc, err := op.Open(ctx, rel)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return rc, info, nil
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

// Formats says which modern formats are produced next to the original-format
// copies.
type Formats struct{ WebP, AVIF bool }

// SetFormatPolicy installs the function that decides, per call, which modern
// formats to write. nil means WebP only, if the encoder is available.
func (s *Service) SetFormatPolicy(f func(context.Context) Formats) { s.formats = f }

// formatsFor lists the modern formats to write for an original of mimeType,
// WebP first. A format the original already has is not repeated.
func (s *Service) formatsFor(ctx context.Context, mimeType string) []modern.Format {
	pol := Formats{WebP: true}
	if s.formats != nil {
		pol = s.formats(ctx)
	}
	var out []modern.Format
	for _, c := range []struct {
		on bool
		f  modern.Format
	}{{pol.WebP, modern.WebP}, {pol.AVIF, modern.AVIF}} {
		if c.on && string(c.f) != mimeType && modern.Available(c.f) {
			out = append(out, c.f)
		}
	}
	return out
}

// Quality of the modern copies.
const (
	webpQuality = 80
	avifQuality = 60
)

// defaultAVIFBudget bounds the AVIF encode time of one upload; copies that do
// not fit are left to the rebuild job.
const defaultAVIFBudget = 10 * time.Second

// variantOrder is the order copies are made in.
var variantOrder = []string{"thumbnail", "medium", "large"}

// createVariants generates the resized copies of an image on upload. AVIF
// copies stop once the per-upload time budget is spent.
func (s *Service) createVariants(ctx context.Context, m *Media, sourcePath string) ([]ImageVariant, error) {
	budget := s.config.AVIFBudget
	if budget <= 0 {
		budget = defaultAVIFBudget
	}
	return s.buildVariants(ctx, m, sourcePath, budget)
}

// buildVariants writes every copy of the image at sourcePath: the
// original-format copies (thumbnail, medium, large) and, for each modern
// format, a copy of medium and large that is kept only when it is smaller.
// An image of at most 1024 px has no large copy, so it gets a full-size
// modern copy instead. avifBudget <= 0 means no time limit.
func (s *Service) buildVariants(ctx context.Context, m *Media, sourcePath string, avifBudget time.Duration) ([]ImageVariant, error) {
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

	base := strings.TrimSuffix(m.Filename, filepath.Ext(m.Filename))
	ext := filepath.Ext(m.Filename)
	dir := filepath.Dir(m.StoragePath)
	formats := s.formatsFor(ctx, m.MimeType)
	var avifSpent time.Duration

	// put stores data under path. It reports false, and stores nothing, when
	// the total quota has no room; the caller then stops making copies.
	put := func(path string, data *bytes.Buffer) (stored bool, err error) {
		if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) >= s.config.MaxTotalBytes {
			return false, nil
		}
		if _, err = s.store.Put(ctx, path, data); err != nil {
			return false, err
		}
		if s.config.MaxTotalBytes > 0 && dirSize(s.uploadRoot()) > s.config.MaxTotalBytes {
			s.removeBlob(ctx, path)
			return false, nil
		}
		return true, nil
	}

	// addModern writes the modern copies of img named <base>_<size>.<ext>,
	// keeping each only when smaller than the fallback bytes of the same
	// width. It reports false once the quota is full.
	addModern := func(sizeName string, img image.Image, width, height int, fallbackLen int) bool {
		for _, f := range formats {
			if f == modern.AVIF && avifBudget > 0 && avifSpent >= avifBudget {
				continue
			}
			q := webpQuality
			if f == modern.AVIF {
				q = avifQuality
			}
			var enc bytes.Buffer
			start := time.Now()
			err := modern.Encode(ctx, &enc, img, f, q)
			if f == modern.AVIF {
				avifSpent += time.Since(start)
			}
			if err != nil {
				s.logger.Warn("failed to encode modern copy",
					slog.String("size", sizeName), slog.String("format", string(f)), slog.String("error", err.Error()))
				continue
			}
			if enc.Len() >= fallbackLen {
				continue
			}
			path := filepath.Join(dir, fmt.Sprintf("%s_%s%s", base, sizeName, modern.Ext(f)))
			ok, err := put(path, &enc)
			if err != nil {
				s.logger.Warn("failed to save variant",
					slog.String("size", sizeName), slog.String("format", string(f)), slog.String("error", err.Error()))
				continue
			}
			if !ok {
				return false
			}
			variants = append(variants, ImageVariant{
				Size: sizeName, Format: string(f), Path: path, URL: s.store.URL(path), Width: width, Height: height,
			})
		}
		return true
	}

	hasLarge := false
	for _, sizeName := range variantOrder {
		sizeConfig := VariantSizes[sizeName]
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

		variantPath := filepath.Join(dir, fmt.Sprintf("%s_%s%s", base, sizeName, variantExt(m.MimeType, ext)))
		resizedImg := resizeImage(srcImg, newWidth, newHeight, sizeConfig.Crop)

		// Save variant; under a total quota only while there is room, and a
		// variant that crosses it is taken back.
		var encoded bytes.Buffer
		if err := encodeImage(ctx, &encoded, resizedImg, m.MimeType); err != nil {
			s.logger.Warn("failed to save variant",
				slog.String("size", sizeName),
				slog.String("error", err.Error()))
			continue
		}
		fallbackLen := encoded.Len()
		ok, err := put(variantPath, &encoded)
		if err != nil {
			s.logger.Warn("failed to save variant",
				slog.String("size", sizeName),
				slog.String("error", err.Error()))
			continue
		}
		if !ok {
			break
		}
		variants = append(variants, ImageVariant{
			Size:   sizeName,
			Path:   variantPath,
			URL:    s.store.URL(variantPath),
			Width:  newWidth,
			Height: newHeight,
		})
		if sizeName == "large" {
			hasLarge = true
		}
		if sizeConfig.Crop {
			continue // thumbnails get no modern copy
		}
		if !addModern(sizeName, resizedImg, newWidth, newHeight, fallbackLen) {
			break
		}
	}

	// Up to 1024 px there is no large copy; a full-size modern copy still gives
	// <picture> a 1x source. It must beat the original file.
	medium := VariantSizes["medium"]
	if !hasLarge && (srcWidth > medium.MaxWidth || srcHeight > medium.MaxHeight) && len(formats) > 0 {
		if s.local == nil {
			addModern("full", srcImg, srcWidth, srcHeight, int(m.FileSize))
		} else if fi, err := os.Stat(sourcePath); err == nil {
			addModern("full", srcImg, srcWidth, srcHeight, int(fi.Size()))
		}
	}

	return variants, nil
}

// RegenerateVariants rebuilds every resized copy of an image from its stored
// original, stores the new variant list and deletes copies the new list no
// longer names (for example the .jpg copies an old WebP upload got). The
// original is never touched. Replaced copies are not deleted here: cached pages
// may still link to them, so the caller purges the pages and then calls
// RemoveStale. It does not notify the change observer either: a caller that
// rebuilds many images does that once.
func (s *Service) RegenerateVariants(ctx context.Context, id uuid.UUID) (*Media, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !m.SupportsVariants() {
		return m, nil
	}
	var variants []ImageVariant
	err = s.withLocalSource(ctx, m.StoragePath, func(path string) (berr error) {
		variants, berr = s.buildVariants(ctx, m, path, 0)
		return berr
	})
	if err != nil {
		return nil, err
	}
	old := m.Variants
	m.Variants = variants
	if m.Variants == nil {
		m.Variants = []ImageVariant{}
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(variants))
	for _, v := range variants {
		keep[v.Path] = true
	}
	s.staleMu.Lock()
	for _, v := range old {
		if !keep[v.Path] {
			s.stale = append(s.stale, v.Path)
		}
	}
	s.staleMu.Unlock()
	return m, nil
}

// RemoveStale deletes the copies RegenerateVariants replaced.
func (s *Service) RemoveStale(ctx context.Context) {
	s.staleMu.Lock()
	paths := s.stale
	s.stale = nil
	s.staleMu.Unlock()
	for _, p := range paths {
		s.removeBlob(ctx, p)
	}
}

// ListImagesAfter and CountImages let the rebuild job walk the library.
func (s *Service) ListImagesAfter(ctx context.Context, after uuid.UUID, limit int) ([]*Media, error) {
	return s.repo.ListImagesAfter(ctx, after, limit)
}

// CountImages is the number of images a rebuild walks.
func (s *Service) CountImages(ctx context.Context) (int, error) { return s.repo.CountImages(ctx) }

// variantExt is the extension of a resized copy in the original's format.
func variantExt(_, origExt string) string { return origExt }

// dirSize is the total size of the regular files under dir.
func dirSize(dir string) int64 {
	var n int64
	// Staged files of unpublished changesets do not count against the quota.
	staged := filepath.Join(dir, legacyMediaTenantDir, stagedDir)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p == staged {
			return filepath.SkipDir
		}
		if err == nil && d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
