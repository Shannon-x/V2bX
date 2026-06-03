package xray

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"strconv"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// BuildInbound build Inbound config for different protocol
func buildInbound(option *conf.Options, nodeInfo *panel.NodeInfo, tag string) (*core.InboundHandlerConfig, error) {
	in := &coreConf.InboundDetourConfig{}
	var err error
	var network string
	switch nodeInfo.Type {
	case "vmess", "vless":
		err = buildV2ray(option, nodeInfo, in)
		network = nodeInfo.VAllss.Network
	case "trojan":
		err = buildTrojan(option, nodeInfo, in)
		if nodeInfo.Trojan.Network != "" {
			network = nodeInfo.Trojan.Network
		} else {
			network = "tcp"
		}
	case "shadowsocks":
		err = buildShadowsocks(option, nodeInfo, in)
		network = "tcp"
	case "hysteria2":
		err = buildHysteria2(nodeInfo, in)
		network = "hysteria"
	default:
		return nil, fmt.Errorf("unsupported node type: %s, Only support: V2ray, Trojan, Shadowsocks, Hysteria2", nodeInfo.Type)
	}
	if err != nil {
		return nil, err
	}
	// Set network protocol
	// Set server port
	in.PortList = &coreConf.PortList{
		Range: []coreConf.PortRange{
			{
				From: uint32(nodeInfo.Common.ServerPort),
				To:   uint32(nodeInfo.Common.ServerPort),
			}},
	}
	// Set Listen IP address
	ipAddress := net.ParseAddress(option.ListenIP)
	in.ListenOn = &coreConf.Address{Address: ipAddress}
	// Set SniffingConfig
	sniffingConfig := &coreConf.SniffingConfig{
		Enabled:      true,
		DestOverride: coreConf.StringList{"http", "tls"},
	}
	if option.XrayOptions.DisableSniffing {
		sniffingConfig.Enabled = false
	}
	in.SniffingConfig = sniffingConfig
	// Ensure StreamSetting is initialized before accessing sub-fields
	if in.StreamSetting == nil {
		t := coreConf.TransportProtocol(network)
		in.StreamSetting = &coreConf.StreamConfig{Network: &t}
	}

	// Determine ProxyProtocol: from panel NetworkSettings (v2node compat) OR local config
	enableProxyProtocol := option.XrayOptions.EnableProxyProtocol
	if !enableProxyProtocol {
		// Check panel's NetworkSettings for acceptProxyProtocol (as v2node does)
		var networkSettings json.RawMessage
		switch nodeInfo.Type {
		case "vmess", "vless":
			if nodeInfo.VAllss != nil {
				networkSettings = nodeInfo.VAllss.NetworkSettings
			}
		case "trojan":
			if nodeInfo.Trojan != nil {
				networkSettings = nodeInfo.Trojan.NetworkSettings
			}
		}
		if len(networkSettings) > 0 {
			var ppConfig struct {
				AcceptProxyProtocol bool `json:"acceptProxyProtocol"`
			}
			if json.Unmarshal(networkSettings, &ppConfig) == nil && ppConfig.AcceptProxyProtocol {
				enableProxyProtocol = true
			}
		}
	}

	switch network {
	case "tcp":
		if in.StreamSetting.TCPSettings != nil {
			in.StreamSetting.TCPSettings.AcceptProxyProtocol = enableProxyProtocol
		} else {
			tcpSetting := &coreConf.TCPConfig{
				AcceptProxyProtocol: enableProxyProtocol,
			}
			in.StreamSetting.TCPSettings = tcpSetting
		}
	case "ws":
		if in.StreamSetting.WSSettings != nil {
			in.StreamSetting.WSSettings.AcceptProxyProtocol = enableProxyProtocol
		} else {
			in.StreamSetting.WSSettings = &coreConf.WebSocketConfig{
				AcceptProxyProtocol: enableProxyProtocol,
			}
		}
	default:
		socketConfig := &coreConf.SocketConfig{
			AcceptProxyProtocol: enableProxyProtocol,
			TFO:                 option.XrayOptions.EnableTFO,
		}
		in.StreamSetting.SocketSettings = socketConfig
	}

	// Also set SocketSettings for universal ProxyProtocol support (v2node compat)
	if enableProxyProtocol {
		if in.StreamSetting.SocketSettings == nil {
			in.StreamSetting.SocketSettings = &coreConf.SocketConfig{}
		}
		in.StreamSetting.SocketSettings.AcceptProxyProtocol = true
	}

	// Set TLS or Reality settings
	switch nodeInfo.Security {
	case panel.Tls:
		// Normal tls
		if option.CertConfig == nil {
			return nil, errors.New("the CertConfig is not vail")
		}
		switch option.CertConfig.CertMode {
		case "none", "":
			break // disable
		default:
			in.StreamSetting.Security = "tls"
			in.StreamSetting.TLSSettings = &coreConf.TLSConfig{
				Certs: []*coreConf.TLSCertConfig{
					{
						CertFile:     option.CertConfig.CertFile,
						KeyFile:      option.CertConfig.KeyFile,
						OcspStapling: 3600,
					},
				},
				RejectUnknownSNI: option.CertConfig.RejectUnknownSni,
			}
			if nodeInfo.Type == "hysteria2" || nodeInfo.Type == "tuic" {
				alpnList := &coreConf.StringList{"h3"}
				in.StreamSetting.TLSSettings.ALPN = alpnList
			}
		}
	case panel.Reality:
		// Reality
		in.StreamSetting.Security = "reality"
		v := nodeInfo.VAllss
		dest := v.TlsSettings.Dest
		if dest == "" {
			dest = v.TlsSettings.ServerName
		}
		xver := v.TlsSettings.Xver
		if xver == 0 {
			xver = v.RealityConfig.Xver
		}
		d, err := json.Marshal(fmt.Sprintf(
			"%s:%s",
			dest,
			v.TlsSettings.ServerPort))
		if err != nil {
			return nil, fmt.Errorf("marshal reality dest error: %s", err)
		}
		mtd, _ := time.ParseDuration(v.RealityConfig.MaxTimeDiff)
		in.StreamSetting.REALITYSettings = &coreConf.REALITYConfig{
			Dest:         d,
			Xver:         xver,
			Show:         false,
			ServerNames:  []string{v.TlsSettings.ServerName},
			PrivateKey:   v.TlsSettings.PrivateKey,
			MinClientVer: v.RealityConfig.MinClientVer,
			MaxClientVer: v.RealityConfig.MaxClientVer,
			MaxTimeDiff:  uint64(mtd.Microseconds()),
			ShortIds:     []string{v.TlsSettings.ShortId},
			Mldsa65Seed:  v.TlsSettings.Mldsa65Seed,
		}
	default:
		break
	}
	in.Tag = tag
	return in.Build()
}

