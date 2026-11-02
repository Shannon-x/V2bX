package xray

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/core"
	coreConf "github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet"
)

// senderDomainStrategy returns the strategy the Freedom handler will actually
// use: Xray-core v26.9.30+ reads it only from streamSettings.sockopt.
func senderDomainStrategy(t *testing.T, ohc *core.OutboundHandlerConfig) internet.DomainStrategy {
	t.Helper()
	if ohc.SenderSettings == nil {
		return internet.DomainStrategy_AS_IS
	}
	msg, err := ohc.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	sender := msg.(*proxyman.SenderConfig)
	if sender.StreamSettings == nil || sender.StreamSettings.SocketSettings == nil {
		return internet.DomainStrategy_AS_IS
	}
	return sender.StreamSettings.SocketSettings.DomainStrategy
}

func TestBuildOutboundPutsDNSTypeInSockopt(t *testing.T) {
	cases := []struct {
		name      string
		enableDNS bool
		dnsType   string
		want      internet.DomainStrategy
	}{
		{"dns off keeps AsIs", false, "UseIPv4", internet.DomainStrategy_AS_IS},
		{"dns on with DNSType", true, "UseIPv4", internet.DomainStrategy_USE_IP4},
		{"dns on defaults to UseIP", true, "", internet.DomainStrategy_USE_IP},
		{"dns on with AsIs", true, "AsIs", internet.DomainStrategy_AS_IS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ohc, err := buildOutbound(&conf.Options{
				XrayOptions: &conf.XrayOptions{EnableDNS: c.enableDNS, DNSType: c.dnsType},
			}, "node")
			if err != nil {
				t.Fatal(err)
			}
			if got := senderDomainStrategy(t, ohc); got != c.want {
				t.Fatalf("domain strategy = %v, want %v", got, c.want)
			}
		})
	}
}

