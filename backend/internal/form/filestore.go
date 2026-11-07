package form

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// maxFileNameRunes bounds a stored display name.
const maxFileNameRunes = 120

// tmpDir holds uploads that are staged but not yet attached to a submission.
const tmpDir = "tmp"

// stagedMaxAge is how long a staged file may sit in tmp before the sweep at
// start-up removes it. An upload is cut off after minutes, so an older file
// is what a crash or kill left behind.
const stagedMaxAge = time.Hour

// ErrFileTooLarge is returned by Stage when the upload exceeds its limit.
var ErrFileTooLarge = errors.New("file too large")

var fileIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidFileID reports whether id has the shape of a generated file id. Check
// it before using an id that came from stored data.
func ValidFileID(id string) bool { return fileIDRe.MatchString(id) }

// FileStore keeps uploaded form files on local disk, outside every web-served
// directory. Layout: <root>/<form id>/<file id> for stored files and
// <root>/tmp/<random> for staged ones. File names on disk are generated; the
// client's filename is never used. Directories are 0700, files 0600. Every
// access goes through an os.Root, which refuses symlinks that leave it.
//
// Typical use: Stage the part, Staged.Check it, then Commit once the whole
// submission is accepted (or Discard on any failure).
type FileStore struct{ root string }

// NewFileStore returns a store rooted at root, created if missing.
func NewFileStore(root string) (*FileStore, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create file store root: %w", err)
	}
	st := &FileStore{root: root}
	st.sweepStaged(time.Now())
	return st, nil
}

// sweepStaged removes staged files older than stagedMaxAge: the leftovers of a
// process that died mid-upload. Best effort, nothing is reported.
func (s *FileStore) sweepStaged(now time.Time) {
	root, err := s.open()
	if err != nil {
		return
	}
	defer root.Close()
	dir, err := root.Open(tmpDir)
	if err != nil {
		return
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && now.Sub(info.ModTime()) > stagedMaxAge {
			_ = root.Remove(path.Join(tmpDir, e.Name()))
		}
	}
}

func (s *FileStore) open() (*os.Root, error) { return os.OpenRoot(s.root) }

func newFileID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Staged is an upload written to the store's tmp area. Call Discard when it
// is not committed; Discard after Commit is a no-op.
type Staged struct {
	// Size is the number of bytes written.
	Size int64

	store *FileStore
	key   string
}

// Stage streams r into the tmp area. It reads at most limit bytes: a longer
// stream fails with ErrFileTooLarge and leaves nothing behind.
func (s *FileStore) Stage(r io.Reader, limit int64) (*Staged, error) {
	root, err := s.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	id, err := newFileID()
	if err != nil {
		return nil, err
	}
	key := path.Join(tmpDir, id)
	out, err := root.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create staged file: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(r, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = ErrFileTooLarge
	}
	if err != nil {
		_ = root.Remove(key)
		return nil, err
	}
	return &Staged{Size: n, store: s, key: key}, nil
}

// Check sniffs the staged bytes against ext (lowercase, no dot) and returns
// the content type to store. See CheckUpload.
func (st *Staged) Check(ext string) (string, error) {
	root, err := st.store.open()
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(st.key)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return CheckUpload(ext, f, st.Size)
}

// Discard removes the staged file. It is safe to call twice.
func (st *Staged) Discard() {
	if st == nil || st.key == "" {
		return
	}
	if root, err := st.store.open(); err == nil {
		_ = root.Remove(st.key)
		root.Close()
	}
	st.key = ""
}

// Commit moves a staged upload to its final place under formID and returns
// its generated id. The staged value is spent afterwards.
func (s *FileStore) Commit(formID uuid.UUID, st *Staged) (string, error) {
	if st == nil || st.key == "" || st.store != s {
		return "", errors.New("commit: nothing staged")
	}
	id, err := newFileID()
	if err != nil {
		return "", err
	}
	root, err := s.open()
	if err != nil {
		return "", err
	}
	defer root.Close()
	dir := formID.String()
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create form directory: %w", err)
	}
	dst := path.Join(dir, id)
	if _, err := root.Lstat(dst); err == nil {
		return "", errors.New("commit: file already exists")
	}
	if err := root.Rename(st.key, dst); err != nil {
		return "", fmt.Errorf("commit file: %w", err)
	}
	st.key = ""
	return id, nil
}

// Open returns the stored file for streaming (it supports Seek and Stat, so
// http.ServeContent can serve it). A missing file or an id that is not a
// generated one returns an error satisfying errors.Is(err, fs.ErrNotExist).
func (s *FileStore) Open(formID uuid.UUID, id string) (*os.File, error) {
	if !ValidFileID(id) {
		return nil, fmt.Errorf("file %q: %w", id, fs.ErrNotExist)
	}
	root, err := s.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(path.Join(formID.String(), id))
}

// Delete removes one stored file. A missing file is not an error.
func (s *FileStore) Delete(formID uuid.UUID, id string) error {
	if !ValidFileID(id) {
		return nil
	}
	root, err := s.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(path.Join(formID.String(), id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteForm removes every stored file of a form. A missing directory is not
// an error.
func (s *FileStore) DeleteForm(formID uuid.UUID) error {
	root, err := s.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.RemoveAll(formID.String()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// SanitizeName turns a client-supplied filename into a safe display name: no
// path components, no control or invisible formatting characters, at most
// maxFileNameRunes runes (the extension is kept), never empty. It is for
// display only; it is never used as a path.
func SanitizeName(name string) string {
	name = strings.ToValidUTF8(name, "")
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		return "file"
	}
	if utf8.RuneCountInString(name) > maxFileNameRunes {
		ext := path.Ext(name)
		if utf8.RuneCountInString(ext) > 16 {
			ext = ""
		}
		keep := maxFileNameRunes - utf8.RuneCountInString(ext)
		name = string([]rune(strings.TrimSuffix(name, ext))[:keep]) + ext
	}
	return name
}
