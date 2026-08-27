package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"encoding/json"
)

// Security type
const (
	None    = 0
	Tls     = 1
	Reality = 2
)

type NodeInfo struct {
	Id           int
	Type         string
	Security     int
	PushInterval time.Duration
	PullInterval time.Duration
	RawDNS       RawDNS
	Rules        Rules

	// Panel-provided thresholds (from base_config, 0 means use local config)
	DeviceOnlineMinTraffic int
	NodeReportMinTraffic   int

	// CertInfo 为面板下发的证书配置；面板未下发时为 nil。
	CertInfo *CertInfo

	// origin
	VAllss      *VAllssNode
	Shadowsocks *ShadowsocksNode
	Trojan      *TrojanNode
	Tuic        *TuicNode
	AnyTls      *AnyTlsNode
	Hysteria    *HysteriaNode
	Hysteria2   *Hysteria2Node
	Common      *CommonNode
}

type CommonNode struct {
	Host       string      `json:"host"`
	ServerPort int         `json:"server_port"`
	ServerName string      `json:"server_name"`
	Routes     []Route     `json:"routes"`
	BaseConfig *BaseConfig `json:"base_config"`
}

type Route struct {
	Id          int         `json:"id"`
	Match       interface{} `json:"match"`
	Action      string      `json:"action"`
	ActionValue string      `json:"action_value"`
}
type BaseConfig struct {
	PushInterval           any `json:"push_interval"`
	PullInterval           any `json:"pull_interval"`
	DeviceOnlineMinTraffic int `json:"device_online_min_traffic"`
	NodeReportMinTraffic   int `json:"node_report_min_traffic"`
}

// VAllssNode is vmess and vless node info
type VAllssNode struct {
	CommonNode
	Tls                 int             `json:"tls"`
	TlsSettings         TlsSettings     `json:"tls_settings"`
	TlsSettingsBack     *TlsSettings    `json:"tlsSettings"`
	Network             string          `json:"network"`
	NetworkSettings     json.RawMessage `json:"network_settings"`
	NetworkSettingsBack json.RawMessage `json:"networkSettings"`
	Encryption          string          `json:"encryption"`
	EncryptionSettings  EncSettings     `json:"encryption_settings"`
	ServerName          string          `json:"server_name"`

	// vless only
	Flow          string        `json:"flow"`
	RealityConfig RealityConfig `json:"-"`
}

type TlsSettings struct {
	ServerName  string `json:"server_name"`
	Dest        string `json:"dest"`
	ServerPort  string `json:"server_port"`
	ShortId     string `json:"short_id"`
	PrivateKey  string `json:"private_key"`
	Mldsa65Seed string `json:"mldsa65Seed"`
	Xver        uint64 `json:"xver,string"`

	// 证书相关。面板把节点的 cert_config 合并进 tls_settings 下发，
	// 对齐 v2node 的字段命名（api/v2board/node.go 里的 TlsSettings）。
	//
	// 其中 remote 模式是解决「SNI 为伪装域名、真证书无法验证」的关键：
	// 面板自己生成一张长效自签证书，把 cert/key 下发给节点，
	// 同时把证书指纹写进订阅（pcs / pinSHA256），
	// 客户端靠指纹固定来验证，既不用真证书也不用裸奔的 allowInsecure。
	CertMode         string `json:"cert_mode"`
	CertFile         string `json:"cert_file"`
	KeyFile          string `json:"key_file"`
	TlsCert          string `json:"tls_cert"`
	TlsKey           string `json:"tls_key"`
	Provider         string `json:"provider"`
	Email            string `json:"email"`
	DNSEnv           string `json:"dns_env"`
	RejectUnknownSni any    `json:"reject_unknown_sni"`
	// 面板算好的证书指纹，节点侧只用来在日志里做一致性核对
	PinnedPeerCertSha256 string `json:"pinned_peer_cert_sha256"`
}

// CertInfo 是面板下发的证书信息在节点侧的归一化形式。
// 面板没下发时各字段为空，由本地 config.json 的 CertConfig 兜底。
type CertInfo struct {
	CertMode             string
	CertDomain           string
	CertFile             string
	KeyFile              string
	TlsCert              string
	TlsKey               string
	Provider             string
	Email                string
	DNSEnv               map[string]string
	RejectUnknownSni     bool
	PinnedPeerCertSha256 string
}

type EncSettings struct {
	Mode          string `json:"mode"`
	Ticket        string `json:"ticket"`
	ServerPadding string `json:"server_padding"`
	PrivateKey    string `json:"private_key"`
}

