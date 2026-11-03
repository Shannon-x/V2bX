package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

// Diagnostics are opt-in and bound only to an explicit loopback IP.
// Use a private mux so unrelated handlers never become exposed here.
func startDiagnostics(address string) (*http.Server, error) {
	if address == "" {
		return nil, nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("pprof-listen must use an explicit loopback IP")
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("GET /debug/runtime", func(w http.ResponseWriter, _ *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"goroutines": runtime.NumGoroutine(), "heap_alloc_bytes": m.HeapAlloc,
			"heap_inuse_bytes": m.HeapInuse, "heap_objects": m.HeapObjects,
			"total_alloc_bytes": m.TotalAlloc, "stack_inuse_bytes": m.StackInuse,
			"gc_cycles": m.NumGC, "gc_cpu_fraction": m.GCCPUFraction,
		})
	})
	s := &http.Server{Addr: l.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Close()
		}
	}()
	return s, nil
}
