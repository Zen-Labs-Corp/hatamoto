package agents

import "testing"

func TestAssistantOperator(t *testing.T) {
	if op, ok := AssistantOperator("chatgpt"); !ok || op != "OpenAI" {
		t.Fatalf("chatgpt = %q, %v", op, ok)
	}
	if op, ok := AssistantOperator("grok"); ok || op != "" {
		t.Fatalf("grok = %q, %v", op, ok)
	}
	if _, ok := AssistantOperator("nope"); ok {
		t.Fatal("unknown source mapped")
	}
}

// TestEveryAIReferrerHasAnOperatorDecision fails when the registry gains an
// assistant source that is neither mapped nor listed as having no operator,
// so its visitors are never dropped from the give-vs-get table silently.
func TestEveryAIReferrerHasAnOperatorDecision(t *testing.T) {
	for _, ref := range Default().AIReferrers {
		_, mapped := referralOperators[ref.Source]
		if !mapped && !referralWithoutOperator[ref.Source] {
			t.Errorf("ai_referrers source %q has no operator: map it in referral_operator.go or list it as operator-less", ref.Source)
		}
		if mapped && referralWithoutOperator[ref.Source] {
			t.Errorf("source %q is both mapped and operator-less", ref.Source)
		}
	}
}
