package auth

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/testutil"
)

// fakeRow hands scanAPIKey a fixed set of values, as pgx would after the
// COALESCEs in apiKeyColumns have been applied.
type fakeRow struct {
	vals []any
}

func (f fakeRow) Scan(dest ...any) error {
	if len(dest) != len(f.vals) {
		return errors.New("column/dest count mismatch")
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *uuid.UUID:
			*p = f.vals[i].(uuid.UUID)
		case *string:
			*p = f.vals[i].(string)
		case *int:
			*p = f.vals[i].(int)
		case *int64:
			*p = f.vals[i].(int64)
		case *bool:
			*p = f.vals[i].(bool)
		case *[]APIKeyScope:
			*p = f.vals[i].([]APIKeyScope)
		case **uuid.UUID:
			*p = f.vals[i].(*uuid.UUID)
		case **time.Time:
			*p = f.vals[i].(*time.Time)
		case *time.Time:
			*p = f.vals[i].(time.Time)
		default:
			return errors.New("unsupported dest type")
		}
	}
	return nil
}

var coalesceRe = regexp.MustCompile(`COALESCE\([^)]*\)`)

// countColumns counts select-list columns, collapsing COALESCE(a, b) groups.
func countColumns() int {
	return len(strings.Split(coalesceRe.ReplaceAllString(apiKeyColumns, "x"), ","))
}

func TestAPIKeyColumnsNullSafe(t *testing.T) {
	for _, col := range []string{"description", "last_used_ip", "rate_limit", "scopes", "request_count", "is_active"} {
		testutil.Contains(t, apiKeyColumns, "COALESCE("+col, col)
	}
	testutil.Contains(t, apiKeyColumns, "COALESCE(is_active, false)", "a NULL is_active must fail closed")
	testutil.NotContains(t, apiKeyColumns, "COALESCE(is_active, true)")
	testutil.Equal(t, 17, countColumns(), "column list shape changed: update scanAPIKey too")
}

func TestScanAPIKeyNullColumns(t *testing.T) {
	now := time.Now()
	creator := uuid.New()
	row := fakeRow{vals: []any{
		uuid.New(), "k", "", "nxk_abcdefgh", "hash",
		[]APIKeyScope{}, 1000, (*time.Time)(nil), (*time.Time)(nil), "",
		int64(0), true, &creator, now, now,
		(*time.Time)(nil), (*uuid.UUID)(nil),
	}}
	var key APIKey
	testutil.RequireNoError(t, scanAPIKey(row, &key))
	testutil.Equal(t, "", key.Description)
	testutil.Equal(t, "", key.LastUsedIP)
	testutil.Equal(t, 1000, key.RateLimit)
}

func TestScanAPIKeyDestCountMatchesColumns(t *testing.T) {
	// 17 destinations in scanAPIKey; a short row must fail the count check.
	var key APIKey
	testutil.Error(t, scanAPIKey(fakeRow{vals: make([]any, 16)}, &key))
}

func TestNormalizeRateLimit(t *testing.T) {
	for in, want := range map[int]int{0: 1000, -5: 1000, 1: 1, 1000: 1000, 5000: 5000} {
		testutil.Equal(t, want, NormalizeRateLimit(in), "in=%d", in)
	}
}

func TestUpdateRejectsNonPositiveRateLimit(t *testing.T) {
	svc := NewAPIKeyService(nil, nil)
	for _, n := range []int{0, -1} {
		_, err := svc.Update(t.Context(), uuid.New(), &UpdateAPIKeyInput{RateLimit: &n})
		testutil.ErrorIs(t, err, ErrInvalidRateLimit)
	}
}

func TestWithExtraDestAppendsCreatorRole(t *testing.T) {
	now := time.Now()
	creator := uuid.New()
	vals := []any{
		uuid.New(), "k", "", "nxk_abcdefgh", "hash",
		[]APIKeyScope{}, 1000, (*time.Time)(nil), (*time.Time)(nil), "",
		int64(0), true, &creator, now, now,
		(*time.Time)(nil), (*uuid.UUID)(nil), "viewer",
	}
	var key APIKey
	testutil.RequireNoError(t, scanAPIKey(withExtraDest{fakeRow{vals: vals}, &key.CreatorRole}, &key))
	testutil.Equal(t, "viewer", key.CreatorRole)
	testutil.False(t, key.CreatorCanAct())
}
