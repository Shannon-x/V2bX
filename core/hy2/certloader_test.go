package hy2

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
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/conf"
)

// testPair 是一对证书/私钥 PEM 及其证书 DER 的 sha256（客户端 pinSHA256 锁定的值）。
type testPair struct {
	cert, key []byte
	pin       string
}

func newTestPair(t testing.TB) testPair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "www.bing.com"},
		DNSNames:     []string{"www.bing.com"},
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
	return testPair{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}),
		pin:  hex.EncodeToString(sum[:]),
	}
}

// writeFile 用「临时文件 + rename」写入（与节点落盘 remote 证书的方式一致），
// 并显式设置修改时间，避免文件系统时间精度不够导致测试偶发不稳定。
func writeFile(t testing.TB, path string, data []byte, mod time.Time) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, mod, mod); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func writePair(t testing.TB, dir string, p testPair, mod time.Time) (string, string) {
	c, k := filepath.Join(dir, "n.crt"), filepath.Join(dir, "n.key")
	writeFile(t, c, p.cert, mod)
	writeFile(t, k, p.key, mod)
	return c, k
}

func pinOf(t testing.TB, c *tls.Certificate) string {
	t.Helper()
	if c == nil || len(c.Certificate) == 0 {
		t.Fatal("没有拿到证书")
	}
	sum := sha256.Sum256(c.Certificate[0])
	return hex.EncodeToString(sum[:])
}

func TestCertLoaderPicksUpRotation(t *testing.T) {
	dir := t.TempDir()
	a, b := newTestPair(t), newTestPair(t)
	t0 := time.Now().Add(-time.Hour)
	cf, kf := writePair(t, dir, a, t0)

	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		t.Fatal(err)
	}
	got, err := l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != a.pin {
		t.Fatalf("应返回初始证书: err=%v", err)
	}

	writePair(t, dir, b, t0.Add(time.Minute))
	got, err = l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != b.pin {
		t.Fatalf("文件更新后应返回新证书: err=%v", err)
	}
}

// 文件没变时绝不重新读盘 —— 这是本次改动的性能来源。
func TestCertLoaderDoesNotReloadWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	cf, kf := writePair(t, dir, newTestPair(t), time.Now().Add(-time.Hour))
	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	l.onReload = func(error) { reloads.Add(1) }
	for i := 0; i < 1000; i++ {
		if _, err := l.GetCertificate(nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := reloads.Load(); n != 0 {
		t.Errorf("文件没变却重新加载了 %d 次", n)
	}
}

// 证书换了、私钥还没换（或文件写坏了）时，继续发旧证书，而不是让握手失败。
// 旧实现每次握手直接 LoadX509KeyPair，这个窗口里的握手全部失败。
func TestCertLoaderKeepsServingOldCertWhenNewFileBroken(t *testing.T) {
	dir := t.TempDir()
	a, b := newTestPair(t), newTestPair(t)
	t0 := time.Now().Add(-time.Hour)
	cf, kf := writePair(t, dir, a, t0)
	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		t.Fatal(err)
	}
	var failures atomic.Int32
	l.onReload = func(err error) {
		if err != nil {
			failures.Add(1)
		}
	}

	// 只换了证书、私钥还是旧的：证书和私钥不配对
	writeFile(t, cf, b.cert, t0.Add(time.Minute))
	got, err := l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != a.pin {
		t.Fatalf("新证书和旧私钥不配对时应继续用旧证书: err=%v", err)
	}
	// 写坏的文件
	writeFile(t, cf, []byte("garbage"), t0.Add(2*time.Minute))
	got, err = l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != a.pin {
		t.Fatalf("文件损坏时应继续用旧证书: err=%v", err)
	}
	if failures.Load() == 0 {
		t.Error("加载失败时应当通知（用于打日志）")
	}
	// 修好之后切换到新证书
	writePair(t, dir, b, t0.Add(3*time.Minute))
	got, err = l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != b.pin {
		t.Fatalf("文件修好后应切到新证书: err=%v", err)
	}
}

func TestCertLoaderKeepsServingWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	a := newTestPair(t)
	cf, kf := writePair(t, dir, a, time.Now().Add(-time.Hour))
	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		t.Fatal(err)
	}
	os.Remove(cf)
	got, err := l.GetCertificate(nil)
	if err != nil || pinOf(t, got) != a.pin {
		t.Fatalf("文件暂时不存在时应继续用缓存: err=%v", err)
	}
}

// 启动时证书就不可用必须报错，与原来的行为一致（不能带着空证书起来）。
func TestCertLoaderInitFailsWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	l := newCertLoader(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"))
	if err := l.init(); err == nil {
		t.Fatal("证书文件不存在时 init 应当报错")
	}
}

// 大量并发握手的同时轮换证书：不能报错、不能返回空证书、race 检测器不能报警。
func TestCertLoaderConcurrentRotation(t *testing.T) {
	dir := t.TempDir()
	pairs := []testPair{newTestPair(t), newTestPair(t), newTestPair(t)}
	valid := map[string]bool{}
	for _, p := range pairs {
		valid[p.pin] = true
	}
	t0 := time.Now().Add(-time.Hour)
	cf, kf := writePair(t, dir, pairs[0], t0)
	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, err := l.GetCertificate(nil)
				if err != nil || c == nil || len(c.Certificate) == 0 {
					bad.Add(1)
					continue
				}
				sum := sha256.Sum256(c.Certificate[0])
				if !valid[hex.EncodeToString(sum[:])] {
					bad.Add(1)
				}
			}
		}()
	}
	for i := 1; i <= 30; i++ {
		writePair(t, dir, pairs[i%len(pairs)], t0.Add(time.Duration(i)*time.Second))
		time.Sleep(2 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Errorf("并发轮换期间出现 %d 次错误或非法证书", n)
	}
	got, _ := l.GetCertificate(nil)
	if pinOf(t, got) != pairs[30%len(pairs)].pin {
		t.Error("轮换结束后应返回最后写入的证书")
	}
}

// 端到端：两个节点各自的证书文件，经 getTLSConfig 构建后做真实 TLS 握手，
// 客户端（按 pinSHA256 的算法）看到的必须是各自节点的证书 —— 包括不带 SNI 的客户端。
func TestHy2TLSConfigServesEachNodesOwnCert(t *testing.T) {
	type node struct {
		pair testPair
		cfg  *tls.Config
	}
	var nodes []node
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		p := newTestPair(t)
		cf, kf := writePair(t, dir, p, time.Now().Add(-time.Hour))
		tc, err := (&Hysteria2node{}).getTLSConfig(&conf.Options{CertConfig: &conf.CertConfig{
			CertMode: "remote", CertFile: cf, KeyFile: kf,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(tc.Certificates) != 0 {
			t.Error("不应再设置 Certificates，否则不带 SNI 的握手会一直用启动时的那张")
		}
		nodes = append(nodes, node{p, &tls.Config{GetCertificate: tc.GetCertificate}})
	}

	for i, n := range nodes {
		for _, sni := range []string{"www.bing.com", ""} {
			if got := handshakePin(t, n.cfg, sni); got != n.pair.pin {
				t.Errorf("节点 %d (SNI=%q) 发出的证书指纹 %s，应为 %s", i, sni, got, n.pair.pin)
			}
		}
	}
}

// handshakePin 做一次真实握手，返回服务端证书 DER 的 sha256。
func handshakePin(t *testing.T, serverCfg *tls.Config, sni string) string {
	t.Helper()
	sc, cc := net.Pipe()
	defer sc.Close()
	defer cc.Close()
	errc := make(chan error, 1)
	go func() { errc <- tls.Server(sc, serverCfg).Handshake() }()
	var pin string
	client := tls.Client(cc, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true, // 自签证书；这里只核对指纹
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			sum := sha256.Sum256(raw[0])
			pin = hex.EncodeToString(sum[:])
			return nil
		},
	})
	if err := client.Handshake(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("服务端握手失败: %v", err)
	}
	return pin
}
