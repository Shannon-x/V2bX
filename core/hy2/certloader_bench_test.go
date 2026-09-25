package hy2

import (
	"crypto/tls"
	"testing"
	"time"
)

// 旧实现：每次握手都重新读、解析证书和私钥。
func BenchmarkGetCertificateReloadEveryHandshake(b *testing.B) {
	cf, kf := writePair(b, b.TempDir(), newTestPair(b), time.Now().Add(-time.Hour))
	b.ReportAllocs()
	for b.Loop() {
		c, err := tls.LoadX509KeyPair(cf, kf)
		if err != nil {
			b.Fatal(err)
		}
		_ = &c
	}
}

// 新实现：文件没变时只 stat 两个文件。
func BenchmarkGetCertificateCached(b *testing.B) {
	cf, kf := writePair(b, b.TempDir(), newTestPair(b), time.Now().Add(-time.Hour))
	l := newCertLoader(cf, kf)
	if err := l.init(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := l.GetCertificate(nil); err != nil {
			b.Fatal(err)
		}
	}
}
