package llmstxt

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// Snapshot keys of the file-level items; entity items use Item.Key.
const (
	keySiteName    = "site:name"
	keySiteSummary = "site:summary"
	keySiteDetails = "site:details"
)

// SnapItem is what the snapshot remembers of one listed item. Path is
// base-relative so builds under another base URL do not look like changes.
type SnapItem struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	Desc string `json:"desc,omitempty"`
	Job  string `json:"job,omitempty"`
}

// Snapshot maps "<locale>|<entity id>" (or a "site:" key) to its item.
type Snapshot map[string]SnapItem

// Change is one entry of the "recently changed" list.
type Change struct {
	At   time.Time `json:"when"`
	Text string    `json:"text"`
}

// SnapshotOf records the site-level text and every listed item.
func SnapshotOf(site Site, sections []Section) Snapshot {
	snap := Snapshot{
		keySiteName:    {Name: site.Name},
		keySiteSummary: {Desc: site.Summary},
		keySiteDetails: {Desc: site.Details},
	}
	for _, sec := range sections {
		for _, it := range sec.Items {
			if it.Key == "" {
				continue
			}
			snap[it.Key] = SnapItem{Name: it.Name, Path: it.Path, Desc: it.Desc, Job: it.Job}
		}
	}
	return snap
}

// Diff returns the changes from old to cur, sorted by text. A nil old is the
// first build: it only records a baseline, so there are no changes. excluded
// maps an entity key to why a still-published entity left the file ("opted
// out", "noindex").
func Diff(old, cur Snapshot, excluded map[string]string, at time.Time) []Change {
	if old == nil {
		return nil
	}
	var out []Change
	add := func(text string) { out = append(out, Change{At: at, Text: text}) }

	for key, c := range cur {
		o, ok := old[key]
		if !ok {
			if !strings.HasPrefix(key, "site:") {
				add(c.Name + ": added")
			}
			continue
		}
		if key == keySiteName {
			if o.Name != c.Name {
				add("Site name: changed")
			}
			continue
		}
		if key == keySiteSummary || key == keySiteDetails {
			if o.Desc != c.Desc {
				add("Site " + strings.TrimPrefix(key, "site:") + ": changed")
			}
			continue
		}
		var fields []string
		if o.Name != c.Name {
			fields = append(fields, "title")
		}
		if o.Path != c.Path {
			fields = append(fields, "address")
		}
		if o.Job != c.Job {
			fields = append(fields, "job title")
		}
		if o.Desc != c.Desc {
			fields = append(fields, "description")
		}
		if len(fields) > 0 {
			add(c.Name + ": " + joinAnd(fields) + " changed")
		}
	}
	for key, o := range old {
		if _, ok := cur[key]; ok || strings.HasPrefix(key, "site:") {
			continue
		}
		text := o.Name + ": removed"
		if why := excluded[key]; why != "" {
			text += " (" + why + ")"
		}
		add(text)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}

// Keep returns add followed by prev, newest first, at most n entries.
func Keep(prev, add []Change, n int) []Change {
	all := append(slices.Clone(add), prev...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if len(all) > n {
		all = all[:n]
	}
	return all
}

func joinAnd(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