type RealityConfig struct {
	Xver         uint64 `json:"Xver"`
	MinClientVer string `json:"MinClientVer"`
	MaxClientVer string `json:"MaxClientVer"`
	MaxTimeDiff  string `json:"MaxTimeDiff"`
}

type ShadowsocksNode struct {
	CommonNode
	Cipher    string `json:"cipher"`
	ServerKey string `json:"server_key"`
}

type TrojanNode struct {
	CommonNode
	Network         string          `json:"network"`
	NetworkSettings json.RawMessage `json:"networkSettings"`
}

type TuicNode struct {
	CommonNode
	CongestionControl string `json:"congestion_control"`
	ZeroRTTHandshake  bool   `json:"zero_rtt_handshake"`
}

type AnyTlsNode struct {
	CommonNode
	PaddingScheme []string `json:"padding_scheme,omitempty"`
}

type HysteriaNode struct {
	CommonNode
	UpMbps   int    `json:"up_mbps"`
	DownMbps int    `json:"down_mbps"`
	Obfs     string `json:"obfs"`
}

type Hysteria2Node struct {
	CommonNode
	Ignore_Client_Bandwidth bool   `json:"ignore_client_bandwidth"`
	UpMbps                  int    `json:"up_mbps"`
	DownMbps                int    `json:"down_mbps"`
	ObfsType                string `json:"obfs"`
	ObfsPassword            string `json:"obfs-password"`
}

type RawDNS struct {
	DNSMap  map[string]map[string]interface{}
	DNSJson []byte
}

type Rules struct {
	Regexp        []string
	Protocol      []string
	InboundIP     []string    // block_ip: IP/CIDR patterns to block
	InboundPort   []string    // block_port: port/port-range to block
	RouteRules    []RouteRule // route/route_ip/direct/proxy rules
	DefaultOut    string      // default_out: custom default outbound tag
	RawDefaultOut string      // default_out: full JSON if default outbound originates from custom JSON
}

// RouteRule represents a dynamic routing rule from the panel
type RouteRule struct {
	Type        string   // "domain" or "ip"
	Match       []string // match patterns
	OutboundTag string   // target outbound tag
	RawOutbound string   // if OutboundTag originates from custom JSON, full JSON is kept here
}

// V2UnifiedNode is the flat config format from V2 API (/api/v2/server/config).
// All protocol fields are in a single struct, with `protocol` distinguishing the type.
type V2UnifiedNode struct {
	CommonNode
	Protocol                string          `json:"protocol"`
	Tls                     int             `json:"tls"`
	TlsSettings             TlsSettings     `json:"tls_settings"`
	Network                 string          `json:"network"`
	NetworkSettings         json.RawMessage `json:"network_settings"`
	Encryption              string          `json:"encryption"`
	EncryptionSettings      EncSettings     `json:"encryption_settings"`
	Flow                    string          `json:"flow"`
	Cipher                  string          `json:"cipher"`
	ServerKey               string          `json:"server_key"`
	CongestionControl       string          `json:"congestion_control"`
	ZeroRTTHandshake        bool            `json:"zero_rtt_handshake"`
	PaddingScheme           []string        `json:"padding_scheme,omitempty"`
	UpMbps                  int             `json:"up_mbps"`
	DownMbps                int             `json:"down_mbps"`
	Obfs                    string          `json:"obfs"`
	ObfsPassword            string          `json:"obfs-password"`
	Ignore_Client_Bandwidth bool            `json:"ignore_client_bandwidth"`
}

func (c *Client) GetNodeInfo() (node *NodeInfo, err error) {
	return c.GetNodeInfoCtx(context.Background())
}

