package sing

import (
	"strings"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

func singVlessNodeInfo(encryption string) *panel.NodeInfo {
	return &panel.NodeInfo{
		Type:     "vless",
		Security: panel.None,
		VAllss: &panel.VAllssNode{
			Network:    "tcp",
			Encryption: encryption,
			EncryptionSettings: panel.EncSettings{
				Mode:       "native",
				Ticket:     "600s",
				PrivateKey: "-NlpeELgsQXM3bwV4cCtWFhygmy4EYVbOzN0abnHP3I",
			},
		},
		Common: &panel.CommonNode{ServerPort: 10086},
	}
}

func TestSingRejectsVlessEncryption(t *testing.T) {
	c := &conf.Options{ListenIP: "0.0.0.0", SingOptions: conf.NewSingOptions()}
	_, err := getInboundOptions("test-enc", singVlessNodeInfo("mlkem768x25519plus"), c)
	if err == nil {
		t.Fatal("sing core must refuse a vless node with encryption instead of starting it unencrypted")
	}
	if !strings.Contains(err.Error(), "xray core") {
		t.Fatalf("error should point to the xray core, got: %v", err)
	}
}

func TestSingVlessWithoutEncryptionStillBuilds(t *testing.T) {
	c := &conf.Options{ListenIP: "0.0.0.0", SingOptions: conf.NewSingOptions()}
	for _, encryption := range []string{"", "none"} {
		if _, err := getInboundOptions("test", singVlessNodeInfo(encryption), c); err != nil {
			t.Fatalf("encryption %q: %v", encryption, err)
		}
	}
}
