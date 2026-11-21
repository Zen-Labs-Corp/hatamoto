package designcheck

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"

	"hatamoto.dev/core/web"
)

// Violation is one design rule a module template breaks.
type Violation struct {
	File    string
	Line    int
	Rule    string
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d %s %s", v.File, v.Line, v.Rule, v.Message)
}

var (
	modClassDef   = regexp.MustCompile(`\.([A-Za-z_][\w-]*)`)
	modStyleTag   = regexp.MustCompile(`(?i)<style\b`)
	modStyleAttr  = regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`)
	modClassValue = regexp.MustCompile(`(?i)\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	coreClassesOnce sync.Once
	coreClasses     map[string]bool
	coreClassesErr  error
)

// CheckModule runs the template rules UI-01, UI-02, UI-03, UI-05, UI-07, UI-08,
// UI-09 and UI-10 on every .html file of fsys, plus MOD-01: a module template
// may use only classes that core's admin CSS defines and no <style> element or
// style attribute. Core's allowlist does not apply to modules, and CSS and JS
// files are not checked because a module ships templates only.
func CheckModule(fsys fs.FS) ([]Violation, error) {
	files, err := readUIFiles(fsys)
	if err != nil {
		return nil, err
	}
	classes, err := coreAdminClasses()
	if err != nil {
		return nil, err
	}
	var vs []Violation
	for _, f := range files {
		if !strings.HasSuffix(f.name, ".html") {
			continue
		}
		add := func(off int, rule, msg string) {
			vs = append(vs, Violation{File: f.name, Line: lineOf(f.src, off), Rule: rule, Message: msg})
		}
		for _, lint := range []func(uiFile, func(int, string, string)){
			lintUI01, lintUI02, lintUI03, lintUI05HTML, lintUI07, lintUI08, lintUI09, lintUI10,
		} {
			lint(f, add)
		}
		lintMOD01(f, classes, add)
	}
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].File != vs[j].File {
			return vs[i].File < vs[j].File
		}
		return vs[i].Line < vs[j].Line
	})
	return vs, nil
}

// lintMOD01 flags classes core CSS does not define, <style> and style=.
func lintMOD01(f uiFile, classes map[string]bool, add func(int, string, string)) {
	// {{...}} actions build values at render time; only literal words count.
	src := blankActions(f.src)
	for _, m := range modStyleTag.FindAllStringIndex(src, -1) {
		add(m[0], "MOD-01", "no <style> element: module pages use core's classes and components")
	}
	for _, m := range modStyleAttr.FindAllStringIndex(src, -1) {
		add(m[0], "MOD-01", "no style attribute: module pages use core's classes and components")
	}
	for _, m := range modClassValue.FindAllStringSubmatchIndex(src, -1) {
		start, end := m[2], m[3]
		if start < 0 {
			start, end = m[4], m[5]
		}
		for _, tok := range tokenSpans(src[start:end]) {
			c := src[start+tok[0] : start+tok[1]]
			if strings.HasPrefix(c, "-") || strings.HasSuffix(c, "-") || classes[c] {
				continue
			}
			add(start+tok[0], "MOD-01", fmt.Sprintf("class %q is not defined by core's admin CSS: use core's classes and components", c))
		}
	}
}

// tokenSpans returns the [start, end) offsets of the space-separated words in s.
func tokenSpans(s string) [][2]int {
	var out [][2]int
	start := -1
	for i, r := range s {
		space := r == ' ' || r == '\t' || r == '\n' || r == '\r'
		switch {
		case !space && start < 0:
			start = i
		case space && start >= 0:
			out = append(out, [2]int{start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(s)})
	}
	return out
}

// blankActions replaces every {{...}} with spaces, keeping offsets and newlines.
func blankActions(src string) string {
	return uiTemplateAct.ReplaceAllStringFunc(src, func(a string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, a)
	})
}

// coreAdminClasses is every class a core admin stylesheet defines.
func coreAdminClasses() (map[string]bool, error) {
	coreClassesOnce.Do(func() {
		coreClasses = map[string]bool{}
		fsys := web.Admin()
		coreClassesErr = fs.WalkDir(fsys, "css", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".css") {
				return err
			}
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			for _, m := range modClassDef.FindAllStringSubmatch(blankComments(string(b)), -1) {
				coreClasses[m[1]] = true
			}
			return nil
		})
	})
	return coreClasses, coreClassesErr
}
