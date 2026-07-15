package xray

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/transport/internet/reality"
)

func TestResolveTrustedXFF(t *testing.T) {
	localOpt := &conf.Options{XrayOptions: &conf.XrayOptions{
		TrustedXForwardedFor: []string{"True-Client-IP"},
	}}
	noneOpt := &conf.Options{XrayOptions: &conf.XrayOptions{}}
	disabledOpt := &conf.Options{XrayOptions: &conf.XrayOptions{DisableCDNRealIP: true}}
	panelSockopt := json.RawMessage(`{"path":"/ws","sockopt":{"trustedXForwardedFor":["X-Real-IP"]}}`)

	// Explicit local option wins everywhere.
	if got := resolveTrustedXFF(localOpt, nil, "ws"); !reflect.DeepEqual(got, []string{"True-Client-IP"}) {
		t.Fatalf("local option: got %v", got)
	}
	if got := resolveTrustedXFF(localOpt, panelSockopt, "ws"); !reflect.DeepEqual(got, []string{"True-Client-IP"}) {
		t.Fatalf("local should win over panel: got %v", got)
	}
	// Panel sockopt beats the auto default.
	if got := resolveTrustedXFF(noneOpt, panelSockopt, "ws"); !reflect.DeepEqual(got, []string{"X-Real-IP"}) {
		t.Fatalf("panel sockopt: got %v", got)
	}
	// Auto default for HTTP transports with nothing configured.
	for _, nw := range []string{"ws", "httpupgrade", "xhttp", "splithttp", "grpc"} {
		if got := resolveTrustedXFF(noneOpt, nil, nw); !reflect.DeepEqual(got, []string{"CF-Connecting-IP"}) {
			t.Fatalf("auto default for %s: got %v", nw, got)
		}
	}
	// Non-HTTP transports never get the auto default.
	if got := resolveTrustedXFF(noneOpt, nil, "tcp"); got != nil {
		t.Fatalf("tcp auto default: got %v, want nil", got)
	}
	// Opt-out disables the auto default.
	if got := resolveTrustedXFF(disabledOpt, nil, "ws"); got != nil {
		t.Fatalf("DisableCDNRealIP: got %v, want nil", got)
	}
	// Malformed panel settings fall through to the auto default (not a crash).
	if got := resolveTrustedXFF(noneOpt, json.RawMessage(`{not json`), "ws"); !reflect.DeepEqual(got, []string{"CF-Connecting-IP"}) {
		t.Fatalf("malformed settings: got %v", got)
	}
}

func buildTestWSInbound(t *testing.T, option *conf.Options, networkSettings string) *proxyman.ReceiverConfig {
	t.Helper()
	nodeInfo := &panel.NodeInfo{
		Type:     "vless",
		Security: panel.None,
		VAllss: &panel.VAllssNode{
			Network:         "ws",
			NetworkSettings: json.RawMessage(networkSettings),
		},
		Common: &panel.CommonNode{ServerPort: 10086},
	}
	ihc, err := buildInbound(option, nodeInfo, "test-xff")
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := ihc.ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return receiver.(*proxyman.ReceiverConfig)
}

// End-to-end: the local TrustedXForwardedFor option must land in the built
// inbound's transport SocketConfig — that is the exact field xray's ws
// listener consults before rewriting the connection source from
// X-Forwarded-For.
func TestBuildInboundWiresTrustedXFFFromLocalConfig(t *testing.T) {
	option := &conf.Options{
		ListenIP: "0.0.0.0",
		XrayOptions: &conf.XrayOptions{
			TrustedXForwardedFor: []string{"CF-Connecting-IP"},
		},
	}
	rc := buildTestWSInbound(t, option, `{"path":"/ws"}`)
	ss := rc.StreamSettings
	if ss == nil || ss.SocketSettings == nil {
		t.Fatal("SocketSettings missing from built ws inbound")
	}
	if !reflect.DeepEqual(ss.SocketSettings.TrustedXForwardedFor, []string{"CF-Connecting-IP"}) {
		t.Fatalf("TrustedXForwardedFor: got %v", ss.SocketSettings.TrustedXForwardedFor)
	}
	// Not opted in via proxy protocol: must stay off.
	if ss.SocketSettings.AcceptProxyProtocol {
		t.Fatal("AcceptProxyProtocol must not be enabled as a side effect")
	}
}

