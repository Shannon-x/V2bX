package xray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport/internet/tls"
	"github.com/xtls/xray-core/transport/internet/websocket"
)

// writeTestCert writes a throw-away self-signed certificate so the TLS branch
// of buildInbound (which reads the files at build time) can be exercised.
func writeTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "anytls.example.com"},
		DNSNames:     []string{"anytls.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func buildTestAnyTLSInbound(t *testing.T, option *conf.Options, node *panel.AnyTlsNode, security int) (*anytls.ServerConfig, *proxyman.ReceiverConfig) {
	t.Helper()
	nodeInfo := &panel.NodeInfo{
		Type:     "anytls",
		Security: security,
		AnyTls:   node,
		Common:   &panel.CommonNode{ServerPort: 10086},
	}
	ihc, err := buildInbound(option, nodeInfo, "test-anytls")
	if err != nil {
		t.Fatal(err)
	}
	proxyMessage, err := ihc.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	serverConfig, ok := proxyMessage.(*anytls.ServerConfig)
	if !ok {
		t.Fatalf("proxy settings type = %T, want *anytls.ServerConfig", proxyMessage)
	}
	receiverMessage, err := ihc.ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return serverConfig, receiverMessage.(*proxyman.ReceiverConfig)
}

// Plain AnyTLS node from the panel: tcp transport, TLS from the local
// CertConfig, padding scheme forwarded line by line, no static users (they are
// injected through AddUsers).
func TestBuildAnyTLSInboundDefaultsToTCPWithTLS(t *testing.T) {
	certFile, keyFile := writeTestCert(t)
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
		CertConfig: &conf.CertConfig{
			CertMode:         "file",
			CertFile:         certFile,
			KeyFile:          keyFile,
			RejectUnknownSni: true,
		},
	}
	serverConfig, rc := buildTestAnyTLSInbound(t, option, &panel.AnyTlsNode{
		PaddingScheme: []string{"stop=8", "0=30-30", "1=100-400"},
	}, panel.Tls)

	if got, want := serverConfig.PaddingScheme, "stop=8\n0=30-30\n1=100-400"; got != want {
		t.Fatalf("PaddingScheme = %q, want %q", got, want)
	}
	if len(serverConfig.Users) != 0 {
		t.Fatalf("static users = %d, want 0 (users come from AddUsers)", len(serverConfig.Users))
	}
	ss := rc.StreamSettings
	if ss == nil {
		t.Fatal("StreamSettings missing")
	}
	if ss.ProtocolName != "tcp" {
		t.Fatalf("ProtocolName = %q, want tcp", ss.ProtocolName)
	}
	if len(ss.SecuritySettings) != 1 {
		t.Fatalf("SecuritySettings = %d entries, want 1 (tls)", len(ss.SecuritySettings))
	}
	securityMessage, err := ss.SecuritySettings[0].GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, ok := securityMessage.(*tls.Config)
	if !ok {
		t.Fatalf("security settings type = %T, want *tls.Config", securityMessage)
	}
	if len(tlsConfig.Certificate) != 1 || tlsConfig.Certificate[0].CertificatePath != certFile {
		t.Fatalf("certificate not wired from CertConfig: %#v", tlsConfig.Certificate)
	}
	if !tlsConfig.RejectUnknownSni {
		t.Fatal("RejectUnknownSni not propagated")
	}
	if len(tlsConfig.NextProtocol) != 0 {
		t.Fatalf("ALPN must stay unset for anytls (h3 is hysteria2/tuic only), got %v", tlsConfig.NextProtocol)
	}
}

