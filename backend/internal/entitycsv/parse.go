package entitycsv

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"hatamoto.dev/core/internal/csvx"
)

// Import limits.
const (
	MaxRows   = 50000
	BatchSize = 500
	// previewRows is how many sample rows the mapping step shows.
	previewRows = 3
)

// ErrTooManyRows is returned when a file has more than MaxRows data rows.
var ErrTooManyRows = fmt.Errorf("the file has more than %d rows; split it and import the parts one after another", MaxRows)

// newReader returns a csv.Reader over r without a leading BOM.
func newReader(r io.Reader) *csv.Reader {
	br := &bomReader{r: r}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	return cr
}

// bomReader drops a UTF-8 byte order mark from the start of the stream.
type bomReader struct {
	r       io.Reader
	started bool
	pending []byte
}

func (b *bomReader) Read(p []byte) (int, error) {
	if !b.started {
		b.started = true
		head := make([]byte, 3)
		n, err := io.ReadFull(b.r, head)
		head = head[:n]
		if string(head) == csvx.BOM {
			head = nil
		}
		b.pending = head
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return 0, err
		}
	}
	if len(b.pending) > 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	return b.r.Read(p)
}

// readHeader reads and checks the header row: no empty and no repeated cell.
func readHeader(cr *csv.Reader) ([]string, error) {
	rec, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("the file has no header row")
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	header := make([]string, len(rec))
	for i, h := range rec {
		h = strings.TrimSpace(h)
		switch {
		case h == "":
			return nil, fmt.Errorf("column %d of the header row has no name", i+1)
		case seen[h]:
			return nil, fmt.Errorf("the header row names %q twice", h)
		}
		seen[h] = true
		header[i] = h
	}
	return header, nil
}

// Preview reads the header, the first sample rows and counts the data rows.
// The file is rejected for a malformed header, a quoting error, or more than
// MaxRows data rows.
func Preview(r io.Reader) (header []string, sample [][]string, rows int, err error) {
	cr := newReader(r)
	if header, err = readHeader(cr); err != nil {
		return nil, nil, 0, err
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, 0, err
		}
		if blank(rec) {
			continue
		}
		rows++
		if rows > MaxRows {
			return nil, nil, 0, ErrTooManyRows
		}
		if len(sample) < previewRows {
			sample = append(sample, rec)
		}
	}
	if rows == 0 {
		return nil, nil, 0, errors.New("the file has a header but no rows")
	}
	return header, sample, rows, nil
}

// blank reports whether a record is one empty line or only empty cells.
func blank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