func TestBuildInboundWiresTrustedXFFFromPanelSockopt(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
	}
	rc := buildTestWSInbound(t, option,
		`{"path":"/ws","sockopt":{"trustedXForwardedFor":["CF-Connecting-IP"]}}`)
	ss := rc.StreamSettings
	if ss == nil || ss.SocketSettings == nil {
		t.Fatal("SocketSettings missing from built ws inbound")
	}
	if !reflect.DeepEqual(ss.SocketSettings.TrustedXForwardedFor, []string{"CF-Connecting-IP"}) {
		t.Fatalf("TrustedXForwardedFor: got %v", ss.SocketSettings.TrustedXForwardedFor)
	}
}

// Out of the box (no config at all), a ws inbound gets the CF-Connecting-IP
// default so real client IPs work behind Cloudflare on a plain binary update.
func TestBuildInboundAutoTrustedXFFForWS(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
	}
	rc := buildTestWSInbound(t, option, `{"path":"/ws"}`)
	ss := rc.StreamSettings
	if ss == nil || ss.SocketSettings == nil {
		t.Fatal("SocketSettings missing from built ws inbound")
	}
	if !reflect.DeepEqual(ss.SocketSettings.TrustedXForwardedFor, []string{"CF-Connecting-IP"}) {
		t.Fatalf("auto default: got %v, want [CF-Connecting-IP]", ss.SocketSettings.TrustedXForwardedFor)
	}
}

// DisableCDNRealIP turns the auto default off for operators who do not want it.
func TestBuildInboundDisableCDNRealIP(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{DisableCDNRealIP: true},
	}
	rc := buildTestWSInbound(t, option, `{"path":"/ws"}`)
	if ss := rc.StreamSettings; ss != nil && ss.SocketSettings != nil &&
		len(ss.SocketSettings.TrustedXForwardedFor) > 0 {
		t.Fatalf("TrustedXForwardedFor set despite DisableCDNRealIP: %v", ss.SocketSettings.TrustedXForwardedFor)
	}
}

func buildTestRealityConfig(t *testing.T, minClientVer string) *reality.Config {
	t.Helper()
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
	}
	nodeInfo := &panel.NodeInfo{
		Type:     "vless",
		Security: panel.Reality,
		VAllss: &panel.VAllssNode{
			Network: "tcp",
			TlsSettings: panel.TlsSettings{
				ServerName: "example.com",
				Dest:       "example.com",
				ServerPort: "443",
				ShortId:    "0123456789abcdef",
				PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			},
			RealityConfig: panel.RealityConfig{MinClientVer: minClientVer},
		},
		Common: &panel.CommonNode{ServerPort: 10086},
	}
	ihc, err := buildInbound(option, nodeInfo, "test-reality")
	if err != nil {
		t.Fatal(err)
	}
	receiverMessage, err := ihc.ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	receiver := receiverMessage.(*proxyman.ReceiverConfig)
	if receiver.StreamSettings == nil || len(receiver.StreamSettings.SecuritySettings) != 1 {
		t.Fatalf("unexpected Reality security settings: %#v", receiver.StreamSettings)
	}
	securityMessage, err := receiver.StreamSettings.SecuritySettings[0].GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	realityConfig, ok := securityMessage.(*reality.Config)
	if !ok {
		t.Fatalf("security settings type = %T, want *reality.Config", securityMessage)
	}
	return realityConfig
}

// Xray 26.7.11 defaults an omitted minClientVer to 26.3.27. V2bX must write
// the v2node-compatible value explicitly so older Reality clients keep working.
func TestBuildRealityInboundUsesCompatibleMinimumClientVersion(t *testing.T) {
	config := buildTestRealityConfig(t, "")
	if got, want := config.MinClientVer, []byte{0, 0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MinClientVer = %v, want %v", got, want)
	}
}

func TestBuildRealityInboundPreservesExplicitMinimumClientVersion(t *testing.T) {
	config := buildTestRealityConfig(t, "26.3.27")
	if got, want := config.MinClientVer, []byte{26, 3, 27}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MinClientVer = %v, want %v", got, want)
	}
}