func buildV2ray(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	v := nodeInfo.VAllss
	if nodeInfo.Type == "vless" {
		//Set vless
		inbound.Protocol = "vless"
		if config.XrayOptions.EnableFallback {
			// Set fallback
			fallbackConfigs, err := buildVlessFallbacks(config.XrayOptions.FallBackConfigs)
			if err != nil {
				return err
			}
			s, err := json.Marshal(&coreConf.VLessInboundConfig{
				Decryption: "none",
				Fallbacks:  fallbackConfigs,
			})
			if err != nil {
				return fmt.Errorf("marshal vless fallback config error: %s", err)
			}
			inbound.Settings = (*json.RawMessage)(&s)
		} else {
			var err error
			decryption := "none"
			if nodeInfo.VAllss.Encryption != "" {
				switch nodeInfo.VAllss.Encryption {
				case "mlkem768x25519plus":
					encSettings := nodeInfo.VAllss.EncryptionSettings
					parts := []string{
						"mlkem768x25519plus",
						encSettings.Mode,
						encSettings.Ticket,
					}
					if encSettings.ServerPadding != "" {
						parts = append(parts, encSettings.ServerPadding)
					}
					parts = append(parts, encSettings.PrivateKey)
					decryption = strings.Join(parts, ".")
				default:
					return fmt.Errorf("vless decryption method %s is not support", nodeInfo.VAllss.Encryption)
				}
			}
			s, err := json.Marshal(&coreConf.VLessInboundConfig{
				Decryption: decryption,
			})
			if err != nil {
				return fmt.Errorf("marshal vless config error: %s", err)
			}
			inbound.Settings = (*json.RawMessage)(&s)
		}
	} else {
		// Set vmess
		inbound.Protocol = "vmess"
		var err error
		s, err := json.Marshal(&coreConf.VMessInboundConfig{})
		if err != nil {
			return fmt.Errorf("marshal vmess settings error: %s", err)
		}
		inbound.Settings = (*json.RawMessage)(&s)
	}
	if len(v.NetworkSettings) == 0 {
		return nil
	}

	t := coreConf.TransportProtocol(v.Network)
	inbound.StreamSetting = &coreConf.StreamConfig{Network: &t}
	switch v.Network {
	case "tcp":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.TCPSettings)
		if err != nil {
			return fmt.Errorf("unmarshal tcp settings error: %s", err)
		}
	case "ws":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.WSSettings)
		if err != nil {
			return fmt.Errorf("unmarshal ws settings error: %s", err)
		}
	case "grpc":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.GRPCSettings)
		if err != nil {
			return fmt.Errorf("unmarshal grpc settings error: %s", err)
		}
	case "httpupgrade":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.HTTPUPGRADESettings)
		if err != nil {
			return fmt.Errorf("unmarshal httpupgrade settings error: %s", err)
		}
	case "splithttp", "xhttp":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.SplitHTTPSettings)
		if err != nil {
			return fmt.Errorf("unmarshal xhttp settings error: %s", err)
		}
	default:
		return errors.New("the network type is not vail")
	}
	return nil
}

