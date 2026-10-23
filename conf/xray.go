package conf

type XrayConfig struct {
	LogConfig          *XrayLogConfig        `json:"Log"`
	AssetPath          string                `json:"AssetPath"`
	DnsConfigPath      string                `json:"DnsConfigPath"`
	RouteConfigPath    string                `json:"RouteConfigPath"`
	ConnectionConfig   *XrayConnectionConfig `json:"XrayConnectionConfig"`
	InboundConfigPath  string                `json:"InboundConfigPath"`
	OutboundConfigPath string                `json:"OutboundConfigPath"`
}

type XrayLogConfig struct {
	Level string `json:"Level"`
	// AccessPath: "" writes connection logs to the default rotated file
	// (/var/log/V2bX/access.log), "console" restores the legacy
	// stdout/journald behavior, "none" disables them entirely.
	AccessPath string `json:"AccessPath"`
	ErrorPath  string `json:"ErrorPath"`
	// Rotation applies to every file-based xray log (access and error).
	MaxSize    int  `json:"MaxSize"`    // MB per file before rotation
	MaxBackups int  `json:"MaxBackups"` // rotated files to keep, 0 = unlimited
	MaxDays    int  `json:"MaxDays"`    // days to retain rotated files
	Compress   bool `json:"Compress"`   // gzip rotated files
}

// XrayConnectionConfig maps to xray's level-0 connection policy. These four
// timeouts and the buffer cap are the single biggest lever on a node's memory
// footprint, because every value is paid per connection:
//
//   - handshake: seconds a connection may sit without completing the proxy
//     handshake. Half-open / probe connections hold a TLS state + goroutine
//     stacks for this long.
//   - connIdle: seconds without traffic in either direction before the
//     connection is torn down. Clients keep idle keep-alive connections open
//     for as long as the server allows, so this value directly controls how
//     many concurrent connections (and therefore how much memory) an idle
//     user base costs.
//   - uplinkOnly / downlinkOnly: grace period after one side closes.
//   - bufferSize: KB that may queue in each direction's internal pipe before
//     the reader is back-pressured. Throughput is governed by the kernel
//     socket buffers, not this pipe, so it only needs to cover scheduling
//     latency between goroutines; large values just let every slow client
//     pin that much extra memory. 0 queues at most one read, -1 is unlimited.
type XrayConnectionConfig struct {
	Handshake    uint32 `json:"handshake"`
	ConnIdle     uint32 `json:"connIdle"`
	UplinkOnly   uint32 `json:"uplinkOnly"`
	DownlinkOnly uint32 `json:"downlinkOnly"`
	BufferSize   int32  `json:"bufferSize"`
}

// Defaults for XrayConnectionConfig. handshake / connIdle / uplinkOnly /
// downlinkOnly match v2node; bufferSize is halved from v2node's 128 KB
// because measurements with slow readers show the pipe cap is paid in full
// by every stalled connection while adding nothing to throughput (xray's own
// default on arm64 is 4 KB).
const (
	DefaultXrayHandshake    = 4
	DefaultXrayConnIdle     = 120
	DefaultXrayUplinkOnly   = 2
	DefaultXrayDownlinkOnly = 4
	DefaultXrayBufferSize   = 64
)

func NewXrayConfig() *XrayConfig {
	return &XrayConfig{
		LogConfig: &XrayLogConfig{
			Level:      "warning",
			AccessPath: "",
			ErrorPath:  "",
			MaxSize:    100,
			MaxBackups: 0,
			MaxDays:    90,
			Compress:   true,
		},
		AssetPath:          "/etc/V2bX/",
		DnsConfigPath:      "",
		InboundConfigPath:  "",
		OutboundConfigPath: "",
		RouteConfigPath:    "",
		ConnectionConfig: &XrayConnectionConfig{
			Handshake:    DefaultXrayHandshake,
			ConnIdle:     DefaultXrayConnIdle,
			UplinkOnly:   DefaultXrayUplinkOnly,
			DownlinkOnly: DefaultXrayDownlinkOnly,
			BufferSize:   DefaultXrayBufferSize,
		},
	}
}