func buildMigratedOutbound(t *testing.T, raw string) *core.OutboundHandlerConfig {
	t.Helper()
	c := &coreConf.OutboundDetourConfig{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	migrateLegacyFreedomDomainStrategy(c)
	ohc, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	return ohc
}

func TestLegacyFreedomDomainStrategyIsMigrated(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want internet.DomainStrategy
	}{
		{"settings.domainStrategy",
			`{"protocol":"freedom","settings":{"domainStrategy":"UseIPv4v6"}}`,
			internet.DomainStrategy_USE_IP46},
		{"settings.targetStrategy wins over domainStrategy",
			`{"protocol":"freedom","settings":{"targetStrategy":"ForceIPv6","domainStrategy":"UseIPv4"}}`,
			internet.DomainStrategy_FORCE_IP6},
		{"direct alias",
			`{"protocol":"Direct","settings":{"domainStrategy":"UseIPv6"}}`,
			internet.DomainStrategy_USE_IP6},
		{"explicit sockopt wins",
			`{"protocol":"freedom","settings":{"domainStrategy":"UseIPv6"},"streamSettings":{"sockopt":{"domainStrategy":"UseIPv4","mark":255}}}`,
			internet.DomainStrategy_USE_IP4},
		{"AsIs stays AsIs",
			`{"protocol":"freedom","settings":{"domainStrategy":"AsIs"}}`,
			internet.DomainStrategy_AS_IS},
		{"no settings",
			`{"protocol":"freedom"}`,
			internet.DomainStrategy_AS_IS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := senderDomainStrategy(t, buildMigratedOutbound(t, c.raw)); got != c.want {
				t.Fatalf("domain strategy = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLegacyFreedomMigrationKeepsOtherSockopt(t *testing.T) {
	ohc := buildMigratedOutbound(t,
		`{"protocol":"freedom","settings":{"domainStrategy":"UseIPv6"},"streamSettings":{"sockopt":{"mark":255}}}`)
	msg, err := ohc.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	so := msg.(*proxyman.SenderConfig).StreamSettings.SocketSettings
	if so.DomainStrategy != internet.DomainStrategy_USE_IP6 || so.Mark != 255 {
		t.Fatalf("sockopt = strategy %v mark %d, want USE_IP6 and 255", so.DomainStrategy, so.Mark)
	}
}

// The legacy field is removed from settings, so Xray no longer logs its
// deprecation warning, while the other Freedom settings are kept.
func TestLegacyFreedomMigrationStripsDeprecatedField(t *testing.T) {
	c := &coreConf.OutboundDetourConfig{}
	raw := `{"protocol":"freedom","settings":{"DomainStrategy":"UseIPv4","userLevel":3}}`
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	migrateLegacyFreedomDomainStrategy(c)
	var settings map[string]any
	if err := json.Unmarshal(*c.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["DomainStrategy"]; ok || len(settings) != 1 || settings["userLevel"] != float64(3) {
		t.Fatalf("settings after migration = %v, want only userLevel 3", settings)
	}
	if c.StreamSetting.SocketSettings.DomainStrategy != "UseIPv4" {
		t.Fatalf("sockopt.domainStrategy = %q, want UseIPv4", c.StreamSetting.SocketSettings.DomainStrategy)
	}
}

func TestLegacyFreedomMigrationKeepsInvalidStrategyError(t *testing.T) {
	c := &coreConf.OutboundDetourConfig{}
	if err := json.Unmarshal([]byte(`{"protocol":"freedom","settings":{"domainStrategy":"UseIPv5"}}`), c); err != nil {
		t.Fatal(err)
	}
	migrateLegacyFreedomDomainStrategy(c)
	if _, err := c.Build(); err == nil {
		t.Fatal("invalid domain strategy was accepted")
	}
}

func TestLegacyFreedomMigrationIgnoresOtherProtocols(t *testing.T) {
	c := &coreConf.OutboundDetourConfig{}
	raw := `{"protocol":"socks","settings":{"domainStrategy":"UseIPv4","servers":[{"address":"127.0.0.1","port":1080}]}}`
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	migrateLegacyFreedomDomainStrategy(c)
	if c.StreamSetting != nil {
		t.Fatalf("non-freedom outbound got streamSettings: %+v", c.StreamSetting)
	}
}

func TestBuildCustomOutboundMigratesLegacyFreedom(t *testing.T) {
	ohc, err := buildCustomOutbound(`{"tag":"x","protocol":"freedom","settings":{"domainStrategy":"UseIPv4"}}`, "node-tag")
	if err != nil {
		t.Fatal(err)
	}
	if ohc.Tag != "node-tag" {
		t.Fatalf("tag = %q, want node-tag", ohc.Tag)
	}
	if got := senderDomainStrategy(t, ohc); got != internet.DomainStrategy_USE_IP4 {
		t.Fatalf("domain strategy = %v, want USE_IP4", got)
	}
}

// scriptCustomOutbound extracts the custom_outbound.json heredoc a script writes.
func scriptCustomOutbound(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const start = "cat <<'EOF' > /etc/V2bX/custom_outbound.json\n"
	s := string(data)
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("%s: custom_outbound.json heredoc not found", path)
	}
	body := s[i+len(start):]
	end := strings.Index(body, "\nEOF\n")
	if end < 0 {
		t.Fatalf("%s: heredoc end not found", path)
	}
	return body[:end]
}

// The route "final" rule sends all traffic to IPv4_out, so its strategy must
// really reach the Freedom handler, both for files written by the current
// scripts and for files left on disk by older ones.
func TestCustomOutboundTemplatesKeepIPFamily(t *testing.T) {
	legacy := `[
  {"tag": "IPv4_out", "protocol": "freedom", "settings": {"domainStrategy": "UseIPv4v6"}},
  {"tag": "IPv6_out", "protocol": "freedom", "settings": {"domainStrategy": "UseIPv6"}},
  {"protocol": "blackhole", "tag": "block"}
]`
	exampleData, err := os.ReadFile("../../example/custom_outbound.json")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"initconfig.sh":       scriptCustomOutbound(t, "../../V2bX-script-master/initconfig.sh"),
		"V2bX.sh":             scriptCustomOutbound(t, "../../V2bX-script-master/V2bX.sh"),
		"legacy file on disk": legacy,
		"example":             string(exampleData),
	}
	want := map[string]internet.DomainStrategy{
		"IPv4_out": internet.DomainStrategy_USE_IP46,
		"IPv6_out": internet.DomainStrategy_USE_IP6,
	}
	for name, raw := range sources {
		t.Run(name, func(t *testing.T) {
			var outs []coreConf.OutboundDetourConfig
			if err := json.Unmarshal([]byte(raw), &outs); err != nil {
				t.Fatal(err)
			}
			seen := 0
			for i := range outs {
				c := &outs[i]
				migrateLegacyFreedomDomainStrategy(c)
				ohc, err := c.Build()
				if err != nil {
					t.Fatalf("%s: %v", c.Tag, err)
				}
				w, ok := want[c.Tag]
				if !ok {
					continue
				}
				if name == "example" && c.Tag == "IPv4_out" {
					// The example's IPv4_out has no strategy on purpose.
					w = internet.DomainStrategy_AS_IS
				}
				seen++
				if got := senderDomainStrategy(t, ohc); got != w {
					t.Fatalf("%s: domain strategy = %v, want %v", c.Tag, got, w)
				}
			}
			if seen != len(want) {
				t.Fatalf("checked %d of %d expected outbounds", seen, len(want))
			}
		})
	}
}
