package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

// genPanelCert 造一对面板 remote 模式会下发的证书和私钥（EC P-256），
// 返回 PEM 文本和证书 DER 的 sha256（即客户端 pinSHA256 锁定的值）。
func genPanelCert(t *testing.T, cn string) (certPEM, keyPEM, pin string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})),
		hex.EncodeToString(sum[:])
}

// 一键脚本给同一个域名的节点生成的本地证书配置：路径按域名命名，几个节点完全相同。
func scriptStyleCertConfig(dir, domain string) *conf.CertConfig {
	return &conf.CertConfig{
		CertMode:   "http",
		CertDomain: domain,
		CertFile:   filepath.Join(dir, domain+".cert.pem"),
		KeyFile:    filepath.Join(dir, domain+".key.pem"),
		Email:      "v2bx@github.com",
		Provider:   "cloudflare",
	}
}

// 回归：同一台机器上两个同域名的 hy2 节点，面板给各自签了不同的 remote 证书。
//
// 修复前两个节点都写 /etc/V2bX/<域名>.cert.pem，后写的覆盖先写的；hy2 每次握手
// 都从这个文件读证书，于是两个端口都发出最后写入的那张，锁定另一个节点指纹的
// 客户端全部握手失败。
func TestRemoteCertSameDomainNodesGetSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	const domain = "sggo.388898.xyz"
	certA, keyA, pinA := genPanelCert(t, "www.bing.com")
	certB, keyB, pinB := genPanelCert(t, "www.bing.com")

	a := &Controller{tag: "[https://panel.example.com]-hysteria2:129",
		Options: &conf.Options{CertConfig: scriptStyleCertConfig(dir, domain)}}
	b := &Controller{tag: "[https://panel.example.com]-hysteria2:130",
		Options: &conf.Options{CertConfig: scriptStyleCertConfig(dir, domain)}}
	if a.CertConfig.CertFile != b.CertConfig.CertFile {
		t.Fatal("前提不成立：本地配置里两个节点应当指向同一个证书文件")
	}

	a.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: certA, TlsKey: keyA, PinnedPeerCertSha256: pinA})
	b.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: certB, TlsKey: keyB, PinnedPeerCertSha256: pinB})

	if a.CertConfig.CertFile == b.CertConfig.CertFile || a.CertConfig.KeyFile == b.CertConfig.KeyFile {
		t.Fatalf("两个节点仍然共用证书文件: %s", a.CertConfig.CertFile)
	}
	for _, c := range []*Controller{a, b} {
		if filepath.Dir(c.CertConfig.CertFile) != dir || filepath.Dir(c.CertConfig.KeyFile) != dir {
			t.Errorf("应沿用本地配置的目录 %s，实际 %s / %s", dir, c.CertConfig.CertFile, c.CertConfig.KeyFile)
		}
	}

	// 按线上的顺序：129 先写，130 后写。修复前 130 会把 129 的证书覆盖掉。
	for _, c := range []*Controller{a, b} {
		if err := c.requestCert(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		c    *Controller
		pin  string
		name string
	}{{a, pinA, "129"}, {b, pinB, "130"}} {
		fp, err := certFingerprints(tc.c.CertConfig.CertFile)
		if err != nil {
			t.Fatal(err)
		}
		if fp.CertSha256 != tc.pin {
			t.Errorf("节点 %s 的证书指纹是 %s，但订阅里锁定的是 %s", tc.name, fp.CertSha256, tc.pin)
		}
		if _, err := tls.LoadX509KeyPair(tc.c.CertConfig.CertFile, tc.c.CertConfig.KeyFile); err != nil {
			t.Errorf("节点 %s 的证书和私钥不配对: %v", tc.name, err)
		}
	}
}

