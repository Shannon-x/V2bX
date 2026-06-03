package json5

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestPrepHonorsExplicitLimit pins the W6 / audit #50 size cap.
func TestPrepHonorsExplicitLimit(t *testing.T) {
	// Construct a payload larger than the limit.
	big := bytes.Repeat([]byte("a"), 1024)
	_, err := io.ReadAll(NewTrimNodeReaderLimit(bytes.NewReader(big), 256))
	if err == nil {
		t.Fatalf("expected size-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "size cap") && !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-cap message, got: %v", err)
	}
}

// TestPrepDefaultLimitAllowsTypicalConfig pins that the default ceiling
// is generous enough for a realistic V2bX node config (typically <100 KiB).
func TestPrepDefaultLimitAllowsTypicalConfig(t *testing.T) {
	typical := bytes.Repeat([]byte("{}"), 50_000) // ~100 KiB
	got, err := io.ReadAll(NewTrimNodeReader(bytes.NewReader(typical)))
	if err != nil {
		t.Fatalf("default-limit reader failed on 100 KiB payload: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected non-empty data through trim reader")
	}
}

// TestPrepDefaultLimitRejectsHugePayload pins the safety ceiling. The
// payload here is just over DefaultMaxBytes — must error rather than
// silently truncate or OOM.
func TestPrepDefaultLimitRejectsHugePayload(t *testing.T) {
	// 70 MiB > DefaultMaxBytes (64 MiB).
	huge := bytes.NewReader(bytes.Repeat([]byte{'x'}, 70<<20))
	_, err := io.ReadAll(NewTrimNodeReader(huge))
	if err == nil {
		t.Fatalf("expected size-cap error on 70 MiB payload, got nil")
	}
}
