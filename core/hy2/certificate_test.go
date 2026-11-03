package hy2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testCertificatePEM(t testing.TB, serial int64) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writeTestFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func testCertificateFiles(t testing.TB) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cert, key := testCertificatePEM(t, 1)
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certFile, cert)
	writeTestFile(t, keyFile, key)
	return certFile, keyFile
}

func TestCertificateCacheConcurrentHandshakesAndRenewal(t *testing.T) {
	certFile, keyFile := testCertificateFiles(t)
	cache, err := newCertificateCache(certFile, keyFile, time.Hour, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	old, _ := cache.getCertificate(nil)
	s, err := startMasqHTTPServer("127.0.0.1:0", &tls.Config{GetCertificate: cache.getCertificate}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	url := "https://" + s.listener.Addr().String()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				resp, err := client.Get(url)
				if err != nil {
					t.Errorf("handshake/request: %v", err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				serial := resp.TLS.PeerCertificates[0].SerialNumber.Int64()
				if serial != 1 && serial != 2 {
					t.Errorf("unexpected certificate serial: %d", serial)
				}
			}
		})
	}
	cert2, key2 := testCertificatePEM(t, 2)
	// Renewal may update the two files separately. Never publish a mixed pair.
	writeTestFile(t, certFile, cert2)
	if err := cache.refresh(); err == nil {
		t.Fatal("mismatched renewal must fail")
	}
	if got, _ := cache.getCertificate(nil); got != old {
		t.Fatal("failed renewal replaced the cached certificate")
	}
	writeTestFile(t, keyFile, key2)
	if err := cache.refresh(); err != nil {
		t.Fatal(err)
	}
	renewed, _ := cache.getCertificate(nil)
	if renewed == old || renewed.Leaf.SerialNumber.Int64() != 2 {
		t.Fatal("valid renewal was not published")
	}
	if err := os.Remove(certFile); err != nil {
		t.Fatal(err)
	}
	if err := cache.refresh(); err == nil {
		t.Fatal("missing certificate must fail refresh")
	}
	// In-flight and subsequent handshakes must not need the missing PEM file.
	wg.Wait()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("cached handshake after removing file: %v", err)
	}
	_ = resp.Body.Close()
	if resp.TLS.PeerCertificates[0].SerialNumber.Int64() != 2 {
		t.Fatal("handshake did not use renewed certificate")
	}
}

func TestCertificateCacheBackgroundRefreshAndClose(t *testing.T) {
	certFile, keyFile := testCertificateFiles(t)
	cache, err := newCertificateCache(certFile, keyFile, 5*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	cert, key := testCertificatePEM(t, 2)
	writeTestFile(t, certFile, cert)
	writeTestFile(t, keyFile, key)
	deadline := time.Now().Add(3 * time.Second)
	for cache.current.Load().Leaf.SerialNumber.Int64() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("background certificate refresh timed out")
		}
		time.Sleep(time.Millisecond)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = cache.Close() })
	}
	wg.Wait()
	select {
	case <-cache.done:
	default:
		t.Fatal("Close returned before refresh loop exited")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = cache.getCertificate(nil) }); got != 0 {
		t.Fatalf("certificate lookup allocated: %v", got)
	}
}

func BenchmarkCertificateLookup(b *testing.B) {
	certFile, keyFile := testCertificateFiles(b)
	cache, err := newCertificateCache(certFile, keyFile, time.Hour, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer cache.Close()
	b.Run("load_pair_each_handshake", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached_certificate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = cache.getCertificate(nil)
		}
	})
}
