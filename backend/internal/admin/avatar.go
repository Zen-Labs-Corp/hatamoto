package admin

import (
	"strings"
	"unicode"
)

// Initials is the avatar text: the first letters of the first and last word of
// the name ("Hana Sato" gives "HS"), the first two letters of a single word,
// "?" for an empty name.
func (u *CurrentUser) Initials() string { return initialsOf(u.Name) }

func initialsOf(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '-' })
	var rs []rune
	switch len(words) {
	case 0:
		return "?"
	case 1:
		rs = []rune(words[0])
		if len(rs) > 2 {
			rs = rs[:2]
		}
	default:
		rs = []rune{[]rune(words[0])[0], []rune(words[len(words)-1])[0]}
	}
	return strings.ToUpper(string(rs))
}
