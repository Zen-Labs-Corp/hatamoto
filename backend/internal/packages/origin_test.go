package packages

import (
	"testing"

	"hatamoto.dev/core/internal/config"
)

func TestClassify(t *testing.T) {
	pub, err := ParsePublicKey(config.FirstPartyKey)
	if err != nil {
		t.Fatal(err)
	}
	cfgKey := KeyID(pub)
	tests := []struct {
		name, plugin, key string
		want              Origin
	}{
		{"searchconsole old key", "searchconsole", "a592a1276b3947b1", OriginFirstParty},
		{"searchconsole new key", "searchconsole", "adea8e00f5ac74ca", OriginFirstParty},
		{"searchconsole config key", "searchconsole", cfgKey, OriginFirstParty},
		{"gtm was folded into core and is community now", "gtm", "adea8e00f5ac74ca", OriginCommunity},
		{"google-analytics was folded into core and is community now", "google-analytics", cfgKey, OriginCommunity},
		{"off-list name is community", "fancy-widgets", "adea8e00f5ac74ca", OriginCommunity},
		{"off-list name config key", "fancy-widgets", cfgKey, OriginCommunity},
		{"unsigned list name", "searchconsole", "", OriginSelfBuilt},
		{"unsigned other name", "fancy-widgets", "", OriginSelfBuilt},
		{"other trusted key", "searchconsole", "0123456789abcdef", OriginSelfBuilt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.plugin, tt.key); got != tt.want {
				t.Fatalf("Classify(%q,%q) = %v, want %v", tt.plugin, tt.key, got, tt.want)
			}
			if got := ClassifyRow(Row{Name: tt.plugin, SignerKeyID: tt.key}); got != tt.want {
				t.Fatalf("ClassifyRow = %v, want %v", got, tt.want)
			}
		})
	}
}
