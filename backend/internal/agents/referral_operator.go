package agents

// referralOperators maps an AI assistant referrer source (the `source` of a
// registry ai_referrers entry) to the operator that crawls on its behalf, so
// "pages fetched" of the Crawlers tab and "visitors sent" of the Traffic tab
// can sit on one row.
var referralOperators = map[string]string{
	"chatgpt":    "OpenAI",
	"perplexity": "Perplexity",
	"claude":     "Anthropic",
	"gemini":     "Google",
	"copilot":    "Microsoft",
	"meta_ai":    "Meta",
	"mistral":    "Mistral",
}

// referralWithoutOperator are assistant sources known to have no crawler
// operator row; their visitors are shown as "Other assistants".
var referralWithoutOperator = map[string]bool{"grok": true, "deepseek": true}

// AssistantOperator returns the crawler operator of an AI assistant referrer
// source. ok is false for sources without one (they belong in "Other assistants").
func AssistantOperator(source string) (operator string, ok bool) {
	operator, ok = referralOperators[source]
	return operator, ok
}

// OperatorFeedsSearch reports whether the operator also runs a search engine,
// that is whether the registry lists a classic search crawler for it. Visitors
// from such an engine arrive as Organic search, not as AI assistants, so
// "pages fetched, no assistant visitors" says nothing about what it gives back.
func OperatorFeedsSearch(operator string) bool {
	for _, a := range Default().Agents {
		if a.Operator == operator && a.Purpose == PurposeSearch {
			return true
		}
	}
	return false
}
