package bizprofile

import (
	"fmt"

	"hatamoto.dev/core/internal/jsonx"
)

// Row is the business data of the site settings row as stored: the two JSON
// columns and the three legacy columns. Default is the normalised default
// locale tag; the keys of LocaleProfiles must be normalised too.
type Row struct {
	Default         string
	BusinessProfile []byte
	LocaleProfiles  []byte
	Phone           string
	BusinessHours   string
	PostalAddress   []byte
	// Previous is the default locale before a language change. When it is set
	// and differs from Default, FoldRow stores the old default business as that
	// locale's card (SwapDefault) before it folds.
	Previous string
	// DropPrevious stores no card for Previous (it has no pages any more); the
	// business is stripped of its address and office all the same.
	DropPrevious bool
}

// FoldRow runs Fold on a stored row: it parses the columns, applies the legacy
// values, folds, and splits the result back into the columns. The migration,
// the language change and the import share it. The bool is false, and the row
// returned as it was, when no card sits under the default tag and no previous
// default is swapped out.
func FoldRow(r Row) (Row, bool, error) {
	if r.Default == "" {
		return r, false, nil
	}
	site, err := Parse(r.BusinessProfile)
	if err != nil {
		return r, false, err
	}
	cards, err := ParseLocales(r.LocaleProfiles)
	if err != nil {
		return r, false, err
	}
	swap := r.Previous != "" && r.Previous != r.Default
	if _, ok := cards[r.Default]; !ok && !swap {
		return r, false, nil
	}
	in := FoldIn{Default: r.Default, Site: FromLegacy(site, r.Phone, r.BusinessHours, r.PostalAddress), Cards: cards}
	if swap {
		in = SwapDefault(in, r.Previous)
	}
	out := Fold(in)
	if swap && r.DropPrevious {
		delete(out.Cards, r.Previous)
	}
	stored, phone, hours, addr := SplitLegacy(out.Business)
	res := Row{Default: r.Default, Phone: phone, BusinessHours: hours, PostalAddress: addr}
	if res.BusinessProfile, err = jsonx.Marshal(stored); err != nil {
		return r, false, fmt.Errorf("marshal business profile: %w", err)
	}
	if res.LocaleProfiles, err = jsonx.Marshal(out.Cards); err != nil {
		return r, false, fmt.Errorf("marshal locale profiles: %w", err)
	}
	return res, true, nil
}
