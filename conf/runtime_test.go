package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func intPtr(v int) *int { return &v }

func TestRuntimeConfigResolveDefaults(t *testing.T) {
	var nilCfg *RuntimeConfig
	got := nilCfg.Resolve()
	if got.GCPercent != DefaultGCPercent || got.MemoryLimitPercent != DefaultMemoryLimitPercent || got.MemoryLimit != "" || got.PprofPort != 0 || got.StatsInterval != DefaultStatsInterval {
		t.Fatalf("nil section must resolve to defaults, got %+v", got)
	}
	got = (&RuntimeConfig{}).Resolve()
	if got.GCPercent != DefaultGCPercent || got.MemoryLimitPercent != DefaultMemoryLimitPercent {
		t.Fatalf("empty section must resolve to defaults, got %+v", got)
	}
}

func TestRuntimeConfigResolveExplicit(t *testing.T) {
	got := (&RuntimeConfig{
		GCPercent:          intPtr(100),
		MemoryLimitPercent: intPtr(0),
		MemoryLimit:        "2GiB",
		StatsInterval:      intPtr(60),
		PprofPort:          6060,
	}).Resolve()
	if got.GCPercent != 100 || got.MemoryLimitPercent != 0 || got.MemoryLimit != "2GiB" || got.StatsInterval != 60 || got.PprofPort != 6060 {
		t.Fatalf("explicit values not honoured: %+v", got)
	}
	if got := (&RuntimeConfig{StatsInterval: intPtr(0)}).Resolve(); got.StatsInterval != 0 {
		t.Fatalf("StatsInterval 0 should disable, got %d", got.StatsInterval)
	}
	if got := (&RuntimeConfig{StatsInterval: intPtr(3)}).Resolve(); got.StatsInterval != DefaultStatsInterval || len(got.Warnings) != 1 {
		t.Fatalf("tiny StatsInterval must fall back with a warning: %+v", got)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", got.Warnings)
	}

	// 0 keeps Go's default GOGC; tiny values are rejected.
	if got := (&RuntimeConfig{GCPercent: intPtr(0)}).Resolve(); got.GCPercent != 0 {
		t.Fatalf("GCPercent 0 should mean 'leave default', got %d", got.GCPercent)
	}
	got = (&RuntimeConfig{GCPercent: intPtr(5), MemoryLimitPercent: intPtr(-1), PprofPort: 70000}).Resolve()
	if got.GCPercent != DefaultGCPercent || got.MemoryLimitPercent != DefaultMemoryLimitPercent || got.PprofPort != 0 {
		t.Fatalf("invalid values must fall back to defaults: %+v", got)
	}
	if len(got.Warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %v", got.Warnings)
	}
}

func TestConfLoadsRuntimeSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{
  "Log": {"Level": "info"},
  "Runtime": {"GCPercent": 60, "MemoryLimitPercent": 80, "MemoryLimit": "1536MiB", "PprofPort": 6061},
  "Cores": [],
  "Nodes": []
}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New()
	if err := c.LoadFromPath(p); err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	r := c.RuntimeConfig.Resolve()
	if r.GCPercent != 60 || r.MemoryLimitPercent != 80 || r.MemoryLimit != "1536MiB" || r.PprofPort != 6061 {
		t.Fatalf("Runtime section not parsed: %+v", r)
	}

	// A config without the section must still resolve to the defaults.
	if err := os.WriteFile(p, []byte(`{"Log": {"Level": "info"}, "Cores": [], "Nodes": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c = New()
	if err := c.LoadFromPath(p); err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	r = c.RuntimeConfig.Resolve()
	if r.GCPercent != DefaultGCPercent || r.MemoryLimitPercent != DefaultMemoryLimitPercent {
		t.Fatalf("missing Runtime section must use defaults: %+v", r)
	}
}
