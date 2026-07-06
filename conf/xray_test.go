package conf

import (
	"encoding/json"
	"testing"
)

// An existing config.json that only sets Level/ErrorPath must pick up the
// new rotation defaults on unmarshal — that is what makes a plain binary
// update enough to bound connection-log growth.
func TestXrayLogConfigMergeKeepsRotationDefaults(t *testing.T) {
	raw := `{
		"Type": "xray",
		"Log": {"Level": "error", "ErrorPath": "/etc/V2bX/error.log"}
	}`
	var cc CoreConfig
	if err := json.Unmarshal([]byte(raw), &cc); err != nil {
		t.Fatal(err)
	}
	lc := cc.XrayConfig.LogConfig
	if lc.Level != "error" || lc.ErrorPath != "/etc/V2bX/error.log" {
		t.Fatalf("user-set fields lost: %+v", lc)
	}
	if lc.AccessPath != "" {
		t.Fatalf("AccessPath: got %q, want empty (resolved to default at core build)", lc.AccessPath)
	}
	if lc.MaxSize != 100 || lc.MaxBackups != 0 || lc.MaxDays != 90 || !lc.Compress {
		t.Fatalf("rotation defaults not preserved: %+v", lc)
	}
}

func TestXrayLogConfigExplicitRotationOverrides(t *testing.T) {
	raw := `{
		"Type": "xray",
		"Log": {"AccessPath": "none", "MaxSize": 10, "MaxDays": 7, "Compress": false}
	}`
	var cc CoreConfig
	if err := json.Unmarshal([]byte(raw), &cc); err != nil {
		t.Fatal(err)
	}
	lc := cc.XrayConfig.LogConfig
	if lc.AccessPath != "none" {
		t.Fatalf("AccessPath: got %q, want none", lc.AccessPath)
	}
	if lc.MaxSize != 10 || lc.MaxDays != 7 || lc.Compress {
		t.Fatalf("explicit overrides not applied: %+v", lc)
	}
}
