package server

import (
	"testing"

	"hatamoto.dev/core/internal/config"
)

func TestPluginClientKeysNeedSecret(t *testing.T) {
	if pluginClientKeys(nil) != nil {
		t.Error("nil config must give no deriver")
	}
	if pluginClientKeys(&config.Config{}) != nil {
		t.Error("empty secret must give no deriver")
	}
	if pluginClientKeys(&config.Config{Secret: "s"}) == nil {
		t.Error("a secret must give a deriver")
	}
}
