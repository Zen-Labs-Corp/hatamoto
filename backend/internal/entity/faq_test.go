package entity

import (
	"reflect"
	"testing"
)

func TestExtractFAQItems(t *testing.T) {
	got := ExtractFAQItems(`<p>Intro</p><div class="faq"><div class="faq-item"><h3 class="faq-q">What is <em>it</em>?</h3><div class="faq-a"><p>A thing.</p><p>Really.</p></div></div>` +
		`<div class="faq-item"><h3 class="faq-q">No answer?</h3><div class="faq-a"></div></div>` +
		`<div class="faq-item"><h3 class="faq-q">Second?</h3><div class="faq-a">Yes.</div></div></div>`)
	want := []FAQItem{{Question: "What is it?", Answer: "A thing. Really."}, {Question: "Second?", Answer: "Yes."}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if ExtractFAQItems("<p>none</p>") != nil {
		t.Fatal("content without a FAQ block has no items")
	}
}