// 同一个域名，一个节点走 http（Let's Encrypt）、另一个走 remote 时，
// remote 节点绝不能把 LE 证书覆盖掉 —— 修复前它会往同一个文件里写自签证书。
func TestRemoteCertDoesNotOverwriteSharedDomainCert(t *testing.T) {
	dir := t.TempDir()
	local := scriptStyleCertConfig(dir, "sggo.388898.xyz")
	const leCert, leKey = "LE-CERT-FOR-sggo.388898.xyz", "LE-KEY"
	if err := os.WriteFile(local.CertFile, []byte(leCert), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local.KeyFile, []byte(leKey), 0o600); err != nil {
		t.Fatal(err)
	}

	certPEM, keyPEM, _ := genPanelCert(t, "www.bing.com")
	c := &Controller{tag: "[https://panel.example.com]-hysteria2:130", Options: &conf.Options{CertConfig: local}}
	c.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: certPEM, TlsKey: keyPEM})
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "sggo.388898.xyz.cert.pem"): leCert,
		filepath.Join(dir, "sggo.388898.xyz.key.pem"):  leKey,
	} {
		got, _ := os.ReadFile(path)
		if string(got) != want {
			t.Errorf("%s 被 remote 节点改写了: %q", filepath.Base(path), got)
		}
	}
}

// http/dns 模式下同域名共用一张 LE 证书是有意的设计，路径不能被改掉。
func TestPanelHTTPModeKeepsSharedDomainPath(t *testing.T) {
	dir := t.TempDir()
	local := scriptStyleCertConfig(dir, "sggo.388898.xyz")
	wantCert, wantKey := local.CertFile, local.KeyFile
	c := &Controller{tag: "[https://panel.example.com]-hysteria2:129", Options: &conf.Options{CertConfig: local}}
	for _, mode := range []string{"http", "dns", "self", "file"} {
		c.applyPanelCert(&panel.CertInfo{CertMode: mode})
		if c.CertConfig.CertFile != wantCert || c.CertConfig.KeyFile != wantKey {
			t.Errorf("面板模式 %s 下路径被改成了 %s / %s", mode, c.CertConfig.CertFile, c.CertConfig.KeyFile)
		}
	}
}

// 面板从 remote 改回别的模式（或不再下发证书配置）时，本地原来的模式和路径要回来。
// 修复前 applyPanelCert 原地改写配置，第一次覆盖后本地原值就丢了。
func TestApplyPanelCertRecomputesFromLocalEachTime(t *testing.T) {
	dir := t.TempDir()
	local := scriptStyleCertConfig(dir, "sggo.388898.xyz")
	origCert, origKey := local.CertFile, local.KeyFile
	c := &Controller{tag: "[https://panel.example.com]-hysteria2:129", Options: &conf.Options{CertConfig: local}}

	certPEM, keyPEM, _ := genPanelCert(t, "www.bing.com")
	c.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: certPEM, TlsKey: keyPEM})
	if c.CertConfig.CertMode != "remote" || c.CertConfig.CertFile == origCert {
		t.Fatalf("remote 没生效: %+v", c.CertConfig)
	}

	c.applyPanelCert(&panel.CertInfo{CertMode: "http"})
	if c.CertConfig.CertMode != "http" || c.CertConfig.CertFile != origCert || c.CertConfig.KeyFile != origKey {
		t.Errorf("改回 http 后应恢复本地路径 %s，实际 %s", origCert, c.CertConfig.CertFile)
	}
	if c.CertConfig.TlsCert != "" || c.CertConfig.TlsKey != "" {
		t.Error("离开 remote 模式后不应还带着面板的证书内容")
	}

	c.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: certPEM, TlsKey: keyPEM})
	c.applyPanelCert(nil)
	if c.CertConfig.CertMode != "http" || c.CertConfig.CertFile != origCert {
		t.Errorf("面板不再下发证书配置时应回到本地配置，实际 %+v", c.CertConfig)
	}
}

