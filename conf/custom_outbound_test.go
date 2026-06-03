package conf

import "testing"

// TestCustomOutboundDefaultsAreSafe pins the W6 / audit #8 safe-by-default
// rule: a nil or empty CustomOutboundConfig MUST allow only freedom and
// blackhole — not vmess / socks / http / etc. that could route real
// traffic through panel-controlled remotes.
func TestCustomOutboundDefaultsAreSafe(t *testing.T) {
	cases := []struct {
		proto       string
		wantAllowed bool
	}{
		{"freedom", true},
		{"blackhole", true},
		{"vmess", false},
		{"socks", false},
		{"http", false},
		{"trojan", false},
		{"shadowsocks", false},
	}
	for _, c := range cases {
		got := IsCustomOutboundAllowed(nil, c.proto)
		if got != c.wantAllowed {
			t.Errorf("nil config, IsCustomOutboundAllowed(%q) = %v, want %v", c.proto, got, c.wantAllowed)
		}
	}
}

// TestCustomOutboundExplicitWidening pins the legacy escape hatch: setting
// AllowedProtocols to ["*"] restores the pre-W6 permissive behavior.
func TestCustomOutboundExplicitWidening(t *testing.T) {
	cfg := &CustomOutboundConfig{AllowedProtocols: []string{"*"}}
	for _, proto := range []string{"vmess", "socks", "freedom", "anything"} {
		if !IsCustomOutboundAllowed(cfg, proto) {
			t.Errorf(`AllowedProtocols=["*"] but IsCustomOutboundAllowed(%q) = false; want true`, proto)
		}
	}
}

// TestCustomOutboundExplicitList pins the per-protocol allow-list path.
func TestCustomOutboundExplicitList(t *testing.T) {
	cfg := &CustomOutboundConfig{AllowedProtocols: []string{"freedom", "socks"}}
	if !IsCustomOutboundAllowed(cfg, "socks") {
		t.Error("socks should be allowed when in list")
	}
	if !IsCustomOutboundAllowed(cfg, "freedom") {
		t.Error("freedom should be allowed when in list")
	}
	if IsCustomOutboundAllowed(cfg, "vmess") {
		t.Error("vmess NOT in list — must be rejected")
	}
	if IsCustomOutboundAllowed(cfg, "blackhole") {
		t.Error("blackhole NOT in list — must be rejected (list overrides default safe-list)")
	}
}

// TestCustomOutboundExplicitDisable pins Enabled=false rejecting everything.
func TestCustomOutboundExplicitDisable(t *testing.T) {
	disabled := false
	cfg := &CustomOutboundConfig{Enabled: &disabled}
	if IsCustomOutboundEnabled(cfg) {
		t.Error("Enabled=false should report not-enabled")
	}
}

func TestCustomOutboundNilEnabledMeansOn(t *testing.T) {
	if !IsCustomOutboundEnabled(nil) {
		t.Error("nil config should still be enabled (safe whitelist applies)")
	}
	if !IsCustomOutboundEnabled(&CustomOutboundConfig{}) {
		t.Error("config with nil Enabled pointer should be enabled (safe whitelist applies)")
	}
}
