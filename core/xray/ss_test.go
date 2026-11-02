package xray

import (
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
)

func newShadowsocksInstance(t *testing.T, node *panel.ShadowsocksNode) (*core.Instance, *core.InboundHandlerConfig) {
	t.Helper()
	nodeInfo := &panel.NodeInfo{
		Type:        "shadowsocks",
		Shadowsocks: node,
		Common:      &panel.CommonNode{ServerPort: 0},
	}
	ihc, err := buildInbound(&conf.Options{ListenIP: "127.0.0.1", XrayOptions: &conf.XrayOptions{}}, nodeInfo, "ss-test")
	if err != nil {
		t.Fatalf("buildInbound: %v", err)
	}
	instance, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Inbound: []*core.InboundHandlerConfig{ihc},
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	return instance, ihc
}

// Xray-core v26.9.30+ rejects SS2022 user keys whose length differs from the
// cipher's key length, so a wrong-sized placeholder user used to stop the
// whole node from starting with "proxy/shadowsocks_2022: bad key".
func TestShadowsocks2022InboundStarts(t *testing.T) {
	cases := []struct {
		cipher, serverKey string
		keyLen            int
	}{
		{"2022-blake3-aes-128-gcm", "AAAAAAAAAAAAAAAAAAAAAA==", 16},
		{"2022-blake3-aes-256-gcm", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", 32},
	}
	for _, c := range cases {
		t.Run(c.cipher, func(t *testing.T) {
			instance, ihc := newShadowsocksInstance(t, &panel.ShadowsocksNode{Cipher: c.cipher, ServerKey: c.serverKey})
			defer instance.Close()

			msg, err := ihc.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			cfg, ok := msg.(*shadowsocks_2022.MultiUserServerConfig)
			if !ok {
				t.Fatalf("proxy settings = %T, want multi-user SS2022", msg)
			}
			if len(cfg.Users) != 1 {
				t.Fatalf("placeholder users = %d, want 1", len(cfg.Users))
			}
			acc, err := cfg.Users[0].Account.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			mem, err := acc.(*shadowsocks_2022.Account).AsAccount()
			if err != nil {
				t.Fatal(err)
			}
			if got := len(mem.(*shadowsocks_2022.MemoryAccount).Key); got != c.keyLen {
				t.Fatalf("placeholder key = %d bytes, want %d", got, c.keyLen)
			}

			// Panel users must still be accepted by the running inbound.
			users := buildSSUsers("ss-test", []panel.UserInfo{{Id: 1, Uuid: "0123456789abcdef0123456789abcdef"}}, c.cipher, c.serverKey)
			if len(users) != 1 {
				t.Fatalf("built %d users, want 1", len(users))
			}
			mu, err := users[0].ToMemoryUser()
			if err != nil {
				t.Fatal(err)
			}
			if got := len(mu.Account.(*shadowsocks_2022.MemoryAccount).Key); got != c.keyLen {
				t.Fatalf("user key = %d bytes, want %d", got, c.keyLen)
			}
		})
	}
}

func TestShadowsocksLegacyInboundStillStarts(t *testing.T) {
	instance, _ := newShadowsocksInstance(t, &panel.ShadowsocksNode{Cipher: "aes-256-gcm"})
	defer instance.Close()
}

func TestSS2022KeyLength(t *testing.T) {
	for cipher, want := range map[string]int{
		"2022-blake3-aes-128-gcm":       16,
		"2022-BLAKE3-AES-128-GCM":       16,
		"2022-blake3-aes-256-gcm":       32,
		"2022-blake3-chacha20-poly1305": 32,
	} {
		if got := ss2022KeyLength(cipher); got != want {
			t.Errorf("ss2022KeyLength(%q) = %d, want %d", cipher, got, want)
		}
	}
}
