package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/InazumaV/V2bX/common/runtimetune"
	log "github.com/sirupsen/logrus"
)

// Nodes run with Log.Level "error"; the runtime summary and capacity
// warnings must still reach the log, otherwise the alerts never fire for
// anyone in practice.
func TestRuntimeStatsVisibleAtErrorLevel(t *testing.T) {
	std := log.StandardLogger()
	oldOut, oldLevel, oldRt := std.Out, std.Level, rtLog
	t.Cleanup(func() {
		std.SetOutput(oldOut)
		std.SetLevel(oldLevel)
		rtLog = oldRt
	})
	var buf bytes.Buffer
	std.SetOutput(&buf)
	std.SetLevel(log.ErrorLevel)
	initRuntimeLogger()

	log.Info("ordinary info line")
	logRuntimeStats(runtimetune.Stats{
		Total:       900 << 20,
		HeapLive:    600 << 20,
		MemoryLimit: 1000 << 20,
		Goroutines:  12000,
		GCCPUShare:  0.2,
		Load1:       1.9,
		NumCPU:      2,
		MemLimited:  true,
	}, true)

	out := buf.String()
	if strings.Contains(out, "ordinary info line") {
		t.Fatal("main logger must keep honouring Log.Level")
	}
	for _, want := range []string{"Runtime stats", "内存已接近", "CPU 负载已接近"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRuntimeLoggerSharesStdAtInfo(t *testing.T) {
	std := log.StandardLogger()
	oldLevel, oldRt := std.Level, rtLog
	t.Cleanup(func() {
		std.SetLevel(oldLevel)
		rtLog = oldRt
	})
	std.SetLevel(log.InfoLevel)
	initRuntimeLogger()
	if rtLog != std {
		t.Fatal("at Info or below the runtime lines should go through the main logger unchanged")
	}
}
