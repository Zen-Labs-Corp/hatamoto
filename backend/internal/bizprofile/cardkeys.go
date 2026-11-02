package bizprofile

import "slices"

// CanonicalKeys re-keys stored locale cards with normalise (the locale tag
// normaliser; the caller passes it so this package stays free of the entity
// layer). A key normalise rejects stays as it is. When several keys normalise
// to the same tag the winner is deterministic: the key that already is the
// canonical tag, otherwise the lexicographically first raw key. The other cards
// are dropped.
func CanonicalKeys[V any](cards map[string]V, normalise func(string) (string, error)) map[string]V {
	raw := make([]string, 0, len(cards))
	for k := range cards {
		raw = append(raw, k)
	}
	slices.Sort(raw)
	out := make(map[string]V, len(cards))
	from := make(map[string]string, len(cards)) // canonical tag -> the raw key it came from
	for _, k := range raw {
		tag := k
		if t, err := normalise(k); err == nil {
			tag = t
		}
		if prev, taken := from[tag]; taken && (prev == tag || k != tag) {
			continue
		}
		from[tag], out[tag] = k, cards[k]
	}
	return out
}