// GetNodeInfoCtx is the ctx-aware variant used by the task framework so a
// watchdog timeout can cancel the HTTP request instead of leaking the
// goroutine + response body. W3.2 / W3.4 / audit #25 #44.
func (c *Client) GetNodeInfoCtx(ctx context.Context) (node *NodeInfo, err error) {
	var path string
	if c.ApiVersion == 2 {
		path = "/api/v2/server/config"
	} else {
		path = "/api/v1/server/UniProxy/config"
	}
	r, err := c.client.
		R().
		SetContext(ctx).
		SetHeader("If-None-Match", c.nodeEtag).
		ForceContentType("application/json").
		Get(path)

	if err != nil {
		return nil, fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)
	}
	if r == nil {
		return nil, fmt.Errorf("received nil response")
	}
	defer func() {
		if r.RawBody() != nil {
			r.RawBody().Close()
		}
	}()

	if r.StatusCode() == 304 {
		return nil, nil
	}
	if err = c.checkResponse(r, path, nil); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(r.Body())
	newBodyHash := hex.EncodeToString(hash[:])
	if c.responseBodyHash == newBodyHash {
		return nil, nil
	}
	// H-09: DO NOT commit responseBodyHash / nodeEtag here. If the body below
	// fails to decode/validate, committing them now would make the next poll
	// treat this same (bad) response as "unchanged" (hash match) or let the
	// panel answer 304 (ETag match) — freezing the stale config indefinitely.
	// They are committed only just before the successful return.
	node = &NodeInfo{
		Id:   c.NodeId,
		Type: c.NodeType,
		RawDNS: RawDNS{
			DNSMap:  make(map[string]map[string]interface{}),
			DNSJson: []byte(""),
		},
	}
	// parse protocol params
	var cm *CommonNode
	if c.ApiVersion == 2 {
		// V2 API: flat unified config with `protocol` field
		rsp := &V2UnifiedNode{}
		err = json.Unmarshal(r.Body(), rsp)
		if err != nil {
			return nil, fmt.Errorf("decode v2 unified config error: %s", err)
		}
		// Override NodeType from panel's protocol field
		proto := strings.ToLower(rsp.Protocol)
		switch proto {
		case "v2ray":
			proto = "vmess"
		case "hysteria2":
			proto = "hysteria2" // keep as-is, panel may use either
		}
		node.Type = proto
		cm = &rsp.CommonNode
		// Map unified fields to protocol-specific structs
		switch proto {
		case "vmess", "vless":
			node.VAllss = &VAllssNode{
				CommonNode:         rsp.CommonNode,
				Tls:                rsp.Tls,
				TlsSettings:        rsp.TlsSettings,
				Network:            rsp.Network,
				NetworkSettings:    rsp.NetworkSettings,
				Encryption:         rsp.Encryption,
				EncryptionSettings: rsp.EncryptionSettings,
				Flow:               rsp.Flow,
			}
			node.Security = rsp.Tls
		case "shadowsocks":
			node.Shadowsocks = &ShadowsocksNode{
				CommonNode: rsp.CommonNode,
				Cipher:     rsp.Cipher,
				ServerKey:  rsp.ServerKey,
			}
			node.Security = None
		case "trojan":
			node.Trojan = &TrojanNode{
				CommonNode:      rsp.CommonNode,
				Network:         rsp.Network,
				NetworkSettings: rsp.NetworkSettings,
			}
			node.Security = Tls
		case "tuic":
			node.Tuic = &TuicNode{
				CommonNode:        rsp.CommonNode,
				CongestionControl: rsp.CongestionControl,
				ZeroRTTHandshake:  rsp.ZeroRTTHandshake,
			}
			node.Security = Tls
		case "anytls":
			node.AnyTls = &AnyTlsNode{
				CommonNode:    rsp.CommonNode,
				PaddingScheme: rsp.PaddingScheme,
			}
			node.Security = Tls
		case "hysteria":
			node.Hysteria = &HysteriaNode{
				CommonNode: rsp.CommonNode,
				UpMbps:     rsp.UpMbps,
				DownMbps:   rsp.DownMbps,
				Obfs:       rsp.Obfs,
			}
			node.Security = Tls
		case "hysteria2":
			node.Hysteria2 = &Hysteria2Node{
				CommonNode:              rsp.CommonNode,
				Ignore_Client_Bandwidth: rsp.Ignore_Client_Bandwidth,
				UpMbps:                  rsp.UpMbps,
				DownMbps:                rsp.DownMbps,
				ObfsType:                rsp.Obfs,
				ObfsPassword:            rsp.ObfsPassword,
			}
			node.Security = Tls
		default:
			return nil, fmt.Errorf("unsupported protocol in V2 config: %s", proto)
		}
	} else {
		// V1 API: protocol-specific config structs
		switch c.NodeType {
		case "vmess", "vless":
			rsp := &VAllssNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode v2ray params error: %s", err)
			}
			if len(rsp.NetworkSettingsBack) > 0 {
				rsp.NetworkSettings = rsp.NetworkSettingsBack
				rsp.NetworkSettingsBack = nil
			}
			if rsp.TlsSettingsBack != nil {
				rsp.TlsSettings = *rsp.TlsSettingsBack
				rsp.TlsSettingsBack = nil
			}
			cm = &rsp.CommonNode
			node.VAllss = rsp
			node.Security = node.VAllss.Tls
		case "shadowsocks":
			rsp := &ShadowsocksNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode shadowsocks params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.Shadowsocks = rsp
			node.Security = None
		case "trojan":
			rsp := &TrojanNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode trojan params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.Trojan = rsp
			node.Security = Tls
		case "tuic":
			rsp := &TuicNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode tuic params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.Tuic = rsp
			node.Security = Tls
		case "anytls":
			rsp := &AnyTlsNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode anytls params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.AnyTls = rsp
			node.Security = Tls
		case "hysteria":
			rsp := &HysteriaNode{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode hysteria params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.Hysteria = rsp
			node.Security = Tls
		case "hysteria2":
			rsp := &Hysteria2Node{}
			err = json.Unmarshal(r.Body(), rsp)
			if err != nil {
				return nil, fmt.Errorf("decode hysteria2 params error: %s", err)
			}
			cm = &rsp.CommonNode
			node.Hysteria2 = rsp
			node.Security = Tls
		}
	}

	// 解析面板下发的证书配置。
	//
	// 这里独立再解一次 tls_settings，而不是复用各协议结构体里的字段，
	// 原因是证书配置与协议无关：hysteria2 / shadowsocks 这些协议的
	// V1 结构体里压根没有 tls_settings 字段，而面板恰恰是把节点的
	// cert_config 合并进 tls_settings 下发的（对齐 v2node 的做法）。
	// 独立解一次可以让所有协议、两种 API 版本都拿到证书配置。
	{
		var certWrap struct {
			TlsSettings TlsSettings `json:"tls_settings"`
		}
		if uErr := json.Unmarshal(r.Body(), &certWrap); uErr == nil {
			node.CertInfo = buildCertInfo(&certWrap.TlsSettings, cm)
		}
	}

	// parse rules and dns
	for i := range cm.Routes {
		// Handle default_out before match parsing, because default_out
		// by design has no match patterns (it applies to ALL traffic).
		if cm.Routes[i].Action == "default_out" {
			outboundTag := cm.Routes[i].ActionValue
			var rawOutbound string

			// If it starts with { it could be a JSON outbound object
			if strings.HasPrefix(strings.TrimSpace(outboundTag), "{") {
				var partial map[string]interface{}
				if err := json.Unmarshal([]byte(outboundTag), &partial); err == nil {
					if tag, ok := partial["tag"].(string); ok && tag != "" {
						rawOutbound = outboundTag
						outboundTag = tag
					}
				}
			}

			node.Rules.DefaultOut = outboundTag
			node.Rules.RawDefaultOut = rawOutbound
			continue
		}

		var matchs []string
		switch v := cm.Routes[i].Match.(type) {
		case string:
			matchs = strings.Split(v, ",")
		case []string:
			matchs = v
		case []interface{}:
			matchs = make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					matchs = append(matchs, s)
				}
			}
		default:
			continue
		}
		if len(matchs) == 0 {
			continue
		}
		switch cm.Routes[i].Action {
		case "block":
			for _, v := range matchs {
				if strings.HasPrefix(v, "protocol:") {
					node.Rules.Protocol = append(node.Rules.Protocol, strings.TrimPrefix(v, "protocol:"))
				} else {
					node.Rules.Regexp = append(node.Rules.Regexp, strings.TrimPrefix(v, "regexp:"))
				}
			}
		case "block_ip":
			node.Rules.InboundIP = append(node.Rules.InboundIP, matchs...)
		case "block_port":
			node.Rules.InboundPort = append(node.Rules.InboundPort, matchs...)
		case "protocol":
			node.Rules.Protocol = append(node.Rules.Protocol, matchs...)
		case "route", "route_ip":
			outboundTag := cm.Routes[i].ActionValue
			var rawOutbound string

			// If it starts with { it could be a JSON outbound object (as used by v2node/v2board)
			if strings.HasPrefix(strings.TrimSpace(outboundTag), "{") {
				var partial map[string]interface{}
				if err := json.Unmarshal([]byte(outboundTag), &partial); err == nil {
					if tag, ok := partial["tag"].(string); ok && tag != "" {
						rawOutbound = outboundTag
						outboundTag = tag
					}
				}
			}

			ruleType := "domain"
			if cm.Routes[i].Action == "route_ip" {
				ruleType = "ip"
			}

			node.Rules.RouteRules = append(node.Rules.RouteRules, RouteRule{
				Type:        ruleType,
				Match:       matchs,
				OutboundTag: outboundTag,
				RawOutbound: rawOutbound,
			})
		case "direct":
			node.Rules.RouteRules = append(node.Rules.RouteRules, RouteRule{
				Type: "domain", Match: matchs, OutboundTag: "direct",
			})
		case "proxy":
			node.Rules.RouteRules = append(node.Rules.RouteRules, RouteRule{
				Type: "domain", Match: matchs, OutboundTag: "proxy",
			})
		case "dns":
			var domains []string
			domains = append(domains, matchs...)
			if matchs[0] != "main" {
				node.RawDNS.DNSMap[strconv.Itoa(i)] = map[string]interface{}{
					"address": cm.Routes[i].ActionValue,
					"domains": domains,
				}
			} else {
				dns := []byte(strings.Join(matchs[1:], ""))
				node.RawDNS.DNSJson = dns
			}
		}
	}

	// set interval
	if cm.BaseConfig != nil {
		node.PushInterval = intervalToTime(cm.BaseConfig.PushInterval)
		node.PullInterval = intervalToTime(cm.BaseConfig.PullInterval)
		node.DeviceOnlineMinTraffic = cm.BaseConfig.DeviceOnlineMinTraffic
		node.NodeReportMinTraffic = cm.BaseConfig.NodeReportMinTraffic
	} else {
		node.PushInterval = 60 * time.Second
		node.PullInterval = 60 * time.Second
	}

	node.Common = cm
	// clear
	cm.Routes = nil
	cm.BaseConfig = nil

	// H-09: commit the cache markers only now that decode + shaping fully
	// succeeded, so a malformed response can never poison the "unchanged" /
	// 304 fast-paths and strand the old config.
	c.responseBodyHash = newBodyHash
	c.nodeEtag = r.Header().Get("ETag")
	return node, nil
}

