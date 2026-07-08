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

type XrayConnectionConfig struct {
	Handshake    uint32 `json:"handshake"`
	ConnIdle     uint32 `json:"connIdle"`
	UplinkOnly   uint32 `json:"uplinkOnly"`
	DownlinkOnly uint32 `json:"downlinkOnly"`
	BufferSize   int32  `json:"bufferSize"`
}

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
			Handshake:    4,
			ConnIdle:     120,
			UplinkOnly:   2,
			DownlinkOnly: 4,
			BufferSize:   128,
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
	// reporting see the real client instead of the CDN edge. For Cloudflare
	// use ["CF-Connecting-IP"].
	//
	// SECURITY: the first X-Forwarded-For entry is client-forgeable — behind
	// Cloudflare it is only safe if you BOTH (a) firewall the origin to
	// Cloudflare's IP ranges AND (b) add a Cloudflare Transform Rule setting
	// X-Forwarded-For to cf.connecting_ip (CF appends the real IP rather than
	// overwriting, so without the rule a client can prepend a forged entry).
	// Leave empty to disable (default); see README for the full setup.
	TrustedXForwardedFor []string `json:"TrustedXForwardedFor"`
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
