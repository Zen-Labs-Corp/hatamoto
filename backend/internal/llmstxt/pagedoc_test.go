package llmstxt

import (
	"testing"

	"hatamoto.dev/core/internal/builder"
)

const refID = "11111111-1111-1111-1111-111111111111"

func mustDoc(t *testing.T, raw string) *builder.Doc {
	t.Helper()
	d, err := builder.ParseExact([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDocToMarkdown(t *testing.T) {
	d := mustDoc(t, `{"v":2,"nodes":[
	 {"id":"a","t":"container","c":[
	  {"id":"b","t":"heading","p":{"text":"Welcome","level":1}},
	  {"id":"c","t":"text","p":{"html":"<p>We build <a href=\"https://acme.example/x\">sites</a>.</p>"}},
	  {"id":"d","t":"text","p":{"text":"Plain words"}},
	  {"id":"e","t":"image","p":{"src":"https://acme.example/i.png","alt":"Office"}},
	  {"id":"f","t":"button","p":{"text":"Contact","href":"https://acme.example/contact"}},
	  {"id":"g","t":"text","p":{"text":"Secret","hidden":true}},
	  {"id":"h","t":"spacer"},
	  {"id":"i","t":"divider"},
	  {"id":"j","t":"slot"}
	 ]},
	 {"id":"k","t":"component","p":{"type":"cards","title":"Our work","content":{"subtitle":"Recent","items":[{"title":"Alpha","body":"First job"},{"title":"Beta","description":"Second job"}]}}}
	]}`)
	want := "# Welcome\n\nWe build [sites](https://acme.example/x).\n\nPlain words\n\n![Office](https://acme.example/i.png)\n\n[Contact](https://acme.example/contact)\n\n## Our work\n\nRecent\n\n## Alpha\n\nFirst job\n\n## Beta\n\nSecond job"
	if got := DocToMarkdown(d, nil); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestDocToMarkdownRefs(t *testing.T) {
	page := mustDoc(t, `{"v":2,"nodes":[{"id":"a","t":"ref","p":{"block":"`+refID+`"}},{"id":"b","t":"text","p":{"text":"After"}}]}`)
	// The block references itself: expansion must stop at RefMaxDepth.
	block := mustDoc(t, `{"v":2,"nodes":[{"id":"x","t":"text","p":{"text":"Block"}},{"id":"y","t":"ref","p":{"block":"`+refID+`"}}]}`)
	calls := 0
	resolve := func(id string) *builder.Doc {
		calls++
		if id == refID {
			return block
		}
		return nil
	}
	got := DocToMarkdown(page, resolve)
	want := "Block\n\nBlock\n\nBlock\n\nAfter"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if calls != builder.RefMaxDepth {
		t.Fatalf("resolver called %d times, want %d", calls, builder.RefMaxDepth)
	}
	if got := DocToMarkdown(page, nil); got != "After" {
		t.Fatalf("nil resolver: %q", got)
	}
	if DocToMarkdown(nil, nil) != "" {
		t.Fatal("nil doc not empty")
	}
}
