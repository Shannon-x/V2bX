package node

import (
	"encoding/json"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
)

// H-10: inboundSignature must stay identical for metadata-only changes and
// differ for any field that changes how the inbound listener is built, so the
// poll path rebuilds exactly when (and only when) it must.
func TestInboundSignature(t *testing.T) {
	base := func() *panel.NodeInfo {
		return &panel.NodeInfo{
			Type:     "vless",
			Security: panel.Tls,
			Common:   &panel.CommonNode{ServerPort: 443},
			VAllss: &panel.VAllssNode{
				Network:         "ws",
				NetworkSettings: json.RawMessage(`{"path":"/ws"}`),
				Flow:            "",
				TlsSettings:     panel.TlsSettings{ServerName: "a.com"},
			},
		}
	}

	if inboundSignature(nil) != "" {
		t.Fatal("nil NodeInfo should yield empty signature")
	}

	// Identical content → identical signature (metadata-only refresh, no rebuild).
	if inboundSignature(base()) != inboundSignature(base()) {
		t.Fatal("identical NodeInfo produced different signatures")
	}

	sig := inboundSignature(base())

	cases := []struct {
		name   string
		mutate func(n *panel.NodeInfo)
	}{
		{"port", func(n *panel.NodeInfo) { n.Common.ServerPort = 8443 }},
		{"security", func(n *panel.NodeInfo) { n.Security = panel.Reality }},
		{"network", func(n *panel.NodeInfo) { n.VAllss.Network = "grpc" }},
		{"networkSettings", func(n *panel.NodeInfo) { n.VAllss.NetworkSettings = json.RawMessage(`{"path":"/new"}`) }},
		{"flow", func(n *panel.NodeInfo) { n.VAllss.Flow = "xtls-rprx-vision" }},
		{"tlsServerName", func(n *panel.NodeInfo) { n.VAllss.TlsSettings.ServerName = "b.com" }},
		{"realityPrivateKey", func(n *panel.NodeInfo) { n.VAllss.TlsSettings.PrivateKey = "k" }},
	}
	for _, tc := range cases {
		n := base()
		tc.mutate(n)
		if inboundSignature(n) == sig {
			t.Fatalf("changing %s must change the inbound signature but did not", tc.name)
		}
	}
}

func TestInboundSignatureShadowsocks(t *testing.T) {
	base := &panel.NodeInfo{
		Type:        "shadowsocks",
		Common:      &panel.CommonNode{ServerPort: 1234},
		Shadowsocks: &panel.ShadowsocksNode{Cipher: "aes-128-gcm", ServerKey: "k1"},
	}
	sig := inboundSignature(base)

	cipherChanged := &panel.NodeInfo{
		Type:        "shadowsocks",
		Common:      &panel.CommonNode{ServerPort: 1234},
		Shadowsocks: &panel.ShadowsocksNode{Cipher: "aes-256-gcm", ServerKey: "k1"},
	}
	if inboundSignature(cipherChanged) == sig {
		t.Fatal("cipher change must change the signature")
	}

	keyChanged := &panel.NodeInfo{
		Type:        "shadowsocks",
		Common:      &panel.CommonNode{ServerPort: 1234},
		Shadowsocks: &panel.ShadowsocksNode{Cipher: "aes-128-gcm", ServerKey: "k2"},
	}
	if inboundSignature(keyChanged) == sig {
		t.Fatal("server key change must change the signature")
	}
}