type XrayOptions struct {
	EnableProxyProtocol bool                    `json:"EnableProxyProtocol"`
	EnableDNS           bool                    `json:"EnableDNS"`
	DNSType             string                  `json:"DNSType"`
	EnableUot           bool                    `json:"EnableUot"`
	EnableTFO           bool                    `json:"EnableTFO"`
	DisableIVCheck      bool                    `json:"DisableIVCheck"`
	DisableSniffing     bool                    `json:"DisableSniffing"`
	EnableFallback      bool                    `json:"EnableFallback"`
	FallBackConfigs     []FallBackConfigForXray `json:"FallBackConfigs"`
	// TrustedXForwardedFor lists header names whose presence marks a request
	// as coming through a trusted reverse proxy / CDN. For such requests the
	// ws/httpupgrade/xhttp/grpc listener replaces the connection source with
	// the FIRST X-Forwarded-For entry, so device limiting and panel online-IP
	// reporting see the real client instead of the CDN edge.
	//
	// Leave empty (default): V2bX auto-uses ["CF-Connecting-IP"] for HTTP-based
	// transports (ws/httpupgrade/xhttp/grpc), so real client IPs work out of
	// the box behind Cloudflare with no config. Set it to override the header
	// list for a different CDN, or set DisableCDNRealIP to turn the default off.
	TrustedXForwardedFor []string `json:"TrustedXForwardedFor"`
	// DisableCDNRealIP turns off the automatic ["CF-Connecting-IP"] default
	// above. Normal proxy clients never send X-Forwarded-For, so the auto
	// default reports the true client IP; a deliberately-malicious subscriber
	// could still forge it (CF appends rather than overwrites XFF). To harden,
	// firewall the origin to Cloudflare IP ranges and add a CF Transform Rule
	// setting X-Forwarded-For to cf.connecting_ip. See README.
	DisableCDNRealIP bool `json:"DisableCDNRealIP"`
	// Hysteria2QuicParams tunes the QUIC layer of xray-core's hysteria2
	// inbound (receive windows, idle timeout, stream cap). Every field is
	// optional and 0 keeps xray's default; see XrayHysteria2QuicParams.
	Hysteria2QuicParams *XrayHysteria2QuicParams `json:"Hysteria2QuicParams"`
}

// XrayHysteria2QuicParams exposes the quic-go knobs xray-core accepts for a
// hysteria2 inbound. xray's defaults (8 MiB per-stream and 20 MiB
// per-connection receive windows, 1024 streams per connection, 30 s idle)
// are tuned for throughput on a single big pipe; a node serving many
// clients on a small VPS can lower the windows to bound how much unread
// upload data each stream may buffer. Sizes are in bytes and must be at
// least 16384; timeouts are in seconds (maxIdleTimeout 4–120,
// keepAlivePeriod 2–60); maxIncomingStreams must be at least 8.
type XrayHysteria2QuicParams struct {
	InitStreamReceiveWindow     uint64 `json:"initStreamReceiveWindow"`
	MaxStreamReceiveWindow      uint64 `json:"maxStreamReceiveWindow"`
	InitConnectionReceiveWindow uint64 `json:"initConnectionReceiveWindow"`
	MaxConnectionReceiveWindow  uint64 `json:"maxConnectionReceiveWindow"`
	MaxIdleTimeout              int64  `json:"maxIdleTimeout"`
	KeepAlivePeriod             int64  `json:"keepAlivePeriod"`
	MaxIncomingStreams          int64  `json:"maxIncomingStreams"`
	DisablePathMTUDiscovery     bool   `json:"disablePathMTUDiscovery"`
}

// IsZero reports whether no QUIC parameter was configured.
func (q *XrayHysteria2QuicParams) IsZero() bool {
	return q == nil || *q == XrayHysteria2QuicParams{}
}

type FallBackConfigForXray struct {
	SNI              string `json:"SNI"`
	Alpn             string `json:"Alpn"`
	Path             string `json:"Path"`
	Dest             string `json:"Dest"`
	ProxyProtocolVer uint64 `json:"ProxyProtocolVer"`
}

func NewXrayOptions() *XrayOptions {
	return &XrayOptions{
		EnableProxyProtocol: false,
		EnableDNS:           false,
		DNSType:             "AsIs",
		EnableUot:           false,
		EnableTFO:           false,
		DisableIVCheck:      false,
		DisableSniffing:     false,
		EnableFallback:      false,
	}
}
