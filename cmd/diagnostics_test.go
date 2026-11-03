package cmd

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDiagnosticsLoopbackAndRuntimeMetrics(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", ":0", "example.com:0", "[::]:0"} {
		if s, err := startDiagnostics(address); err == nil || s != nil {
			t.Fatalf("accepted public bind %s", address)
		}
	}
	s, err := startDiagnostics("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := http.Get("http://" + s.Addr + "/debug/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["goroutines"].(float64) <= 0 || m["heap_alloc_bytes"].(float64) <= 0 {
		t.Fatalf("bad metrics %v", m)
	}
}
