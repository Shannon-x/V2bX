package xray

import (
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/proxyman"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

func TestHysteria2QUICReachesCore(t *testing.T) {
	opt := &conf.Options{ListenIP: "127.0.0.1", XrayOptions: &conf.XrayOptions{
		Hysteria2QUIC: &conf.XrayQUICConfig{
			InitStreamReceiveWindow: 1 << 20, MaxStreamReceiveWindow: 4 << 20,
			InitConnectionReceiveWindow: 4 << 20, MaxConnectionReceiveWindow: 12 << 20,
			MaxIncomingStreams: 256, MaxIdleTimeout: 45,
		},
	}}
	n := &panel.NodeInfo{Type: "hysteria2", Common: &panel.CommonNode{ServerPort: 10086}, Hysteria2: &panel.Hysteria2Node{}}
	built, err := buildInbound(opt, n, "quic-budget-test")
	if err != nil {
		t.Fatal(err)
	}
	v, err := built.ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	q := v.(*proxyman.ReceiverConfig).StreamSettings.QuicParams
	if q == nil || q.InitStreamReceiveWindow != 1<<20 || q.MaxStreamReceiveWindow != 4<<20 || q.InitConnReceiveWindow != 4<<20 || q.MaxConnReceiveWindow != 12<<20 || q.MaxIncomingStreams != 256 || q.MaxIdleTimeout != 45 {
		t.Fatalf("QUIC settings did not reach core: %+v", q)
	}
}

func TestHysteria2QUICPreservesPanelSettings(t *testing.T) {
	in := &coreConf.InboundDetourConfig{}
	n := &panel.NodeInfo{Hysteria2: &panel.Hysteria2Node{UpMbps: 100, DownMbps: 100, ObfsType: "salamander", ObfsPassword: "secret"}}
	if err := buildHysteria2(n, in); err != nil {
		t.Fatal(err)
	}
	if err := applyHysteria2QUIC(&conf.XrayQUICConfig{MaxIncomingStreams: 256}, in); err != nil {
		t.Fatal(err)
	}
	if in.StreamSetting.FinalMask.QuicParams.Congestion != "force-brutal" || len(in.StreamSetting.FinalMask.Udp) != 1 {
		t.Fatal("QUIC override discarded congestion or obfuscation")
	}
}

func TestTFOForTCPAndWebSocket(t *testing.T) {
	for _, network := range []string{"tcp", "ws"} {
		n := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 10086}, VAllss: &panel.VAllssNode{Network: network}}
		opt := &conf.Options{ListenIP: "127.0.0.1", XrayOptions: &conf.XrayOptions{EnableTFO: true}}
		built, err := buildInbound(opt, n, "tfo-test")
		if err != nil {
			t.Fatal(err)
		}
		v, err := built.ReceiverSettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		sock := v.(*proxyman.ReceiverConfig).StreamSettings.SocketSettings
		if sock == nil || sock.Tfo == 0 {
			t.Fatalf("%s: TFO not propagated: %+v", network, sock)
		}
	}
}
