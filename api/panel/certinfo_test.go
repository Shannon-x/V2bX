package panel

import (
	"encoding/json/v2"
	"os"
	"strings"
	"testing"
)

// 端到端：面板实际下发的那份 tls_settings，V2bX 必须能解析成 CertInfo。
// 这份 JSON 由 Xboard 侧的 ServerService 生成，格式对齐 v2node。
func TestBuildCertInfoFromPanelPayload(t *testing.T) {
	raw := os.Getenv("PANEL_PUSH")
	if raw == "" {
		raw = `{"tls_settings":{"cert_mode":"remote","server_name":"www.bing.com",` +
			`"tls_cert":"-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n",` +
			`"tls_key":"-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----\n",` +
			`"pinned_peer_cert_sha256":"deadbeef"}}`
	}
	var wrap struct {
		TlsSettings TlsSettings `json:"tls_settings"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatalf("解析面板下发的 tls_settings 失败: %v", err)
	}
	info := buildCertInfo(&wrap.TlsSettings, &CommonNode{ServerName: "fallback.example.com"})
	if info == nil {
		t.Fatal("cert_mode=remote 时应当解析出 CertInfo")
	}
	if info.CertMode != "remote" {
		t.Errorf("CertMode = %q，期望 remote", info.CertMode)
	}
	if info.CertDomain != "www.bing.com" {
		t.Errorf("CertDomain = %q，期望取 tls_settings.server_name", info.CertDomain)
	}
	if !strings.Contains(info.TlsCert, "BEGIN CERTIFICATE") {
		t.Errorf("证书内容没解析出来: %q", info.TlsCert)
	}
	if !strings.Contains(info.TlsKey, "BEGIN PRIVATE KEY") {
		t.Errorf("私钥内容没解析出来: %q", info.TlsKey)
	}
	if info.PinnedPeerCertSha256 == "" {
		t.Error("指纹没解析出来")
	}
}

// 面板没配证书时必须返回 nil，交回本地 config.json 兜底 —— 现有部署不能被影响。
func TestBuildCertInfoNilWhenNotConfigured(t *testing.T) {
	for _, raw := range []string{
		`{"tls_settings":{}}`,
		`{"tls_settings":{"cert_mode":""}}`,
		`{"tls_settings":{"cert_mode":"none"}}`,
	} {
		var wrap struct {
			TlsSettings TlsSettings `json:"tls_settings"`
		}
		if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
			t.Fatal(err)
		}
		if info := buildCertInfo(&wrap.TlsSettings, nil); info != nil {
			t.Errorf("%s 应当返回 nil，实际 %+v", raw, info)
		}
	}
}

// reject_unknown_sni 面板可能发 bool 也可能发字符串，都要认。
func TestBuildCertInfoRejectUnknownSniForms(t *testing.T) {
	cases := map[string]bool{
		`{"tls_settings":{"cert_mode":"file","reject_unknown_sni":true}}`:   true,
		`{"tls_settings":{"cert_mode":"file","reject_unknown_sni":"true"}}`: true,
		`{"tls_settings":{"cert_mode":"file","reject_unknown_sni":"1"}}`:    true,
		`{"tls_settings":{"cert_mode":"file","reject_unknown_sni":false}}`:  false,
		`{"tls_settings":{"cert_mode":"file"}}`:                             false,
	}
	for raw, want := range cases {
		var wrap struct {
			TlsSettings TlsSettings `json:"tls_settings"`
		}
		if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		info := buildCertInfo(&wrap.TlsSettings, nil)
		if info == nil {
			t.Fatalf("%s: 应当解析出 CertInfo", raw)
		}
		if info.RejectUnknownSni != want {
			t.Errorf("%s: RejectUnknownSni = %v，期望 %v", raw, info.RejectUnknownSni, want)
		}
	}
}

// dns_env 形如 "K1=V1,K2=V2"
func TestBuildCertInfoParsesDNSEnv(t *testing.T) {
	var wrap struct {
		TlsSettings TlsSettings `json:"tls_settings"`
	}
	raw := `{"tls_settings":{"cert_mode":"dns","dns_env":"CF_TOKEN=abc,CF_ZONE=xyz"}}`
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatal(err)
	}
	info := buildCertInfo(&wrap.TlsSettings, nil)
	if info.DNSEnv["CF_TOKEN"] != "abc" || info.DNSEnv["CF_ZONE"] != "xyz" {
		t.Errorf("dns_env 解析错误: %+v", info.DNSEnv)
	}
}
