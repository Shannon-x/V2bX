package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

// 造一张和面板生成的形制一致的自签证书：EC P-256、CN=域名、长有效期。
func writeTestCert(t *testing.T, dir, cn string) (certPath string, certPEM string, certDER []byte, pubSPKI []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPath = filepath.Join(dir, "test.crt")
	if err := os.WriteFile(certPath, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return certPath, string(pemBytes), der, spki
}

// 两种指纹必须分别对应「证书 DER」和「公钥 SPKI」。
// 算混了客户端一定连不上，而且报错信息毫无指向性，所以这里钉死。
func TestCertFingerprintsMatchClientExpectations(t *testing.T) {
	dir := t.TempDir()
	certPath, _, der, spki := writeTestCert(t, dir, "www.bing.com")

	fp, err := certFingerprints(certPath)
	if err != nil {
		t.Fatal(err)
	}

	wantCert := sha256.Sum256(der)
	if got, want := fp.CertSha256, hex.EncodeToString(wantCert[:]); got != want {
		t.Errorf("证书指纹算错了\n  实际 %s\n  期望 %s（xray pinnedPeerCertSha256 / hysteria pinSHA256）", got, want)
	}

	wantPub := sha256.Sum256(spki)
	if got, want := fp.PubKeySha256, hex.EncodeToString(wantPub[:]); got != want {
		t.Errorf("公钥指纹算错了\n  实际 %s\n  期望 %s（sing-box certificate_public_key_sha256）", got, want)
	}

	if fp.CertSha256 == fp.PubKeySha256 {
		t.Error("两种指纹算出了同一个值，说明实现把证书和公钥搞混了")
	}
}

func TestCertFingerprintsRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := certFingerprints(bad); err == nil {
		t.Error("非 PEM 内容应当报错")
	}
	if _, err := certFingerprints(filepath.Join(dir, "missing.crt")); err == nil {
		t.Error("文件不存在应当报错")
	}
}

// 面板下发证书时必须覆盖本地配置，否则 remote 模式无从生效。
func TestApplyPanelCertOverridesLocal(t *testing.T) {
	c := &Controller{
		tag: "hysteria2-1",
		Options: &conf.Options{CertConfig: &conf.CertConfig{
			CertMode:   "self",
			CertDomain: "local.example.com",
			CertFile:   "/local/a.crt",
			KeyFile:    "/local/a.key",
		}},
	}
	c.applyPanelCert(&panel.CertInfo{
		CertMode:         "remote",
		CertDomain:       "www.bing.com",
		CertFile:         "/panel/b.crt",
		KeyFile:          "/panel/b.key",
		TlsCert:          "CERTPEM",
		TlsKey:           "KEYPEM",
		RejectUnknownSni: true,
	})
	cc := c.CertConfig
	if cc.CertMode != "remote" {
		t.Errorf("CertMode 未被面板覆盖: %s", cc.CertMode)
	}
	if cc.CertDomain != "www.bing.com" || cc.CertFile != "/panel/b.crt" || cc.KeyFile != "/panel/b.key" {
		t.Errorf("证书路径/域名未被面板覆盖: %+v", cc)
	}
	if cc.TlsCert != "CERTPEM" || cc.TlsKey != "KEYPEM" {
		t.Error("面板下发的证书内容没有带进来")
	}
	if !cc.RejectUnknownSni {
		t.Error("RejectUnknownSni 未被面板覆盖")
	}
}

// 面板没下发证书配置时，本地配置必须原样保留 —— 现有部署不能被影响。
func TestApplyPanelCertNilKeepsLocal(t *testing.T) {
	local := &conf.CertConfig{
		CertMode:   "http",
		CertDomain: "real.example.com",
		CertFile:   "/local/a.crt",
	}
	c := &Controller{tag: "x", Options: &conf.Options{CertConfig: local}}
	c.applyPanelCert(nil)
	if local.CertMode != "http" || local.CertDomain != "real.example.com" || local.CertFile != "/local/a.crt" {
		t.Errorf("面板未下发时不应改动本地配置: %+v", local)
	}
}

// 面板只给证书内容、不给路径时，要按 tag 生成互不冲突的默认路径。
func TestApplyPanelCertDefaultsPathPerTag(t *testing.T) {
	mk := func(tag string) *conf.CertConfig {
		c := &Controller{tag: tag, Options: &conf.Options{CertConfig: &conf.CertConfig{}}}
		c.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: "C", TlsKey: "K"})
		return c.CertConfig
	}
	a, b := mk("hysteria2-1"), mk("hysteria2-2")
	if a.CertFile == "" || a.KeyFile == "" {
		t.Fatal("未生成默认证书路径")
	}
	if a.CertFile == b.CertFile || a.KeyFile == b.KeyFile {
		t.Errorf("不同节点生成了相同的证书路径，会互相覆盖: %s / %s", a.CertFile, b.CertFile)
	}
}

func TestSanitizeTag(t *testing.T) {
	for in, want := range map[string]string{
		"hysteria2-1":    "hysteria2-1",
		"a/b":            "a_b",
		"../../etc/pass": "______etc_pass", // 6 个非法字符 -> 6 个下划线
		"":               "node",
	} {
		if got := sanitizeTag(in); got != want {
			t.Errorf("sanitizeTag(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// remote 模式：证书内容由面板下发，节点负责落盘。
func TestRequestCertRemoteWritesPanelCert(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "n.crt")
	keyFile := filepath.Join(dir, "n.key")
	c := &Controller{tag: "hy2", Options: &conf.Options{CertConfig: &conf.CertConfig{
		CertMode: "remote",
		CertFile: certFile,
		KeyFile:  keyFile,
		TlsCert:  "-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n",
		TlsKey:   "-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----\n",
	}}}
	if err := c.requestCert(); err != nil {
		t.Fatalf("requestCert: %v", err)
	}
	got, _ := os.ReadFile(certFile)
	if string(got) != c.CertConfig.TlsCert {
		t.Errorf("证书内容没写对: %q", got)
	}
	if fi, err := os.Stat(keyFile); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("私钥权限应为 0600，实际 %o", fi.Mode().Perm())
	}
}

// 面板换证书时节点必须跟着换，否则客户端按新指纹校验会连不上。
// 这是与 self 模式（文件在就跳过）最关键的区别。
func TestRequestCertRemoteRewritesWhenPanelCertChanges(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "n.crt")
	keyFile := filepath.Join(dir, "n.key")
	cc := &conf.CertConfig{
		CertMode: "remote", CertFile: certFile, KeyFile: keyFile,
		TlsCert: "OLD-CERT", TlsKey: "OLD-KEY",
	}
	c := &Controller{tag: "hy2", Options: &conf.Options{CertConfig: cc}}
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	cc.TlsCert, cc.TlsKey = "NEW-CERT", "NEW-KEY"
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(certFile)
	if string(got) != "NEW-CERT" {
		t.Errorf("面板换证书后节点没跟着换，实际还是 %q", got)
	}
}

func TestRequestCertRemoteRejectsMissingContent(t *testing.T) {
	dir := t.TempDir()
	c := &Controller{tag: "hy2", Options: &conf.Options{CertConfig: &conf.CertConfig{
		CertMode: "remote",
		CertFile: filepath.Join(dir, "n.crt"),
		KeyFile:  filepath.Join(dir, "n.key"),
	}}}
	if err := c.requestCert(); err == nil {
		t.Error("面板没下发证书内容时应当报错，而不是静默生成一张不匹配的证书")
	}
}