// The panel may put AnyTLS behind a CDN: network + network_settings select the
// transport exactly like vless/trojan, and the HTTP-transport real-IP default
// applies.
func TestBuildAnyTLSInboundWebSocketTransport(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
		CertConfig:  &conf.CertConfig{CertMode: "none"},
	}
	_, rc := buildTestAnyTLSInbound(t, option, &panel.AnyTlsNode{
		Network:         "ws",
		NetworkSettings: json.RawMessage(`{"path":"/anytls","acceptProxyProtocol":true}`),
	}, panel.Tls)

	ss := rc.StreamSettings
	if ss == nil || ss.ProtocolName != "websocket" {
		t.Fatalf("ProtocolName = %v, want websocket", ss)
	}
	var wsConfig *websocket.Config
	for _, ts := range ss.TransportSettings {
		if ts.ProtocolName != "websocket" {
			continue
		}
		m, err := ts.Settings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		wsConfig = m.(*websocket.Config)
	}
	if wsConfig == nil {
		t.Fatalf("websocket transport settings missing: %#v", ss.TransportSettings)
	}
	if wsConfig.Path != "/anytls" {
		t.Fatalf("ws path = %q, want /anytls", wsConfig.Path)
	}
	if !wsConfig.AcceptProxyProtocol {
		t.Fatal("acceptProxyProtocol from panel network_settings not applied")
	}
	if ss.SocketSettings == nil || !reflect.DeepEqual(ss.SocketSettings.TrustedXForwardedFor, []string{"CF-Connecting-IP"}) {
		t.Fatalf("CDN real-IP default missing for anytls over ws: %#v", ss.SocketSettings)
	}
	if len(ss.SecuritySettings) != 0 {
		t.Fatalf("CertMode none must not add TLS, got %d security settings", len(ss.SecuritySettings))
	}
}

func TestBuildAnyTLSInboundRejectsMissingNodeAndBadNetwork(t *testing.T) {
	option := &conf.Options{
		ListenIP:    "0.0.0.0",
		XrayOptions: &conf.XrayOptions{},
		CertConfig:  &conf.CertConfig{CertMode: "none"},
	}
	if _, err := buildInbound(option, &panel.NodeInfo{
		Type:     "anytls",
		Security: panel.Tls,
		Common:   &panel.CommonNode{ServerPort: 10086},
	}, "test-anytls"); err == nil {
		t.Fatal("expected error when AnyTls node info is missing")
	}
	if _, err := buildInbound(option, &panel.NodeInfo{
		Type:     "anytls",
		Security: panel.Tls,
		AnyTls: &panel.AnyTlsNode{
			Network:         "kcp",
			NetworkSettings: json.RawMessage(`{"seed":"x"}`),
		},
		Common: &panel.CommonNode{ServerPort: 10086},
	}, "test-anytls"); err == nil {
		t.Fatal("expected error for unsupported anytls transport")
	}
}

// Users are keyed by UUID → AnyTLS password, same as v2node and the sing-box
// core, so the panel's subscription (password = uuid) matches the node.
func TestBuildAnyTLSUserUsesUUIDAsPassword(t *testing.T) {
	u := panel.UserInfo{Id: 7, Uuid: "0a1b2c3d-4e5f-6789-abcd-ef0123456789"}
	user := buildAnyTLSUser("node-1", &u)
	if got, want := user.Email, format.UserTag("node-1", u.Uuid); got != want {
		t.Fatalf("Email = %q, want %q", got, want)
	}
	mem, err := user.ToMemoryUser()
	if err != nil {
		t.Fatal(err)
	}
	account, ok := mem.Account.(*anytls.MemoryAccount)
	if !ok {
		t.Fatalf("account type = %T, want *anytls.MemoryAccount", mem.Account)
	}
	if account.Password != u.Uuid {
		t.Fatalf("Password = %q, want uuid %q", account.Password, u.Uuid)
	}
	if got := len(buildAnyTLSUsers("node-1", []panel.UserInfo{u, u})); got != 2 {
		t.Fatalf("buildAnyTLSUsers len = %d, want 2", got)
	}
}

func TestXrayProtocolsIncludeAnyTLS(t *testing.T) {
	for _, p := range (&Xray{}).Protocols() {
		if p == "anytls" {
			return
		}
	}
	t.Fatal("anytls missing from Xray.Protocols(), the core selector would refuse anytls nodes")
}
