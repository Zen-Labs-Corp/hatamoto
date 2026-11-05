package media

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidFolder is returned for a folder name that breaks the rules.
var ErrInvalidFolder = errors.New("a folder name is 1 to 60 letters, digits, spaces, hyphens or underscores; folders cannot be nested")

// RootFolder is the folder of files that are in no folder ("Uncategorised").
const RootFolder = "/"

// MaxFolderNameLen is the longest folder name, in characters.
const MaxFolderNameLen = 60

// FolderCount is a folder and the number of files in it.
type FolderCount struct {
	Name  string
	Count int
}

// NormalizeFolder returns the stored form of a folder: "/" for none, else "/"
// plus a name of 1 to 60 letters, digits, spaces, hyphens or underscores.
// There is one level only, so a name with a slash is refused.
func NormalizeFolder(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "/"))
	if s == "" {
		return RootFolder, nil
	}
	if utf8.RuneCountInString(s) > MaxFolderNameLen {
		return "", ErrInvalidFolder
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' {
			return "", ErrInvalidFolder
		}
	}
	return "/" + s, nil
}

// canonicalFolder normalises name and, when a folder that differs only in
// case already exists, returns that folder's spelling so names stay unique.
func (s *Service) canonicalFolder(ctx context.Context, name string) (string, error) {
	folder, err := NormalizeFolder(name)
	if err != nil || folder == RootFolder {
		return folder, err
	}
	existing, err := s.repo.Folders(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range existing {
		if strings.EqualFold(f.Name, folder) {
			return f.Name, nil
		}
	}
	return folder, nil
}

// Folders lists the folders in use with their file counts, the root folder
// ("/") included when it has files, ordered by name.
func (s *Service) Folders(ctx context.Context) ([]FolderCount, error) {
	return s.repo.Folders(ctx)
}