func buildTrojan(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "trojan"
	v := nodeInfo.Trojan
	if config.XrayOptions.EnableFallback {
		// Set fallback
		fallbackConfigs, err := buildTrojanFallbacks(config.XrayOptions.FallBackConfigs)
		if err != nil {
			return err
		}
		s, err := json.Marshal(&coreConf.TrojanServerConfig{
			Fallbacks: fallbackConfigs,
		})
		inbound.Settings = (*json.RawMessage)(&s)
		if err != nil {
			return fmt.Errorf("marshal trojan fallback config error: %s", err)
		}
	} else {
		s := []byte("{}")
		inbound.Settings = (*json.RawMessage)(&s)
	}
	network := v.Network
	if network == "" {
		network = "tcp"
	}
	t := coreConf.TransportProtocol(network)
	inbound.StreamSetting = &coreConf.StreamConfig{Network: &t}
	if len(v.NetworkSettings) == 0 {
		return nil
	}
	switch network {
	case "tcp":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.TCPSettings)
		if err != nil {
			return fmt.Errorf("unmarshal tcp settings error: %s", err)
		}
	case "ws":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.WSSettings)
		if err != nil {
			return fmt.Errorf("unmarshal ws settings error: %s", err)
		}
	case "grpc":
		err := json.Unmarshal(v.NetworkSettings, &inbound.StreamSetting.GRPCSettings)
		if err != nil {
			return fmt.Errorf("unmarshal grpc settings error: %s", err)
		}
	default:
		return errors.New("the network type is not vail")
	}
	return nil
}

func buildShadowsocks(config *conf.Options, nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "shadowsocks"
	s := nodeInfo.Shadowsocks
	settings := &coreConf.ShadowsocksServerConfig{
		Cipher: s.Cipher,
	}
	p := make([]byte, 32)
	_, err := rand.Read(p)
	if err != nil {
		return fmt.Errorf("generate random password error: %s", err)
	}
	randomPasswd := hex.EncodeToString(p)
	cipher := s.Cipher
	if s.ServerKey != "" {
		settings.Password = s.ServerKey
		randomPasswd = base64.StdEncoding.EncodeToString([]byte(randomPasswd))
		cipher = ""
	}
	defaultSSuser := &coreConf.ShadowsocksUserConfig{
		Cipher:   cipher,
		Password: randomPasswd,
	}
	settings.Users = append(settings.Users, defaultSSuser)
	settings.NetworkList = &coreConf.NetworkList{"tcp", "udp"}
	// IVCheck has been removed from xray-core
	t := coreConf.TransportProtocol("tcp")
	inbound.StreamSetting = &coreConf.StreamConfig{Network: &t}
	sets, err := json.Marshal(settings)
	inbound.Settings = (*json.RawMessage)(&sets)
	if err != nil {
		return fmt.Errorf("marshal shadowsocks settings error: %s", err)
	}
	return nil
}

