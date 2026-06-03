package conf

// CustomOutboundConfig governs whether and how panel-supplied raw outbound
// JSON (info.Rules.RawOutbound / info.Rules.RawDefaultOut) is loaded into
// the running core.
//
// Design intent: panel-driven custom outbounds are a CORE FEATURE — most
// V2bX deployments rely on the panel pushing socks/http/vmess outbounds
// for routing logic. Therefore the default is **permissive**: any
// panel-pushed outbound is accepted (matches pre-W6 behavior). The
// CustomOutboundConfig fields exist as an OPTIONAL HARDENING knob for
// deployers who want a tighter trust boundary (e.g. multi-tenant nodes
// where the panel and node operator aren't the same party).
//
// Behavior matrix:
//
//	| Config                                       | Effect                          |
//	| -------------------------------------------- | ------------------------------- |
//	| (omitted / nil)                              | accept ALL (default, pre-W6)    |
//	| Enabled=false                                | reject ALL                      |
//	| Enabled=true (or nil) + AllowedProtocols=[]  | accept ALL                      |
//	| Enabled=true + AllowedProtocols=["freedom"]  | accept ONLY listed protocols    |
//	| Enabled=true + AllowedProtocols=["*"]        | accept ALL (explicit form)      |
//
// Audit #8 — see AUDIT_REPORT §3.3 for the trust-boundary declaration
// reminding deployers that an unrestricted panel must be treated as
// node-root-equivalent.
type CustomOutboundConfig struct {
	Enabled          *bool    `json:"Enabled,omitempty"`
	AllowedProtocols []string `json:"AllowedProtocols,omitempty"`
}

// PermissiveWildcard, when present in AllowedProtocols, accepts ANY
// protocol the panel sends. This is also the default when AllowedProtocols
// is empty / unset.
const PermissiveWildcard = "*"

// IsCustomOutboundEnabled returns whether custom outbound loading should
// proceed at all. nil config or missing Enabled field → enabled (default).
func IsCustomOutboundEnabled(c *CustomOutboundConfig) bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// IsCustomOutboundAllowed reports whether a specific outbound protocol may
// be loaded. Default (nil config / empty AllowedProtocols / wildcard
// entry) is PERMISSIVE — every protocol is accepted, so panel-driven
// routing keeps working without any node-side configuration change.
//
// To restrict, set AllowedProtocols to a concrete list e.g.
// ["freedom","blackhole","socks"].
func IsCustomOutboundAllowed(c *CustomOutboundConfig, proto string) bool {
	// nil config or empty list ⇒ permissive (pre-W6 / default behavior).
	if c == nil || len(c.AllowedProtocols) == 0 {
		return true
	}
	for _, p := range c.AllowedProtocols {
		if p == PermissiveWildcard || p == proto {
			return true
		}
	}
	return false
}
