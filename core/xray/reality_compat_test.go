package xray

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/xtls/reality"
	xnet "github.com/xtls/xray-core/common/net"
	coreConf "github.com/xtls/xray-core/infra/conf"
	xreality "github.com/xtls/xray-core/transport/internet/reality"
)

// REALITY regression guard.
//
// xtls/reality 8cdf7bf (2026-09-08) started rejecting every ClientHello that
// does not carry an X25519MLKEM768 key share before X25519. sing-box (all
// versions), mihomo (defaults) and Xray's edge/ios/qq fingerprints send
// X25519 only, so their users silently fell back to the camouflage target
// ("reality verification failed"). go.mod pins a REALITY build that accepts
// them again; these tests fail if a dependency bump brings the check back.

const (
	realityCompatSNI     = "example.com"
	realityCompatShortID = "0123456789abcdef"
)

type realityCompatServer struct {
	addr      string
	publicKey string
}

// startRealityCompatServer starts a local TLS 1.3 "target" plus an Xray
// REALITY server in front of it. Each accepted REALITY connection reads
// "ping" and answers "pong".
func startRealityCompatServer(t *testing.T) realityCompatServer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: realityCompatSNI},
		DNSNames:     []string{realityCompatSNI},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	targetLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { targetLn.Close() })
	go func() {
		for {
			c, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				io.Copy(io.Discard, c)
			}()
		}
	}()

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := json.Marshal(targetLn.Addr().String())
	msg, err := (&coreConf.REALITYConfig{
		Dest:        dest,
		ServerNames: []string{realityCompatSNI},
		PrivateKey:  base64.RawURLEncoding.EncodeToString(priv.Bytes()),
		ShortIds:    []string{realityCompatShortID},
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := msg.(*xreality.Config).GetREALITYConfig()
	// A real server learns the target's post-handshake record lengths in the
	// background and holds each handshake until that is known. The local
	// target sends none worth imitating, so record "none" up front.
	for _, suffix := range []string{" 0", " 1", " 2"} {
		reality.GlobalPostHandshakeRecordsLens.Store(serverCfg.Dest+" "+realityCompatSNI+suffix, []int{})
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				sc, err := xreality.Server(c, serverCfg)
				if err != nil {
					return
				}
				buf := make([]byte, 4)
				if _, err := io.ReadFull(sc, buf); err != nil || string(buf) != "ping" {
					return
				}
				sc.Write([]byte("pong"))
				io.Copy(io.Discard, sc) // hold until the client closes
			}()
		}
	}()

	return realityCompatServer{
		addr:      ln.Addr().String(),
		publicKey: base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
	}
}

func realityCompatPing(c net.Conn) error {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return fmt.Errorf("write ping: %w", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		return fmt.Errorf("read pong: %w", err)
	}
	if string(buf) != "pong" {
		return fmt.Errorf("unexpected reply %q", buf)
	}
	return nil
}

func dialXrayReality(srv realityCompatServer, fingerprint, publicKey string) error {
	msg, err := (&coreConf.REALITYConfig{
		Fingerprint: fingerprint,
		ServerName:  realityCompatSNI,
		PublicKey:   publicKey,
		ShortId:     realityCompatShortID,
	}).Build()
	if err != nil {
		return err
	}
	raw, err := net.Dial("tcp", srv.addr)
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(20 * time.Second))
	host, port, _ := net.SplitHostPort(srv.addr)
	p, _ := xnet.PortFromString(port)
	c, err := xreality.UClient(raw, msg.(*xreality.Config), context.Background(),
		xnet.TCPDestination(xnet.ParseAddress(host), p))
	if err != nil {
		return err
	}
	return realityCompatPing(c)
}

// edge, ios and qq send X25519 without X25519MLKEM768; the others send both.
func TestRealityCompatXrayFingerprints(t *testing.T) {
	srv := startRealityCompatServer(t)
	for _, fp := range []string{"chrome", "firefox", "safari", "edge", "ios", "qq"} {
		t.Run(fp, func(t *testing.T) {
			if err := dialXrayReality(srv, fp, srv.publicKey); err != nil {
				t.Fatalf("REALITY handshake with Xray fingerprint %q failed: %v", fp, err)
			}
		})
	}
}

// Guards against a test that passes because the server accepts anything.
func TestRealityCompatRejectsWrongKey(t *testing.T) {
	srv := startRealityCompatServer(t)
	other, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong := base64.RawURLEncoding.EncodeToString(other.PublicKey().Bytes())
	if err := dialXrayReality(srv, "chrome", wrong); err == nil {
		t.Fatal("REALITY handshake succeeded with the wrong public key")
	}
}