func buildVlessFallbacks(fallbackConfigs []conf.FallBackConfigForXray) ([]*coreConf.VLessInboundFallback, error) {
	if fallbackConfigs == nil {
		return nil, fmt.Errorf("you must provide FallBackConfigs")
	}
	vlessFallBacks := make([]*coreConf.VLessInboundFallback, len(fallbackConfigs))
	for i, c := range fallbackConfigs {
		if c.Dest == "" {
			return nil, fmt.Errorf("dest is required for fallback fialed")
		}
		var dest json.RawMessage
		dest, err := json.Marshal(c.Dest)
		if err != nil {
			return nil, fmt.Errorf("marshal dest %s config fialed: %s", dest, err)
		}
		vlessFallBacks[i] = &coreConf.VLessInboundFallback{
			Name: c.SNI,
			Alpn: c.Alpn,
			Path: c.Path,
			Dest: dest,
			Xver: c.ProxyProtocolVer,
		}
	}
	return vlessFallBacks, nil
}

func buildTrojanFallbacks(fallbackConfigs []conf.FallBackConfigForXray) ([]*coreConf.TrojanInboundFallback, error) {
	if fallbackConfigs == nil {
		return nil, fmt.Errorf("you must provide FallBackConfigs")
	}

	trojanFallBacks := make([]*coreConf.TrojanInboundFallback, len(fallbackConfigs))
	for i, c := range fallbackConfigs {

		if c.Dest == "" {
			return nil, fmt.Errorf("dest is required for fallback fialed")
		}

		var dest json.RawMessage
		dest, err := json.Marshal(c.Dest)
		if err != nil {
			return nil, fmt.Errorf("marshal dest %s config fialed: %s", dest, err)
		}
		trojanFallBacks[i] = &coreConf.TrojanInboundFallback{
			Name: c.SNI,
			Alpn: c.Alpn,
			Path: c.Path,
			Dest: dest,
			Xver: c.ProxyProtocolVer,
		}
	}
	return trojanFallBacks, nil
}

func buildHysteria2(nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "hysteria"
	s := nodeInfo.Hysteria2
	if s == nil {
		return fmt.Errorf("hysteria2 config is missing")
	}
	settings := &coreConf.HysteriaServerConfig{
		Version: 2,
	}

	t := coreConf.TransportProtocol("hysteria")
	up := coreConf.Bandwidth(strconv.Itoa(s.UpMbps) + "mbps")
	down := coreConf.Bandwidth(strconv.Itoa(s.DownMbps) + "mbps")
	inbound.StreamSetting = &coreConf.StreamConfig{Network: &t}
	hysteriasetting := &coreConf.HysteriaConfig{
		Version: 2,
	}
	var finalmask *coreConf.FinalMask
	if !s.Ignore_Client_Bandwidth && (s.UpMbps > 0 || s.DownMbps > 0) {
		finalmask = &coreConf.FinalMask{
			QuicParams: &coreConf.QuicParamsConfig{
				Congestion: "force-brutal",
				BrutalUp:   up,
				BrutalDown: down,
			},
		}
	}
	if s.ObfsType != "" && s.ObfsPassword != "" {
		// W4.4 / audit #7: build the obfs settings via json.Marshal instead
		// of fmt.Sprintf, otherwise a panel-controlled password containing
		// `"`, `\`, or `"}{"...` would inject arbitrary JSON fields into the
		// xray config (or break parsing entirely → DoS).
		obfsSettings, mErr := json.Marshal(map[string]string{"password": s.ObfsPassword})
		if mErr != nil {
			return fmt.Errorf("marshal obfs settings error: %s", mErr)
		}
		rawobfsJSON := json.RawMessage(obfsSettings)
		udp := []coreConf.Mask{
			{
				Type:     s.ObfsType,
				Settings: &rawobfsJSON,
			},
		}
		if finalmask == nil {
			finalmask = &coreConf.FinalMask{}
		}
		finalmask.Udp = udp
	}
	inbound.StreamSetting.FinalMask = finalmask
	sets, err := json.Marshal(settings)
	inbound.Settings = (*json.RawMessage)(&sets)
	inbound.StreamSetting.HysteriaSettings = hysteriasetting
	if err != nil {
		return fmt.Errorf("marshal hysteria2 settings error: %s", err)
	}
	return nil
}
