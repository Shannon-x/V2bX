package conf

import "testing"

// TestCustomOutboundDefaultsArePermissive pins the W6 / audit #8 behavior:
// without explicit hardening, panel-pushed outbounds of ANY protocol are
// accepted — panel-driven routing is a core V2bX feature and must keep
// working with zero node-side config.
func TestCustomOutboundDefaultsArePermissive(t *testing.T) {
	for _, proto := range []string{
		"freedom", "blackhole",
		"vmess", "vless", "trojan",
		"socks", "http", "shadowsocks",
		"anything-the-panel-sends",
	} {
		if !IsCustomOutboundAllowed(nil, proto) {
			t.Errorf("nil config: %q must be allowed (default is permissive)", proto)
		}
		if !IsCustomOutboundAllowed(&CustomOutboundConfig{}, proto) {
			t.Errorf("empty config: %q must be allowed (default is permissive)", proto)
		}
	}
}

// TestCustomOutboundExplicitDisable pins the hard opt-out: Enabled=false
// rejects everything.
func TestCustomOutboundExplicitDisable(t *testing.T) {
	disabled := false
	cfg := &CustomOutboundConfig{Enabled: &disabled}
	if IsCustomOutboundEnabled(cfg) {
		t.Fatal("Enabled=false should report not-enabled")
	}
}

// TestCustomOutboundExplicitList pins the per-protocol allow-list path:
// once AllowedProtocols is set, the list becomes authoritative.
func TestCustomOutboundExplicitList(t *testing.T) {
	cfg := &CustomOutboundConfig{AllowedProtocols: []string{"freedom", "socks"}}
	for _, proto := range []string{"freedom", "socks"} {
		if !IsCustomOutboundAllowed(cfg, proto) {
			t.Errorf("%q should be allowed (in explicit list)", proto)
		}
	}
	for _, proto := range []string{"vmess", "trojan", "shadowsocks"} {
		if IsCustomOutboundAllowed(cfg, proto) {
			t.Errorf("%q NOT in explicit list — must be rejected", proto)
		}
	}
}

// TestCustomOutboundWildcardEqualsDefault pins that ["*"] behaves exactly
// like the empty/nil default — it's an explicit form of "accept all".
func TestCustomOutboundWildcardEqualsDefault(t *testing.T) {
	cfg := &CustomOutboundConfig{AllowedProtocols: []string{"*"}}
	for _, proto := range []string{"vmess", "socks", "freedom", "anything"} {
		if !IsCustomOutboundAllowed(cfg, proto) {
			t.Errorf(`AllowedProtocols=["*"]: %q must be allowed`, proto)
		}
	}
}

func TestCustomOutboundEnabledDefaults(t *testing.T) {
	if !IsCustomOutboundEnabled(nil) {
		t.Error("nil config should default to enabled")
	}
	if !IsCustomOutboundEnabled(&CustomOutboundConfig{}) {
		t.Error("empty config should default to enabled")
	}
	enabled := true
	if !IsCustomOutboundEnabled(&CustomOutboundConfig{Enabled: &enabled}) {
		t.Error("Enabled=true should report enabled")
	}
}
