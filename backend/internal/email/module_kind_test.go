package email

import "testing"

func TestModuleKindStorage(t *testing.T) {
	if KindModule.StoresBody() {
		t.Error("module mail may carry links or tokens: the body is not stored")
	}
	if !KindModule.StoresRecipients() {
		t.Error("recipients of module mail are kept")
	}
}
