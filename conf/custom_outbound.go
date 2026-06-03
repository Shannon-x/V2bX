package conf

// CustomOutboundConfig governs whether and how panel-supplied raw outbound
// JSON (info.Rules.RawOutbound / info.Rules.RawDefaultOut) is loaded into
// the running core.
//
// W6 / audit #8: previously V2bX accepted any panel-pushed outbound config
// with zero filtering — a compromised panel could MITM every proxied flow
// by routing it through an attacker-controlled SOCKS5 / HTTP / VMess
// upstream. From W6 the default is "whitelist freedom/blackhole only";
// deployers who actually need richer custom outbounds (most don't) must
// explicitly widen AllowedProtocols.
//
// The `Enabled` pointer-bool exists so that:
//   - missing (nil) → default safe behavior (Enabled=true, whitelist =
//     [freedom, blackhole])
//   - explicit false → reject ALL custom outbounds, including freedom
//   - explicit true → use AllowedProtocols (which falls back to the safe
//     whitelist if empty)
type CustomOutboundConfig struct {
	Enabled          *bool    `json:"Enabled,omitempty"`
	AllowedProtocols []string `json:"AllowedProtocols,omitempty"`
}

// DefaultAllowedOutbounds is the safe default protocol list — these don't
// route traffic to operator-controlled remotes, so they can't be used to
// MITM. Override via Options.CustomOutbound.AllowedProtocols if you trust
// the panel and actually need richer outbounds.
var DefaultAllowedOutbounds = []string{"freedom", "blackhole"}

// LegacyPermissiveWildcard, when present in AllowedProtocols, restores the
// pre-W6 behavior of accepting ANY protocol the panel sends. Documented
// here so deployers grepping for the old behavior can find this comment.
const LegacyPermissiveWildcard = "*"

// IsCustomOutboundEnabled returns whether custom outbound loading should
// proceed at all. nil config → enabled by default (safe whitelist still
// applies via IsCustomOutboundAllowed).
func IsCustomOutboundEnabled(c *CustomOutboundConfig) bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// IsCustomOutboundAllowed reports whether a specific outbound protocol may
// be loaded. The safe whitelist applies when the field is unset/empty;
// callers that intentionally want everything must include "*" explicitly.
func IsCustomOutboundAllowed(c *CustomOutboundConfig, proto string) bool {
	list := DefaultAllowedOutbounds
	if c != nil && len(c.AllowedProtocols) > 0 {
		list = c.AllowedProtocols
	}
	for _, p := range list {
		if p == LegacyPermissiveWildcard || p == proto {
			return true
		}
	}
	return false
}
