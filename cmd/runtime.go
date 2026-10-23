package cmd

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/InazumaV/V2bX/common/runtimetune"
	"github.com/InazumaV/V2bX/conf"
	log "github.com/sirupsen/logrus"
)

// rtLog carries the Runtime section's own output: what tuning took effect,
// the periodic "Runtime stats" line and the capacity warnings. It writes to
// the main log's destination but never filters below Info, because nodes are
// normally deployed with Log.Level "error", which would hide exactly the
// lines an operator enables StatsInterval to see. StatsInterval 0 turns the
// periodic part off.
var rtLog = log.StandardLogger()

// initRuntimeLogger must run after the main logger's level and output are
// configured.
func initRuntimeLogger() {
	std := log.StandardLogger()
	if std.IsLevelEnabled(log.InfoLevel) {
		rtLog = std
		return
	}
	l := log.New()
	l.SetOutput(std.Out)
	l.SetFormatter(std.Formatter)
	l.SetLevel(log.InfoLevel)
	rtLog = l
}

// applyRuntimeConfig installs the GC percent / soft memory limit from the
// top-level "Runtime" section and logs what took effect. It is called once at
// startup and again after every config hot-reload, so operators can tune the
// collector without restarting the node.
func applyRuntimeConfig(rc *conf.RuntimeConfig) conf.ResolvedRuntime {
	resolved := rc.Resolve()
	for _, w := range resolved.Warnings {
		rtLog.WithField("warning", w).Warn("Runtime config")
	}
	res := runtimetune.Apply(runtimetune.Options{
		GCPercent:          resolved.GCPercent,
		MemoryLimitPercent: resolved.MemoryLimitPercent,
		MemoryLimit:        resolved.MemoryLimit,
	})
	for _, w := range res.Warnings {
		rtLog.WithField("warning", w).Warn("Runtime tuning")
	}
	gc := "default"
	if res.GCPercent > 0 {
		gc = fmt.Sprintf("%d", res.GCPercent)
	}
	limit := "none"
	if res.MemoryLimit > 0 {
		limit = runtimetune.FormatSize(res.MemoryLimit)
	}
	rtLog.WithFields(log.Fields{
		"GOGC":         gc + " (" + res.GCSource + ")",
		"memory_limit": limit + " (" + res.MemorySource + ")",
	}).Info("Go runtime tuned")
	return resolved
}

var statsOnce sync.Once

// startRuntimeStats logs a periodic one-line resource summary and warns when
// the node is memory- or CPU-bound. The thresholds are deliberately simple:
//
//   - memory-bound: the GC CPU limiter engaged (the soft memory limit is
//     binding), or the collector burned >15% of CPU over the interval, or
//     runtime memory is above 90% of the limit;
//   - CPU-bound: 1-minute load average at or above 90% of the core count.
//
// Either warning means this machine is at capacity for its current traffic
// and users should be spread to another node.
func startRuntimeStats(intervalSec int) {
	if intervalSec <= 0 {
		return
	}
	statsOnce.Do(func() {
		go runtimeStatsLoop(time.Duration(intervalSec) * time.Second)
	})
}

func runtimeStatsLoop(interval time.Duration) {
	var sampler runtimetune.Sampler
	sampler.Sample() // prime the deltas
	t := time.NewTicker(interval)
	defer t.Stop()
	// The first window still contains core start-up and the forced GC in
	// serverHandle, whose cost is unrelated to steady-state capacity, so it
	// is logged but never turned into a warning.
	warn := false
	for range t.C {
		logRuntimeStats(sampler.Sample(), warn)
		warn = true
	}
}

const (
	statsGCCPUWarn  = 0.15
	statsMemUseWarn = 0.90
	statsLoadWarn   = 0.90
)

func logRuntimeStats(st runtimetune.Stats, warn bool) {
	limit := "none"
	memPct := 0.0
	if st.MemoryLimit > 0 {
		limit = runtimetune.FormatSize(st.MemoryLimit)
		memPct = float64(st.Total) / float64(st.MemoryLimit)
	}
	fields := log.Fields{
		"mem_used":   runtimetune.FormatSize(st.Total),
		"heap_live":  runtimetune.FormatSize(st.HeapLive),
		"mem_limit":  limit,
		"goroutines": st.Goroutines,
		"gc_cpu":     fmt.Sprintf("%.1f%%", st.GCCPUShare*100),
		"load1":      fmt.Sprintf("%.2f/%d", st.Load1, st.NumCPU),
	}
	rtLog.WithFields(fields).Info("Runtime stats")
	if !warn {
		return
	}

	memBound := st.MemLimited || st.GCCPUShare >= statsGCCPUWarn || (st.MemoryLimit > 0 && memPct >= statsMemUseWarn)
	if memBound {
		rtLog.WithFields(fields).Warn("内存已接近这台机器的承载上限（GC 被内存软上限压着跑，回收开销上升）：" +
			"先确认 connIdle/bufferSize 是否已按 README 收紧；若仍持续出现，这台机器的连接数已到顶，建议新增节点分流")
	}
	if st.Load1 > 0 && st.NumCPU > 0 && st.Load1 >= float64(st.NumCPU)*statsLoadWarn {
		rtLog.WithFields(fields).Warn("CPU 负载已接近核数：带宽/连接数已接近这台机器的 CPU 上限，建议新增节点分流")
	}
}

var pprofOnce sync.Once

// startPprof serves net/http/pprof on loopback when Runtime.PprofPort is set.
// It uses a private mux so nothing is registered on http.DefaultServeMux.
func startPprof(port int) {
	if port <= 0 {
		return
	}
	pprofOnce.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
		srv := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			rtLog.WithField("addr", addr).Info("pprof listening (loopback only)")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				rtLog.WithField("err", err).Warn("pprof server stopped")
			}
		}()
	})
}