// W1.11 / audit #46: panel-controlled push/pull intervals must be clamped.
// A panel returning 0 (or anything below ~1s) turns the task loop into a
// busy-loop and self-DoS's the node; absurdly large values just disable
// reporting silently. These bounds protect both directions.
const (
	defaultIntervalSeconds = 60
	minIntervalSeconds     = 10
	maxIntervalSeconds     = 24 * 60 * 60 // 24h
)

func intervalToTime(i interface{}) time.Duration {
	if i == nil {
		return defaultIntervalSeconds * time.Second
	}
	var seconds int
	switch v := i.(type) {
	case int:
		seconds = v
	case float64:
		seconds = int(v)
	case string:
		n, _ := strconv.Atoi(v)
		seconds = n
	default:
		rv := reflect.ValueOf(i)
		if rv.CanInt() {
			seconds = int(rv.Int())
		} else {
			seconds = defaultIntervalSeconds
		}
	}
	if seconds <= 0 {
		seconds = defaultIntervalSeconds
	}
	if seconds < minIntervalSeconds {
		seconds = minIntervalSeconds
	}
	if seconds > maxIntervalSeconds {
		seconds = maxIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}

// buildCertInfo 把面板下发的 tls_settings 归一化成 CertInfo。
// 面板没配证书（cert_mode 为空或 none）时返回 nil，由本地 config.json 兜底。
func buildCertInfo(t *TlsSettings, cm *CommonNode) *CertInfo {
	if t == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(t.CertMode))
	if mode == "" || mode == "none" {
		return nil
	}
	info := &CertInfo{
		CertMode:             mode,
		CertFile:             t.CertFile,
		KeyFile:              t.KeyFile,
		TlsCert:              t.TlsCert,
		TlsKey:               t.TlsKey,
		Provider:             t.Provider,
		Email:                t.Email,
		PinnedPeerCertSha256: t.PinnedPeerCertSha256,
		DNSEnv:               map[string]string{},
	}
	// 证书域名优先用 tls_settings.server_name，退回节点的 server_name。
	info.CertDomain = t.ServerName
	if info.CertDomain == "" && cm != nil {
		info.CertDomain = cm.ServerName
	}
	// reject_unknown_sni 面板可能发 bool，也可能发 "true"/"1" 这样的字符串。
	switch v := t.RejectUnknownSni.(type) {
	case bool:
		info.RejectUnknownSni = v
	case string:
		info.RejectUnknownSni = v == "true" || v == "1"
	case float64:
		info.RejectUnknownSni = v != 0
	}
	// dns_env 形如 "K1=V1,K2=V2"，与 v2node 的解析方式一致。
	if t.DNSEnv != "" {
		for _, env := range strings.Split(t.DNSEnv, ",") {
			if kv := strings.SplitN(env, "=", 2); len(kv) == 2 {
				info.DNSEnv[kv[0]] = kv[1]
			}
		}
	}
	return info
}
