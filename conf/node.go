package conf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"encoding/json"

	"github.com/InazumaV/V2bX/common/json5"
)

// W4.1 / audit #9 #18 #51: harden the Include URL fetcher against SSRF.
// The previous DialContext only filtered literal-IP hosts — a DNS name
// resolving to 169.254.169.254 (AWS IMDS), 127.0.0.0/8, or any RFC1918
// range got dialed normally. Now we resolve the hostname ourselves,
// reject the request if ANY resolved address is unsafe, and pin the
// outbound dial to the validated IP so DNS rebinding cannot trick a
// second resolution into a different (safe-looking) host.

const (
	includeBodyMaxBytes      = 8 << 20 // 8 MiB — generous for any sane config include
	includeRequestTimeout    = 30 * time.Second
	includeDialTimeout       = 10 * time.Second
	includeHandshakeTimeout  = 10 * time.Second
	includeResponseHdrWindow = 15 * time.Second
)

// isUnsafeIP rejects any address that points at loopback, private RFC1918,
// link-local, multicast, or the unspecified ranges. v4-mapped v6 addresses
// are checked in both their v6 and underlying v4 form.
// extraUnsafeCIDRs covers ranges the stdlib predicates miss but that are
// still dangerous SSRF targets. W6 review #13:
//   - 0.0.0.0/8     "this host" — on Linux routes to localhost.
//   - 100.64.0.0/10 CGNAT (RFC 6598) — carrier-internal, reachable hosts.
//   - 192.0.0.0/24  IETF protocol assignments.
//   - 198.18.0.0/15 benchmarking (RFC 2544).
//   - 240.0.0.0/4   reserved / "future use".
var extraUnsafeCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

func isUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		for _, n := range extraUnsafeCIDRs {
			if n.Contains(v4) {
				return true
			}
		}
		if !v4.Equal(ip) {
			return isUnsafeIP(v4)
		}
	}
	return false
}

// safeIncludeTransport rejects Include URLs that resolve to any
// loopback/private/link-local IP, pins the dial to the verified IP, and
// has its own timeouts so a slow-loris peer cannot stall the load for the
// full client Timeout.
var safeIncludeTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address: %s", err)
		}
		resolver := net.DefaultResolver
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve include host %q: %w", host, err)
		}
		// Reject if ANY resolved address is in a forbidden range — this
		// defeats DNS rebinding where the attacker returns both a safe
		// address and the target. We must also pick a specific IP to
		// pin against, so a second resolution can't sneak through.
		var pick net.IP
		for _, ia := range ips {
			if isUnsafeIP(ia.IP) {
				return nil, fmt.Errorf("include URL host %q resolves to unsafe address %s", host, ia.IP)
			}
			if pick == nil {
				pick = ia.IP
			}
		}
		if pick == nil {
			return nil, fmt.Errorf("include URL host %q resolved to no addresses", host)
		}
		dialer := &net.Dialer{Timeout: includeDialTimeout}
		// Pin the dial to the validated IP, with the original port.
		return dialer.DialContext(ctx, network, net.JoinHostPort(pick.String(), port))
	},
	TLSHandshakeTimeout:   includeHandshakeTimeout,
	ResponseHeaderTimeout: includeResponseHdrWindow,
	ExpectContinueTimeout: time.Second,
	// Don't keep these connections alive — each include load is one-shot
	// and we don't want IP-pinned conns sitting in a pool against the
	// possibility of host re-resolution between calls.
	DisableKeepAlives: true,
}

// newSafeIncludeClient constructs the HTTP client used for Include URL
// loading. CheckRedirect refuses cross-host redirects so a panel-controlled
// 302 cannot bounce us to an internal IP via a safe-looking external URL.
func newSafeIncludeClient() *http.Client {
	return &http.Client{
		Timeout:   includeRequestTimeout,
		Transport: safeIncludeTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many include redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("include redirect to different host blocked: %s", req.URL.Host)
			}
			return nil
		},
	}
}

type NodeConfig struct {
	ApiConfig ApiConfig `json:"-"`
	Options   Options   `json:"-"`
}

type rawNodeConfig struct {
	Include string          `json:"Include"`
	ApiRaw  json.RawMessage `json:"ApiConfig"`
	OptRaw  json.RawMessage `json:"Options"`
}

type ApiConfig struct {
	APIHost      string `json:"ApiHost"`
	APISendIP    string `json:"ApiSendIP"`
	NodeID       int    `json:"NodeID"`
	Key          string `json:"ApiKey"`
	NodeType     string `json:"NodeType"`
	Timeout      int    `json:"Timeout"`
	RuleListPath string `json:"RuleListPath"`
	ApiVersion   int    `json:"ApiVersion"` // 1 = V1 UniProxy (default), 2 = V2 flat API (for Shannon-x/v2board ServerV2node)
}