// 原来就没配本地路径的节点，remote 证书的位置保持不变，升级后不会换地方。
func TestRemoteCertPathUnchangedForNodesWithoutLocalPath(t *testing.T) {
	tag := "[https://panel.example.com]-hysteria2:129"
	c := &Controller{tag: tag, Options: &conf.Options{CertConfig: &conf.CertConfig{}}}
	c.applyPanelCert(&panel.CertInfo{CertMode: "remote", TlsCert: "C", TlsKey: "K"})
	want := filepath.Join(defaultCertDir, sanitizeTag(tag))
	if c.CertConfig.CertFile != want+".crt" || c.CertConfig.KeyFile != want+".key" {
		t.Errorf("默认路径变了: %s / %s", c.CertConfig.CertFile, c.CertConfig.KeyFile)
	}
}

// remote 证书写入：目录不存在要自动建，写完不能留下临时文件，权限要对。
func TestRequestCertRemoteWritesAtomically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "yet", "created")
	certPEM, keyPEM, pin := genPanelCert(t, "www.bing.com")
	c := &Controller{tag: "hy2", Options: &conf.Options{CertConfig: &conf.CertConfig{
		CertMode: "remote",
		CertFile: filepath.Join(dir, "n.crt"),
		KeyFile:  filepath.Join(dir, "n.key"),
		TlsCert:  certPEM,
		TlsKey:   keyPEM,
	}}}
	if err := c.requestCert(); err != nil {
		t.Fatalf("目标目录不存在时应自动创建: %v", err)
	}
	// 再换一张，走覆盖路径
	certPEM2, keyPEM2, pin2 := genPanelCert(t, "www.bing.com")
	c.CertConfig.TlsCert, c.CertConfig.TlsKey = certPEM2, keyPEM2
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	fp, err := certFingerprints(c.CertConfig.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	if fp.CertSha256 != pin2 || fp.CertSha256 == pin {
		t.Errorf("覆盖后指纹应为新证书 %s，实际 %s", pin2, fp.CertSha256)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
	if len(entries) != 2 {
		t.Errorf("目录里应只有证书和私钥两个文件，实际 %d 个", len(entries))
	}
	for path, perm := range map[string]os.FileMode{c.CertConfig.CertFile: 0o644, c.CertConfig.KeyFile: 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != perm {
			t.Errorf("%s 权限应为 %o，实际 %o", filepath.Base(path), perm, fi.Mode().Perm())
		}
	}
}

// 面板换了证书，入站签名必须变，节点才会重建并写入新证书；
// 内容没变时签名必须完全一致，否则每次轮询都会重建、反复断连。
func TestInboundSignatureTracksCertInfo(t *testing.T) {
	mk := func(cert string, env map[string]string) *panel.NodeInfo {
		return &panel.NodeInfo{Type: "hysteria2", Security: panel.Tls, CertInfo: &panel.CertInfo{
			CertMode: "remote", CertDomain: "www.bing.com", TlsCert: cert, TlsKey: "KEY-" + cert, DNSEnv: env,
		}}
	}
	env1 := map[string]string{}
	env2 := map[string]string{}
	for _, k := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		env1[k] = "v" + k
	}
	for _, k := range []string{"H", "G", "F", "E", "D", "C", "B", "A"} {
		env2[k] = "v" + k
	}

	base := inboundSignature(mk("CERT-1", env1))
	for i := 0; i < 50; i++ {
		if got := inboundSignature(mk("CERT-1", env2)); got != base {
			t.Fatalf("证书配置没变，签名却不同（会导致每次轮询都重建）:\n%s\n%s", base, got)
		}
	}
	if inboundSignature(mk("CERT-2", env1)) == base {
		t.Error("面板换了证书，签名却没变，节点不会写入新证书")
	}
	noCert := &panel.NodeInfo{Type: "hysteria2", Security: panel.Tls}
	if inboundSignature(noCert) == base {
		t.Error("面板撤掉证书配置时签名应当变化")
	}
	if strings.Contains(base, "KEY-CERT-1") || strings.Contains(base, "CERT-1") {
		t.Error("签名里不应出现证书或私钥原文")
	}
}
