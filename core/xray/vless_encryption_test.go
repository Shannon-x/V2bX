package xray

import (
	"strings"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

// X25519 private key in the form `xray vlessenc` prints (base64url, clamped).
const testVlessEncPrivateKey = "-NlpeELgsQXM3bwV4cCtWFhygmy4EYVbOzN0abnHP3I"

func vlessEncryptionNodeInfo(encryption string) *panel.NodeInfo {
	return &panel.NodeInfo{
		Type:     "vless",
		Security: panel.None,
		VAllss: &panel.VAllssNode{
			Network:    "tcp",
			Flow:       "xtls-rprx-vision",
			Encryption: encryption,
			EncryptionSettings: panel.EncSettings{
				Mode:       "native",
				Ticket:     "600s",
				PrivateKey: testVlessEncPrivateKey,
			},
		},
		Common: &panel.CommonNode{ServerPort: 10086},
	}
}

func TestVlessEncryptionWithFallbackFails(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{EnableFallback: true},
	}
	_, err := buildInbound(option, vlessEncryptionNodeInfo("mlkem768x25519plus"), "test-enc-fallback")
	if err == nil {
		t.Fatal("encryption with fallback must fail instead of silently falling back to decryption none")
	}
	if !strings.Contains(err.Error(), "EnableFallback") {
		t.Fatalf("error should name the fallback option, got: %v", err)
	}
}

func TestVlessFallbackWithoutEncryptionStillWorks(t *testing.T) {
	option := &conf.Options{
		ListenIP: "0.0.0.0",
		XrayOptions: &conf.XrayOptions{
			EnableFallback:  true,
			FallBackConfigs: []conf.FallBackConfigForXray{{Dest: "80"}},
		},
	}
	for _, encryption := range []string{"", "none"} {
		if _, err := buildInbound(option, vlessEncryptionNodeInfo(encryption), "test-fallback"); err != nil {
			t.Fatalf("encryption %q with fallback: %v", encryption, err)
		}
	}
}

func TestVlessEncryptionWithoutFallbackBuilds(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
	}
	if _, err := buildInbound(option, vlessEncryptionNodeInfo("mlkem768x25519plus"), "test-enc"); err != nil {
		t.Fatalf("encryption without fallback should build: %v", err)
	}
}
