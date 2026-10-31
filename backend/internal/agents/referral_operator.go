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
