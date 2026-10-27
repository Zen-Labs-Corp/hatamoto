package wpimport

// Selection narrows a Dump to what the owner chose to import.
type Selection struct {
	// Types lists the post types to import ("post", "page", a custom type);
	// nil means all of them.
	Types []string
	// Drafts includes drafts, pending, private and password-protected items
	// (imported as drafts). Off, they are left out.
	Drafts bool
	// Menus includes the navigation menus.
	Menus bool
}

// AllSelected imports everything the source has.
func AllSelected() Selection { return Selection{Drafts: true, Menus: true} }

// Apply removes what the selection leaves out of d. It never changes the
// site-level data, authors, terms or the list of old URLs: the audit still
// asks for every old URL, so what was left out shows up as not covered.
func (s Selection) Apply(d *Dump) {
	if s.Types != nil {
		keep := map[string]bool{}
		for _, t := range s.Types {
			keep[t] = true
		}
		d.Items = filterItems(d.Items, func(it Item) bool { return keep[it.Type] })
	}
	if !s.Drafts {
		d.Items = filterItems(d.Items, func(it Item) bool {
			return !it.Protected && it.Status != wpDraft && it.Status != wpPending && it.Status != wpPrivate
		})
	}
	if !s.Menus {
		d.Menus = nil
	}
}

func filterItems(items []Item, keep func(Item) bool) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}
