package media

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/google/uuid"
)

// Column limits of hatamoto_media that an adopted key must fit.
const (
	maxAdoptKey    = 500
	maxAdoptName   = 255
	maxAdoptFolder = 255
)

// AdoptRepo is the part of the media repository Adopt needs.
type AdoptRepo interface {
	ReferencedKeys(ctx context.Context) (map[string]struct{}, error)
	Create(ctx context.Context, m *Media) error
}

// AdoptOptions configures Adopt.
type AdoptOptions struct {
	// Dir is the directory to adopt, as a key prefix relative to the store
	// root; "", "/" and "." mean the whole store.
	Dir string
	// Apply creates the rows. Without it Adopt only reports them.
	Apply bool
	// Provider is the storage_provider of the active store, "local" or "s3".
	Provider string
	// OlderThan skips files modified more recently than this (or of unknown
	// age): a file may be in flight, written before its row. Zero or less
	// disables the guard.
	OlderThan time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// AdoptItem is one file that has, or would get, a row.
type AdoptItem struct {
	Key    string
	Mime   string
	Size   int64
	Folder string
}

// AdoptResult is the outcome of Adopt. Items are the files found without a
// row; Adopted how many rows were created; AlreadyHaveRow how many files in
// scope already had one; TooNew the files left alone because they are younger
// than OlderThan; Skipped the files that cannot get a row, each with
// the reason.
type AdoptResult struct {
	Items          []AdoptItem
	Adopted        int
	AlreadyHaveRow int
	TooNew         int
	Skipped        []string
}

// Adopt creates media rows for the files of the store that have none. Files the
// application owns (see IgnoredKey) are never adopted, and no variants are
// made: the row points at the file as it is.
func Adopt(ctx context.Context, repo AdoptRepo, l Lister, o AdoptOptions) (AdoptResult, error) {
	var res AdoptResult
	prefix, err := CleanKeyPrefix(o.Dir)
	if err != nil {
		return res, err
	}
	referenced, err := repo.ReferencedKeys(ctx)
	if err != nil {
		return res, fmt.Errorf("media adopt: load media rows: %w", err)
	}
	objs, withRow, err := listRowless(ctx, l, referenced, prefix)
	if err != nil {
		return res, err
	}
	res.AlreadyHaveRow = withRow
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	if o.Apply {
		// Rows may have appeared since the first load (an upload or an
		// import that committed meanwhile): never make a second one.
		fresh, err := repo.ReferencedKeys(ctx)
		if err != nil {
			return res, fmt.Errorf("media adopt: load media rows: %w", err)
		}
		kept := objs[:0]
		for _, obj := range objs {
			if _, ok := fresh[obj.Key]; ok {
				res.AlreadyHaveRow++
				continue
			}
			kept = append(kept, obj)
		}
		objs = kept
	}
	cutoff := now().Add(-o.OlderThan)
	for _, obj := range objs {
		if o.OlderThan > 0 && (obj.ModTime.IsZero() || obj.ModTime.After(cutoff)) {
			res.TooNew++
			continue
		}
		folder := "/"
		if dir := path.Dir(obj.Key); dir != "." {
			folder = "/" + dir
		}
		base := path.Base(obj.Key)
		switch {
		case hasUnsafeRune(obj.Key):
			res.Skipped = append(res.Skipped, DisplayKey(obj.Key)+": key holds control characters")
			continue
		case len(obj.Key) > maxAdoptKey:
			res.Skipped = append(res.Skipped, obj.Key+": key longer than 500 bytes")
			continue
		case len(base) > maxAdoptName:
			res.Skipped = append(res.Skipped, obj.Key+": file name longer than 255 bytes")
			continue
		case len(folder) > maxAdoptFolder:
			res.Skipped = append(res.Skipped, obj.Key+": folder longer than 255 bytes")
			continue
		}
		mime := MIMEForExtension(path.Ext(base))
		if mime == "" {
			mime = "application/octet-stream"
		}
		res.Items = append(res.Items, AdoptItem{Key: obj.Key, Mime: mime, Size: obj.Size, Folder: folder})
		if !o.Apply {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		url := "/media/" + obj.Key
		ts := now().UTC()
		m := &Media{
			ID:               uuid.New(),
			Filename:         base,
			OriginalFilename: base,
			MimeType:         mime,
			FileSize:         obj.Size,
			StorageProvider:  o.Provider,
			StoragePath:      obj.Key,
			PublicURL:        &url,
			Variants:         []ImageVariant{},
			Folder:           folder,
			CreatedAt:        ts,
			UpdatedAt:        ts,
		}
		if err := repo.Create(ctx, m); err != nil {
			return res, fmt.Errorf("media adopt: %s: %w", obj.Key, err)
		}
		res.Adopted++
	}
	return res, nil
}
