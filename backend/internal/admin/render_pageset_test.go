package admin

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

// built counts the page sets of one Admin that currently hold an executable copy.
func built(pages map[string]*pageSet) int {
	var lru *execLRU
	for _, ps := range pages {
		lru = ps.lru
		break
	}
	lru.mu.Lock()
	defer lru.mu.Unlock()
	n := 0
	for _, ps := range pages {
		if ps.t != nil {
			n++
		}
	}
	return n
}

func sortedKeys(pages map[string]*pageSet) []string {
	keys := make([]string, 0, len(pages))
	for k := range pages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestPageSetsKeepAtMostFourExecutable(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	keys := sortedKeys(pages)
	if len(keys) < 6 {
		t.Fatalf("need 6 pages, have %d", len(keys))
	}
	for _, k := range keys[:6] {
		if _, err := pages[k].tmpl(); err != nil {
			t.Fatal(err)
		}
		if n := built(pages); n > maxExecSets {
			t.Fatalf("%d executable sets after %s, want at most %d", n, k, maxExecSets)
		}
	}
	if n := built(pages); n != maxExecSets {
		t.Errorf("%d executable sets, want %d", n, maxExecSets)
	}
	if pages[keys[0]].t != nil || pages[keys[1]].t != nil {
		t.Error("the least recently used sets were not the ones evicted")
	}
}

func TestEvictedPageSetRendersAgain(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	keys := sortedKeys(pages)
	first := keys[0]
	render := func() string {
		tm, err := pages[first].tmpl()
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, 300)
		for _, d := range tm.Templates() {
			names = append(names, d.Name())
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	before := render()
	for _, k := range keys[1 : maxExecSets+2] {
		if _, err := pages[k].tmpl(); err != nil {
			t.Fatal(err)
		}
	}
	if pages[first].t != nil {
		t.Fatal("first page was not evicted")
	}
	if after := render(); after != before {
		t.Errorf("rebuilt page differs:\n%s\n%s", before, after)
	}
}

func TestPageSetsConcurrent(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	keys := sortedKeys(pages)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tm, err := pages[keys[(i+j)%8]].tmpl()
				if err != nil {
					t.Error(err)
					return
				}
				_ = tm.DefinedTemplates()
			}
		}()
	}
	wg.Wait()
	if n := built(pages); n > maxExecSets {
		t.Errorf("%d executable sets, want at most %d", n, maxExecSets)
	}
}
