package xray

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/proxyman"
)

func TestResolveTrustedXFF(t *testing.T) {
	localOpt := &conf.Options{XrayOptions: &conf.XrayOptions{
		TrustedXForwardedFor: []string{"CF-Connecting-IP"},
	}}
	noneOpt := &conf.Options{XrayOptions: &conf.XrayOptions{}}
	panelSockopt := json.RawMessage(`{"path":"/ws","sockopt":{"trustedXForwardedFor":["True-Client-IP"]}}`)

	if got := resolveTrustedXFF(localOpt, nil); !reflect.DeepEqual(got, []string{"CF-Connecting-IP"}) {
		t.Fatalf("local option: got %v", got)
	}
	// Local config wins over panel sockopt.
	if got := resolveTrustedXFF(localOpt, panelSockopt); !reflect.DeepEqual(got, []string{"CF-Connecting-IP"}) {
		t.Fatalf("local should win over panel: got %v", got)
	}
	if got := resolveTrustedXFF(noneOpt, panelSockopt); !reflect.DeepEqual(got, []string{"True-Client-IP"}) {
		t.Fatalf("panel sockopt: got %v", got)
	}
	if got := resolveTrustedXFF(noneOpt, nil); got != nil {
		t.Fatalf("no source: got %v, want nil", got)
	}
	if got := resolveTrustedXFF(noneOpt, json.RawMessage(`{not json`)); got != nil {
		t.Fatalf("malformed settings: got %v, want nil", got)
	}
	if got := resolveTrustedXFF(noneOpt, json.RawMessage(`{"path":"/ws"}`)); got != nil {
		t.Fatalf("settings without sockopt: got %v, want nil", got)
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

// Default behavior is unchanged: no trusted headers configured anywhere means
// the field stays empty and sources are never rewritten from XFF.
func TestBuildInboundNoTrustedXFFByDefault(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
	}
	rc := buildTestWSInbound(t, option, `{"path":"/ws"}`)
	if ss := rc.StreamSettings; ss != nil && ss.SocketSettings != nil &&
		len(ss.SocketSettings.TrustedXForwardedFor) > 0 {
		t.Fatalf("TrustedXForwardedFor unexpectedly set: %v", ss.SocketSettings.TrustedXForwardedFor)
	}
}
