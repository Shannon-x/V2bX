package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var configDir string

var generateCommand = cobra.Command{
	Use:   "generate",
	Short: "Generate V2bX configuration files interactively",
	Run:   generateHandle,
}

func init() {
	generateCommand.PersistentFlags().
		StringVarP(&configDir, "config", "c",
			"/etc/V2bX", "config output directory")
	command.AddCommand(&generateCommand)
}

var scanner *bufio.Scanner

func prompt(msg string) string {
	fmt.Print(msg)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

func promptDefault(msg, def string) string {
	s := prompt(msg)
	if s == "" {
		return def
	}
	return s
}

func confirm(msg string) bool {
	s := strings.ToLower(prompt(msg))
	return s == "y" || s == "yes"
}

type generateConfig struct {
	Log   logConfig     `json:"Log"`
	Cores []interface{} `json:"Cores"`
	Nodes []interface{} `json:"Nodes"`
}

type logConfig struct {
	Level  string `json:"Level"`
	Output string `json:"Output"`
}

func generateHandle(_ *cobra.Command, _ []string) {
	scanner = bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	fmt.Println("V2bX 配置文件生成向导")
	fmt.Println("注意事项：")
	fmt.Println("1. 配置文件将保存到", configDir)
	fmt.Println("2. 已有 config.json 将备份为 config.json.bak")
	fmt.Println("3. 支持 Xray / Hysteria2 / Sing-box 核心")
	fmt.Println("4. Xray 核心已内置高性能连接参数优化")
	fmt.Println("5. 将自带审计规则")
	if !confirm("确定继续？(y/n) ") {
		return
	}

	apiHost := prompt("请输入机场网址(https://example.com)：")
	apiKey := prompt("请输入面板对接API Key：")
	fixedAPI := confirm("是否设置固定的机场网址和API Key？(y/n) ")
	if fixedAPI {
		fmt.Println("成功固定地址")
	}

	var fixedAPIVersion int
	coreUsed := map[string]bool{}
	var nodes []interface{}

	for {
		node, core, apiVer := addNodeConfig(apiHost, apiKey, fixedAPIVersion)
		if node != nil {
			nodes = append(nodes, node)
			coreUsed[core] = true
			if fixedAPI && fixedAPIVersion == 0 {
				fixedAPIVersion = apiVer
			}
		}

		cont := prompt("是否继续添加节点配置？(回车继续，输入n退出) ")
		if strings.HasPrefix(strings.ToLower(cont), "n") {
			break
		}
		if !fixedAPI {
			apiHost = prompt("请输入机场网址(https://example.com)：")
			apiKey = prompt("请输入面板对接API Key：")
		}
	}

	var cores []interface{}
	if coreUsed["xray"] {
		cores = append(cores, map[string]interface{}{
			"Type": "xray",
			"Log": map[string]interface{}{
				"Level":     "error",
				"ErrorPath": filepath.Join(configDir, "error.log"),
			},
			"AssetPath":          configDir + "/",
			"DnsConfigPath":      filepath.Join(configDir, "dns.json"),
			"OutboundConfigPath": filepath.Join(configDir, "custom_outbound.json"),
			"RouteConfigPath":    filepath.Join(configDir, "route.json"),
			"XrayConnectionConfig": map[string]interface{}{
				"handshake":    10,
				"connIdle":     300,
				"uplinkOnly":   2,
				"downlinkOnly": 4,
				"bufferSize":   256,
			},
		})
	}
	if coreUsed["hysteria2"] {
		cores = append(cores, map[string]interface{}{
			"Type": "hysteria2",
			"Log":  map[string]interface{}{"Level": "error"},
		})
	}
	if coreUsed["sing"] {
		cores = append(cores, map[string]interface{}{
			"Type": "sing",
			"Log": map[string]interface{}{
				"Disable":   false,
				"Level":     "error",
				"Timestamp": true,
			},
			"NTP": map[string]interface{}{
				"Enable":     false,
				"Server":     "time.apple.com",
				"ServerPort": 0,
			},
		})
	}

	cfg := generateConfig{
		Log:   logConfig{Level: "error", Output: ""},
		Cores: cores,
		Nodes: nodes,
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Println("创建目录失败:", err)
		return
	}

	// backup
	cfgPath := filepath.Join(configDir, "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		os.Rename(cfgPath, cfgPath+".bak")
	}

	if err := writeJSON(cfgPath, cfg); err != nil {
		fmt.Println("写入 config.json 失败:", err)
		return
	}
	fmt.Println("已生成", cfgPath)

	writeStaticFiles(configDir)
	fmt.Println("V2bX 配置文件生成完成")
}

func addNodeConfig(apiHost, apiKey string, fixedAPIVer int) (node interface{}, core string, apiVer int) {
	fmt.Println("请选择节点核心类型：")
	fmt.Println("1. xray")
	fmt.Println("2. hysteria2")
	fmt.Println("3. sing-box")
	coreChoice := prompt("请输入：")
	switch coreChoice {
	case "1":
		core = "xray"
	case "2":
		core = "hysteria2"
	case "3":
		core = "sing"
	default:
		fmt.Println("无效的选择。请选择 1、2 或 3。")
		return nil, "", 0
	}

	var nodeID int
	for {
		s := prompt("请输入节点Node ID：")
		var err error
		nodeID, err = strconv.Atoi(s)
		if err == nil && nodeID > 0 {
			break
		}
		fmt.Println("错误：请输入正确的数字作为Node ID。")
	}

	apiVer = fixedAPIVer
	if apiVer == 0 {
		fmt.Println("请选择面板 API 版本：")
		fmt.Println("1. V1 UniProxy (默认，兼容大部分面板)")
		fmt.Println("2. V2 Flat API (适用于 Shannon-x/v2board)")
		v := promptDefault("请输入 [默认1]：", "1")
		if v == "2" {
			apiVer = 2
		} else {
			apiVer = 1
		}
	}

	nodeType := ""
	if core == "hysteria2" {
		nodeType = "hysteria2"
	} else {
		fmt.Println("请选择节点传输协议：")
		fmt.Println("1. Shadowsocks")
		fmt.Println("2. Vless")
		fmt.Println("3. Vmess")
		fmt.Println("4. Trojan")
		if core == "xray" {
			fmt.Println("5. Hysteria2")
		}
		t := prompt("请输入：")
		switch t {
		case "1":
			nodeType = "shadowsocks"
		case "2":
			nodeType = "vless"
		case "3":
			nodeType = "vmess"
		case "4":
			nodeType = "trojan"
		case "5":
			nodeType = "hysteria2"
		default:
			nodeType = "shadowsocks"
		}
	}

	isReality := false
	isTLS := false
	if nodeType == "vless" {
		isReality = confirm("请选择是否为reality节点？(y/n) ")
	} else if nodeType == "hysteria2" {
		isTLS = true
	}
	if !isReality && !isTLS {
		isTLS = confirm("请选择是否进行TLS配置？(y/n) ")
	}

	certMode := "none"
	certDomain := "example.com"
	if !isReality && isTLS {
		fmt.Println("请选择证书申请模式：")
		fmt.Println("1. http模式自动申请，节点域名已正确解析")
		fmt.Println("2. dns模式自动申请，需填入正确域名服务商API参数")
		fmt.Println("3. file模式，自签证书或提供已有证书文件")
		cm := prompt("请输入：")
		switch cm {
		case "1":
			certMode = "http"
		case "2":
			certMode = "dns"
		case "3":
			certMode = "file"
		}
		certDomain = prompt("请输入节点证书域名(example.com)：")
		if certMode == "dns" {
			fmt.Println("请在配置生成后手动修改 DNSEnv 参数，然后重启V2bX！")
		}
	}

	n := map[string]interface{}{
		"Core":                   core,
		"ApiHost":                apiHost,
		"ApiKey":                 apiKey,
		"NodeID":                 nodeID,
		"NodeType":               nodeType,
		"Timeout":                30,
		"ApiVersion":             apiVer,
		"ListenIP":               "0.0.0.0",
		"SendIP":                 "0.0.0.0",
		"DeviceOnlineMinTraffic": 200,
		"ReportMinTraffic":       0,
		"CertConfig": map[string]interface{}{
			"CertMode":         certMode,
			"RejectUnknownSni": false,
			"CertDomain":       certDomain,
			"CertFile":         filepath.Join(configDir, certDomain+".cert.pem"),
			"KeyFile":          filepath.Join(configDir, certDomain+".key.pem"),
			"Email":            "v2bx@github.com",
			"Provider":         "cloudflare",
			"DNSEnv":           map[string]string{"EnvName": "env1"},
		},
	}

	switch core {
	case "xray":
		n["EnableProxyProtocol"] = false
		n["EnableUot"] = true
		n["EnableTFO"] = true
		n["DNSType"] = "UseIPv4"
		n["DisableSniffing"] = false
	case "hysteria2":
		n["Hysteria2ConfigPath"] = filepath.Join(configDir, "hy2config.yaml")
		n["ListenIP"] = ""
	case "sing":
		n["EnableTFO"] = false
		n["EnableSniff"] = true
		n["SniffOverrideDestination"] = true
	}

	return n, core, apiVer
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func writeStaticFiles(dir string) {
	// dns.json
	os.WriteFile(filepath.Join(dir, "dns.json"), []byte(`{
    "servers": [
        "1.1.1.1",
        "8.8.8.8",
        "localhost"
    ],
    "tag": "dns_inbound"
}`), 0644)

	// custom_outbound.json
	os.WriteFile(filepath.Join(dir, "custom_outbound.json"), []byte(`[
    {
        "tag": "IPv4_out",
        "protocol": "freedom",
        "settings": {
            "domainStrategy": "UseIPv4v6"
        }
    },
    {
        "tag": "IPv6_out",
        "protocol": "freedom",
        "settings": {
            "domainStrategy": "UseIPv6"
        }
    },
    {
        "protocol": "blackhole",
        "tag": "block"
    }
]`), 0644)

	// route.json
	os.WriteFile(filepath.Join(dir, "route.json"), []byte(`{
    "domainStrategy": "AsIs",
    "rules": [
        {
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "geoip:private"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "regexp:(api|ps|sv|offnavi|newvector|ulog\\.imap|newloc)(\\.map|)\\.(baidu|n\\.shifen)\\.com",
                "regexp:(.+\\.|^)(360|so)\\.(cn|com)",
                "regexp:(Subject|HELO|SMTP)",
                "regexp:(torrent|\\.torrent|peer_id=|info_hash|get_peers|find_node|BitTorrent|announce_peer|announce\\.php\\?passkey=)",
                "regexp:(^.@)(guerrillamail|guerrillamailblock|sharklasers|grr|pokemail|spam4|bccto|chacuo|027168)\\.(info|biz|com|de|net|org|me|la)",
                "regexp:(.?)(xunlei|sandai|Thunder|XLLiveUD)(.)",
                "regexp:(ed2k|\\.torrent|peer_id=|announce|info_hash|get_peers|find_node|BitTorrent|announce_peer|announce\\.php\\?passkey=|magnet:|xunlei|sandai|Thunder|XLLiveUD|bt_key)",
                "regexp:(.+\\.|^)(360)\\.(cn|com|net)",
                "regexp:(.*\\.||)(guanjia\\.qq\\.com|qqpcmgr|QQPCMGR)",
                "regexp:(.*\\.||)(rising|kingsoft|duba|xindubawukong|jinshanduba)\\.(com|net|org)",
                "regexp:(.*\\.||)(netvigator|torproject)\\.(com|cn|net|org)",
                "regexp:(.*\\.||)(miaozhen|cnzz|talkingdata|umeng)\\.(cn|com)",
                "regexp:(.*\\.||)(taobao)\\.(com)",
                "regexp:(.*\\.||)(laomoe|jiyou|ssss|lolicp|vv1234|0z|4321q|868123|ksweb|mm126)\\.(com|cloud|fun|cn|gs|xyz|cc)",
                "regexp:(flows|miaoko)\\.(pages)\\.(dev)"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "127.0.0.1/32",
                "10.0.0.0/8",
                "fc00::/7",
                "fe80::/10",
                "172.16.0.0/12"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "protocol": [
                "bittorrent"
            ]
        },
        {
            "type": "field",
            "outboundTag": "IPv4_out",
            "network": "udp,tcp"
        }
    ]
}`), 0644)

	// hy2config.yaml
	os.WriteFile(filepath.Join(dir, "hy2config.yaml"), []byte(`quic:
  initStreamReceiveWindow: 16777216
  maxStreamReceiveWindow: 16777216
  initConnReceiveWindow: 33554432
  maxConnReceiveWindow: 33554432
  maxIdleTimeout: 90s
  maxIncomingStreams: 4096
  disablePathMTUDiscovery: false
ignoreClientBandwidth: false
disableUDP: false
udpIdleTimeout: 120s
resolver:
  type: system
acl:
  inline:
    - direct(geosite:google)
    - reject(geosite:cn)
    - reject(geoip:cn)
masquerade:
  type: 404`), 0644)

	fmt.Println("已生成 dns.json, custom_outbound.json, route.json, hy2config.yaml")
}
