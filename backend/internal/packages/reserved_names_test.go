package packages

import "testing"

func TestIsReservedPluginName(t *testing.T) {
	for name, want := range map[string]bool{
		"webhooks": true, "gtm": true, "google-analytics": true,
		"searchconsole": false, "hello": false, "browse": false, "security": false, "": false, "GTM": false,
	} {
		if got := IsReservedPluginName(name); got != want {
			t.Errorf("IsReservedPluginName(%q) = %v, want %v", name, got, want)
		}
	}
	if ReservedPluginName != "webhooks" {
		t.Errorf("ReservedPluginName = %q, want webhooks", ReservedPluginName)
	}
	names := ReservedPluginNames()
	names[0] = "hello"
	if IsReservedPluginName("hello") || !IsReservedPluginName("webhooks") {
		t.Error("mutating ReservedPluginNames() result changed the reserved set")
	}
}
