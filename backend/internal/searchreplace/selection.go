package searchreplace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MaxSelection is the most tokens one apply accepts.
const MaxSelection = 2000

// Menu item fields a search can change.
const (
	fieldLabel = "label"
	fieldURL   = "url"
)

// Selection is the set of matches a person chose in the preview.
type Selection struct {
	Entities map[uuid.UUID]*entitySel
	Menus    map[menuKey]*menuSel
	Count    int
}

type entitySel struct {
	Version int
	picks   map[string]map[int]bool // path -> occurrence numbers
}

type menuKey struct {
	Item  uuid.UUID
	Field string
}

type menuSel struct {
	Menu  uuid.UUID
	Hash  string // of the value as previewed
	picks map[int]bool
}

func (s *entitySel) pick(path string, i int) bool { return s.picks[path][i] }

// entityToken names one match in an entity: the version it was seen at, the
// attribute path and the occurrence number.
func entityToken(id uuid.UUID, version int, path string, idx int) string {
	return fmt.Sprintf("e|%s|%d|%s|%d", id, version, url.PathEscape(path), idx)
}

// menuToken names one match in a menu item field, with a hash of the value it
// was seen in.
func menuToken(menuID, itemID uuid.UUID, field string, idx int, value string) string {
	return fmt.Sprintf("m|%s|%s|%s|%d|%s", menuID, itemID, field, idx, valueHash(value))
}

func valueHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:4])
}

// ParseSelection reads the posted match tokens. It refuses more than
// MaxSelection and any token it does not understand.
func ParseSelection(vals []string) (Selection, error) {
	sel := Selection{Entities: map[uuid.UUID]*entitySel{}, Menus: map[menuKey]*menuSel{}}
	if len(vals) > MaxSelection {
		return sel, fmt.Errorf("select at most %d matches at a time", MaxSelection)
	}
	for _, v := range vals {
		parts := strings.Split(v, "|")
		if err := sel.add(parts); err != nil {
			return sel, fmt.Errorf("unreadable selection: %q", v)
		}
		sel.Count++
	}
	return sel, nil
}

func (sel *Selection) add(p []string) error {
	switch {
	case len(p) == 5 && p[0] == "e":
		id, err := uuid.Parse(p[1])
		if err != nil {
			return err
		}
		ver, err := strconv.Atoi(p[2])
		if err != nil {
			return err
		}
		path, err := url.PathUnescape(p[3])
		if err != nil {
			return err
		}
		idx, err := strconv.Atoi(p[4])
		if err != nil || idx < 0 {
			return fmt.Errorf("bad index")
		}
		es := sel.Entities[id]
		if es == nil {
			es = &entitySel{Version: ver, picks: map[string]map[int]bool{}}
			sel.Entities[id] = es
		}
		if es.picks[path] == nil {
			es.picks[path] = map[int]bool{}
		}
		es.picks[path][idx] = true
	case len(p) == 6 && p[0] == "m":
		menuID, err := uuid.Parse(p[1])
		if err != nil {
			return err
		}
		item, err := uuid.Parse(p[2])
		if err != nil {
			return err
		}
		if p[3] != fieldLabel && p[3] != fieldURL {
			return fmt.Errorf("bad field")
		}
		idx, err := strconv.Atoi(p[4])
		if err != nil || idx < 0 {
			return fmt.Errorf("bad index")
		}
		k := menuKey{item, p[3]}
		ms := sel.Menus[k]
		if ms == nil {
			ms = &menuSel{Menu: menuID, Hash: p[5], picks: map[int]bool{}}
			sel.Menus[k] = ms
		}
		ms.picks[idx] = true
	default:
		return fmt.Errorf("unknown token")
	}
	return nil
}
