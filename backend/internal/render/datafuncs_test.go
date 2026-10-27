package render

import (
	"html/template"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// spyData records the QuerySpec and AttrValues arguments the funcs pass.
type spyData struct {
	fakeData
	q      QuerySpec
	attr   [3]any
	result QueryResult
}

func (s *spyData) Query(q QuerySpec) QueryResult { s.q = q; return s.result }
func (s *spyData) AttrValues(schema, attr string, limit int) []AttrCount {
	s.attr = [3]any{schema, attr, limit}
	return []AttrCount{{Value: "A", Count: 2}, {Value: "B", Count: 1}}
}

func execFuncs(t *testing.T, ds DataSource, src string, data any) string {
	t.Helper()
	fm := baseFuncs()
	for k, f := range dataFuncs(ds) {
		fm[k] = f
	}
	var b strings.Builder
	if err := template.Must(template.New("t").Funcs(fm).Parse(src)).Execute(&b, data); err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return b.String()
}

func TestQueryFuncBuildsClampedSpec(t *testing.T) {
	spy := &spyData{result: QueryResult{Items: fakeItems, Total: 30, Page: 2, Pages: 3}}
	got := execFuncs(t, spy, `{{$r := query (dict "schema" "blog" "attr" "series_slug" "eq" "x" "author" "jo" "term" "category:news" "order" "attr:part" "limit" 500 "page" "2")}}{{$r.Total}}/{{$r.Page}}/{{$r.Pages}}/{{$r.Limit}}/{{len $r.Items}}`, nil)
	want := QuerySpec{Schema: "blog", Attr: "series_slug", Eq: "x", EqSet: true, Author: "jo", Term: "category:news", Order: "attr:part", Limit: MaxQueryLimit, Page: 2}
	if spy.q != want {
		t.Errorf("spec = %+v, want %+v", spy.q, want)
	}
	if got != "30/2/3/100/2" {
		t.Errorf("result = %q", got)
	}

	// Defaults, and caps on the page.
	execFuncs(t, spy, `{{query (dict "schema" "blog" "page" 999999)}}`, nil)
	if spy.q.Limit != DefaultQueryLimit || spy.q.Page != MaxQueryPage {
		t.Errorf("defaults/caps = limit %d page %d", spy.q.Limit, spy.q.Page)
	}
	execFuncs(t, spy, `{{query (dict "schema" "blog" "page" -3 "limit" -1)}}`, nil)
	if spy.q.Limit != DefaultQueryLimit || spy.q.Page != 1 {
		t.Errorf("negative = limit %d page %d", spy.q.Limit, spy.q.Page)
	}
}

func TestQueryFuncFailOpen(t *testing.T) {
	// No schema: the DataSource is not asked at all.
	spy := &spyData{result: QueryResult{Items: fakeItems, Total: 2}}
	if got := execFuncs(t, spy, `{{$r := query (dict "attr" "x")}}{{len $r.Items}}/{{$r.Page}}/{{$r.Pages}}`, nil); got != "0/1/1" || spy.q.Schema != "" {
		t.Errorf("no schema = %q (spec %+v)", got, spy.q)
	}
	// No DataSource, and a source that reports no pages.
	if got := execFuncs(t, nil, `{{$r := query (dict "schema" "blog")}}{{len $r.Items}}/{{$r.Pages}}/{{$r.Limit}}`, nil); got != "0/1/12" {
		t.Errorf("nil source = %q", got)
	}
	if got := execFuncs(t, &spyData{}, `{{$r := query (dict "schema" "blog" "limit" 5)}}{{$r.Page}}/{{$r.Pages}}/{{$r.Limit}}`, nil); got != "1/1/5" {
		t.Errorf("empty result = %q", got)
	}
}

func TestAttrValuesFunc(t *testing.T) {
	spy := &spyData{}
	got := execFuncs(t, spy, `{{range attrValues "blog" "category" 9999}}{{.Value}}={{.Count}};{{end}}`, nil)
	if got != "A=2;B=1;" || spy.attr != [3]any{"blog", "category", maxAttrValues} {
		t.Errorf("attrValues = %q, args %v", got, spy.attr)
	}
	if got := execFuncs(t, nil, `{{len (attrValues "blog" "category" 5)}}`, nil); got != "0" {
		t.Errorf("nil source = %q", got)
	}
}

func TestPageNum(t *testing.T) {
	for _, c := range []struct {
		name string
		v    any
		want int
	}{
		{"unset", &pageData{}, 1},
		{"page view", PageView{PageNum: 4}, 4},
		{"page data", &pageData{PageView: PageView{PageNum: 7}}, 7},
		{"capped", &PageView{PageNum: 99999}, MaxQueryPage},
		{"negative", &PageView{PageNum: -2}, 1},
		{"not a page", "x", 1},
		{"nil", nil, 1},
	} {
		if got := pageNum(c.v); got != c.want {
			t.Errorf("%s: pageNum = %d, want %d", c.name, got, c.want)
		}
	}
	if got := execFuncs(t, nil, `{{pageNum .}}`, &pageData{PageView: PageView{PageNum: 3}}); got != "3" {
		t.Errorf("in a template = %q", got)
	}
}

func TestQueryEqPresence(t *testing.T) {
	spy := &spyData{result: QueryResult{Items: fakeItems, Total: 2}}
	// A nil eq (a missing attribute) matches nothing: the source is not asked.
	got := execFuncs(t, spy, `{{$r := query (dict "schema" "blog" "attr" "series_slug" "eq" .Missing)}}{{len $r.Items}}/{{$r.Total}}`, map[string]any{"Missing": nil})
	if got != "0/0" || spy.q.Schema != "" {
		t.Errorf("nil eq = %q (spec %+v)", got, spy.q)
	}
	// An empty eq is still an equality test, a bool is spelled true/false.
	execFuncs(t, spy, `{{query (dict "schema" "blog" "attr" "a" "eq" "")}}`, nil)
	if !spy.q.EqSet || spy.q.Eq != "" {
		t.Errorf("empty eq = %+v", spy.q)
	}
	execFuncs(t, spy, `{{query (dict "schema" "blog" "attr" "featured" "eq" true)}}`, nil)
	if !spy.q.EqSet || spy.q.Eq != "true" {
		t.Errorf("bool eq = %+v", spy.q)
	}
	execFuncs(t, spy, `{{query (dict "schema" "blog" "attr" "a")}}`, nil)
	if spy.q.EqSet {
		t.Errorf("no eq key must stay an existence test: %+v", spy.q)
	}
}

func TestQueryStatus(t *testing.T) {
	spy := &spyData{}
	for status, want := range map[string]bool{"": false, "published": false, "published+scheduled": true, "draft": false, "all": false} {
		execFuncs(t, spy, `{{query (dict "schema" "blog" "status" "`+status+`")}}`, nil)
		if spy.q.IncludeScheduled != want {
			t.Errorf("status %q: IncludeScheduled = %v, want %v", status, spy.q.IncludeScheduled, want)
		}
	}
}

func TestAttrValuesFuncFailOpenTypes(t *testing.T) {
	spy := &spyData{}
	execFuncs(t, spy, `{{range attrValues "blog" .A .N}}{{end}}`, map[string]any{"A": nil, "N": 7.0})
	if spy.attr != [3]any{"blog", "", 7} {
		t.Errorf("args = %v", spy.attr)
	}
	execFuncs(t, spy, `{{range attrValues "blog" "c" "x"}}{{end}}`, nil)
	if spy.attr[2] != 1 {
		t.Errorf("bad limit = %v", spy.attr)
	}
}

func TestQueryParam(t *testing.T) {
	q := url.Values{"days": {"30", "7"}, "long": {strings.Repeat("é", 300)}}
	section := preparedSection{Page: sectionPage{Params: q, PageNum: 5}}
	for _, v := range []any{&PageView{Params: q}, &pageData{PageView: PageView{Params: q}}, section, &section} {
		if got := queryParam(v, "days"); got != "30" {
			t.Errorf("%T: days = %q", v, got)
		}
		if got := queryParam(v, "none"); got != "" {
			t.Errorf("%T: absent = %q", v, got)
		}
	}
	if got := queryParam(&PageView{Params: q}, "long"); len(got) > maxParamLen || !utf8.ValidString(got) || got == "" {
		t.Errorf("long = %d bytes valid=%v", len(got), utf8.ValidString(got))
	}
	if queryParam(nil, "x") != "" || queryParam("x", "x") != "" {
		t.Error("unknown data must give empty")
	}
	if got := pageNum(section); got != 5 {
		t.Errorf("pageNum in a section = %d", got)
	}
	if got := execFuncs(t, nil, `{{queryParam . "days"}}`, &pageData{PageView: PageView{Params: q}}); got != "30" {
		t.Errorf("in a template = %q", got)
	}
}

func TestToStrLargeFloatsAreNotExponent(t *testing.T) {
	t.Parallel()
	for in, want := range map[float64]string{1000000: "1000000", 2.5: "2.5", 3: "3", 1e21: "1000000000000000000000"} {
		if got := toStr(in); got != want {
			t.Errorf("toStr(%v) = %q, want %q", in, got, want)
		}
	}
}
