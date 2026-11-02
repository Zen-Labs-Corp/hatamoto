package seosvc

import "testing"

func TestActorLabel(t *testing.T) {
	t.Parallel()

	if got := (Actor{KeyName: "deploy", Email: "o@example.com"}).Label(); got != "api-key:deploy" {
		t.Errorf("key label = %q", got)
	}
	if got := (Actor{Email: "o@example.com"}).Label(); got != "o@example.com" {
		t.Errorf("owner label = %q", got)
	}
}

func TestValidationErrorListsFieldsSorted(t *testing.T) {
	t.Parallel()

	e := &ValidationError{Fields: map[string]string{"b": "two", "a": "one"}}
	if got, want := e.Error(), "validation failed: a: one; b: two"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
