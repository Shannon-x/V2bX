package conf

// RuntimeConfig tunes the Go runtime for the whole V2bX process. It lives at
// the top level of config.json:
//
//	"Runtime": {
//	    "GCPercent": 50,
//	    "MemoryLimitPercent": 70,
//	    "MemoryLimit": "",
//	    "PprofPort": 0
//	}
//
// Every field is optional. Environment variables GOGC / GOMEMLIMIT, when
// set, override the corresponding field.
type RuntimeConfig struct {
	// GCPercent is the GOGC value. Go's default is 100, which lets the heap
	// grow to ~2× the live data before a collection; a proxy node's live data
	// is dominated by per-connection state, so with tens of thousands of
	// connections that headroom alone is hundreds of MB. V2bX defaults to 50
	// (heap ceiling ≈ 1.5× live data) at the cost of slightly more frequent,
	// cheap GC cycles. 100 restores Go's default; values below 10 are
	// rejected.
	GCPercent *int `json:"GCPercent"`
	// MemoryLimitPercent sets Go's soft memory limit (GOMEMLIMIT) to this
	// percentage of the memory available to the process (physical RAM or the
	// cgroup limit, whichever is smaller). Default 70. 0 disables it. When
	// the heap approaches the limit the collector works harder instead of
	// letting the process be OOM-killed; it is a safety net, not a hard cap.
	MemoryLimitPercent *int `json:"MemoryLimitPercent"`
	// MemoryLimit is an absolute soft limit ("1536MiB", "2GiB") that takes
	// precedence over MemoryLimitPercent. Required on non-Linux platforms
	// where total memory cannot be detected.
	MemoryLimit string `json:"MemoryLimit"`
	// StatsInterval is how often (seconds) V2bX logs a one-line runtime
	// summary — memory in use vs. the soft limit, live heap, goroutine
	// count (≈ 5 × connections on xray), GC CPU share and 1-minute load —
	// and warns when the node is memory-bound (GC pinned against the limit)
	// or CPU-bound (load ≥ core count). That warning is the signal that this
	// machine is saturated and traffic should be spread to another node.
	// Default 300; 0 disables; minimum 10.
	StatsInterval *int `json:"StatsInterval"`
	// PprofPort, when non-zero, serves Go's net/http/pprof on
	// 127.0.0.1:<port> so heap / goroutine / CPU profiles can be captured
	// from a running node (`go tool pprof http://127.0.0.1:<port>/debug/pprof/heap`).
	// Loopback only; never exposed to the network. Changing it requires a
	// restart.
	PprofPort int `json:"PprofPort"`
}

const (
	// DefaultGCPercent is applied when Runtime.GCPercent is omitted.
	DefaultGCPercent = 50
	// DefaultMemoryLimitPercent is applied when Runtime.MemoryLimitPercent
	// is omitted.
	DefaultMemoryLimitPercent = 70
	// DefaultStatsInterval (seconds) is applied when Runtime.StatsInterval
	// is omitted.
	DefaultStatsInterval = 300
	// minStatsInterval keeps the summary from flooding the log.
	minStatsInterval = 10
	// minGCPercent guards against values that would make the collector run
	// almost continuously.
	minGCPercent = 10
)

// ResolvedRuntime holds the effective knobs after defaults are applied.
type ResolvedRuntime struct {
	GCPercent          int
	MemoryLimitPercent int
	MemoryLimit        string
	StatsInterval      int
	PprofPort          int
	// Warnings lists config values that were rejected and replaced.
	Warnings []string
}

// Resolve applies V2bX defaults to the optional fields. A nil receiver is
// treated as an empty section.
func (r *RuntimeConfig) Resolve() ResolvedRuntime {
	out := ResolvedRuntime{
		GCPercent:          DefaultGCPercent,
		MemoryLimitPercent: DefaultMemoryLimitPercent,
		StatsInterval:      DefaultStatsInterval,
	}
	if r == nil {
		return out
	}
	if r.GCPercent != nil {
		switch v := *r.GCPercent; {
		case v == 0:
			// 0 means "leave Go's default alone" (GOGC=100).
			out.GCPercent = 0
		case v < minGCPercent:
			out.Warnings = append(out.Warnings, "Runtime.GCPercent below 10 is rejected; using default")
		default:
			out.GCPercent = v
		}
	}
	if r.MemoryLimitPercent != nil {
		if v := *r.MemoryLimitPercent; v < 0 {
			out.Warnings = append(out.Warnings, "Runtime.MemoryLimitPercent must be >= 0; using default")
		} else {
			out.MemoryLimitPercent = v
		}
	}
	out.MemoryLimit = r.MemoryLimit
	if r.StatsInterval != nil {
		switch v := *r.StatsInterval; {
		case v == 0:
			out.StatsInterval = 0
		case v < minStatsInterval:
			out.Warnings = append(out.Warnings, "Runtime.StatsInterval below 10s is rejected; using default")
		default:
			out.StatsInterval = v
		}
	}
	if r.PprofPort < 0 || r.PprofPort > 65535 {
		out.Warnings = append(out.Warnings, "Runtime.PprofPort out of range; pprof disabled")
	} else {
		out.PprofPort = r.PprofPort
	}
	return out
}