func (n *NodeConfig) UnmarshalJSON(data []byte) (err error) {
	rn := rawNodeConfig{}
	err = json.Unmarshal(data, &rn)
	if err != nil {
		return err
	}
	if len(rn.Include) != 0 {
		u, urlErr := url.Parse(rn.Include)
		if urlErr == nil && (u.Scheme == "http" || u.Scheme == "https") {
			httpClient := newSafeIncludeClient()
			rsp, err := httpClient.Get(rn.Include)
			if err != nil {
				return fmt.Errorf("fetch include URL error: %s", err)
			}
			defer rsp.Body.Close()
			// W4.1 / W4.2 / W6 / audit #9 #18 #50: cap the response body so
			// a gigabyte-or-larger payload (malicious or accidental) cannot
			// OOM the node. Belt and braces — MaxBytesReader on the body
			// AND NewTrimNodeReaderLimit on the json5 parser.
			limited := http.MaxBytesReader(nil, rsp.Body, includeBodyMaxBytes)
			data, err = io.ReadAll(json5.NewTrimNodeReaderLimit(limited, includeBodyMaxBytes))
			if err != nil {
				return fmt.Errorf("read include URL error: %s", err)
			}
		} else {
			f, err := os.Open(rn.Include)
			if err != nil {
				return fmt.Errorf("open include file error: %s", err)
			}
			defer f.Close()
			// W6 / audit #50 后半: same 8 MiB ceiling for local Include files —
			// a misconfigured path pointing at a huge file shouldn't OOM us.
			data, err = io.ReadAll(json5.NewTrimNodeReaderLimit(f, includeBodyMaxBytes))
			if err != nil {
				return fmt.Errorf("read include file error: %s", err)
			}
		}
		err = json.Unmarshal(data, &rn)
		if err != nil {
			return fmt.Errorf("unmarshal include file error: %s", err)
		}
	}

	n.ApiConfig = ApiConfig{
		APIHost: "http://127.0.0.1",
		Timeout: 30,
	}
	if len(rn.ApiRaw) > 0 {
		err = json.Unmarshal(rn.ApiRaw, &n.ApiConfig)
		if err != nil {
			return
		}
	} else {
		err = json.Unmarshal(data, &n.ApiConfig)
		if err != nil {
			return
		}
	}

	n.Options = Options{
		ListenIP:   "0.0.0.0",
		SendIP:     "0.0.0.0",
		CertConfig: NewCertConfig(),
	}
	if len(rn.OptRaw) > 0 {
		err = json.Unmarshal(rn.OptRaw, &n.Options)
		if err != nil {
			return
		}
	} else {
		err = json.Unmarshal(data, &n.Options)
		if err != nil {
			return
		}
	}
	return
}

type Options struct {
	Name                   string                `json:"Name"`
	Core                   string                `json:"Core"`
	CoreName               string                `json:"CoreName"`
	ListenIP               string                `json:"ListenIP"`
	SendIP                 string                `json:"SendIP"`
	DeviceOnlineMinTraffic int64                 `json:"DeviceOnlineMinTraffic"`
	ReportMinTraffic       int64                 `json:"ReportMinTraffic"`
	LimitConfig            LimitConfig           `json:"LimitConfig"`
	RawOptions             json.RawMessage       `json:"RawOptions"`
	XrayOptions            *XrayOptions          `json:"XrayOptions"`
	SingOptions            *SingOptions          `json:"SingOptions"`
	Hysteria2ConfigPath    string                `json:"Hysteria2ConfigPath"`
	CertConfig             *CertConfig           `json:"CertConfig"`
	// W6 / audit #8: opt-in widening of the panel-pushed custom-outbound
	// trust boundary. Nil / unset → safe default (whitelist freedom +
	// blackhole). See CustomOutboundConfig docs for the rationale and the
	// LegacyPermissiveWildcard knob.
	CustomOutbound *CustomOutboundConfig `json:"CustomOutbound,omitempty"`
}

func (o *Options) UnmarshalJSON(data []byte) error {
	type opt Options
	err := json.Unmarshal(data, (*opt)(o))
	if err != nil {
		return err
	}
	switch o.Core {
	case "xray":
		o.XrayOptions = NewXrayOptions()
		return json.Unmarshal(data, o.XrayOptions)
	case "sing":
		o.SingOptions = NewSingOptions()
		return json.Unmarshal(data, o.SingOptions)
	case "hysteria2":
		o.RawOptions = data
		return nil
	default:
		o.Core = ""
		o.RawOptions = data
	}
	return nil
}
