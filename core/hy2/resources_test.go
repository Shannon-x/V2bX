package hy2

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

func testNativeConfig(t *testing.T) (*panel.NodeInfo, *conf.Options) {
	t.Helper()
	cert, key := testCertificateFiles(t)
	return &panel.NodeInfo{Common: &panel.CommonNode{}, Hysteria2: &panel.Hysteria2Node{}}, &conf.Options{
		ListenIP: "127.0.0.1",
		CertConfig: &conf.CertConfig{
			CertMode: "file", CertFile: cert, KeyFile: key,
		},
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	_ = c.Close()
	return port
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func assertUDPReleased(t *testing.T, port int) {
	t.Helper()
	c, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("UDP port not released: %v", err)
	}
	_ = c.Close()
}

func assertTCPReleased(t *testing.T, addr string) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("TCP port not released: %v", err)
	}
	_ = l.Close()
}

func TestHyConfigConstructionRollback(t *testing.T) {
	for _, stage := range []string{"obfs", "resolver", "masquerade", "partial-masquerade"} {
		t.Run(stage, func(t *testing.T) {
			info, options := testNativeConfig(t)
			info.Common.ServerPort = freeUDPPort(t)
			c := &serverConfig{}
			var httpsAddr string
			switch stage {
			case "obfs":
				info.Hysteria2.ObfsType = "invalid"
			case "resolver":
				c.Resolver.Type = "invalid"
			case "masquerade":
				c.Masquerade.Type = "invalid"
			case "partial-masquerade":
				blocked, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer blocked.Close()
				httpsAddr = freeTCPAddress(t)
				c.Masquerade.ListenHTTPS = httpsAddr
				c.Masquerade.ListenHTTP = blocked.Addr().String()
			}
			n := &Hysteria2node{Tag: "rollback", Logger: zap.NewNop()}
			if config, err := n.getHyConfig(info, options, c); err == nil {
				_ = closeUnstartedConfig(config)
				t.Fatal("expected construction failure")
			}
			assertUDPReleased(t, info.Common.ServerPort)
			if httpsAddr != "" {
				assertTCPReleased(t, httpsAddr)
			}
		})
	}
}

func TestAddNodeValidationFailureReleasesResources(t *testing.T) {
	info, options := testNativeConfig(t)
	info.Common.ServerPort = freeUDPPort(t)
	httpsAddr := freeTCPAddress(t)
	options.Hysteria2ConfigPath = filepath.Join(t.TempDir(), "hy2.yaml")
	// This passes getHyConfig and fails inside server.NewServer's validation.
	writeTestFile(t, options.Hysteria2ConfigPath, []byte(fmt.Sprintf("udpIdleTimeout: 1s\nmasquerade:\n  listenHTTPS: %s\n", httpsAddr)))
	h := &Hysteria2{Hy2nodes: make(map[string]Hysteria2node), Auth: &V2bX{usersMap: make(map[string]int)}, Logger: zap.NewNop()}
	if err := h.AddNode("invalid", info, options); err == nil {
		_ = h.Close()
		t.Fatal("invalid upstream config must fail")
	}
	assertUDPReleased(t, info.Common.ServerPort)
	assertTCPReleased(t, httpsAddr)
	if len(h.Hy2nodes) != 0 {
		t.Fatal("failed node was published")
	}
}

func TestHyServerCloseStopsAuxiliaryResources(t *testing.T) {
	info, options := testNativeConfig(t)
	n := &Hysteria2node{Logger: zap.NewNop()}
	c := &serverConfig{Masquerade: serverConfigMasquerade{ListenHTTPS: "127.0.0.1:0"}}
	config, err := n.getHyConfig(info, options, c)
	if err != nil {
		t.Fatal(err)
	}
	resources := config.Cleanup.(*nodeResources)
	cache := resources.closers[0].(*certificateCache)
	masq := resources.closers[1].(*masqHTTPServer)
	udpPort := config.Conn.LocalAddr().(*net.UDPAddr).Port
	tcpAddr := masq.listener.Addr().String()
	config.Authenticator = &V2bX{usersMap: make(map[string]int)}
	s, err := server.NewServer(config)
	if err != nil {
		_ = closeUnstartedConfig(config)
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := resources.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, done := range []<-chan struct{}{cache.done, masq.done} {
		select {
		case <-done:
		default:
			t.Fatal("cleanup returned with auxiliary goroutine running")
		}
	}
	assertUDPReleased(t, udpPort)
	assertTCPReleased(t, tcpAddr)
}

func TestMasqueradeCloseClosesHijackedConnections(t *testing.T) {
	hijacked := make(chan net.Conn, 1)
	s, err := startMasqHTTPServer("127.0.0.1:0", nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		c, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		hijacked <- c
	}), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	addr := s.listener.Addr().String()
	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, _ = fmt.Fprint(client, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	select {
	case <-hijacked:
	case <-time.After(3 * time.Second):
		t.Fatal("hijack timed out")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
	wg.Wait()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("hijacked connection remained open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("hijacked connection was not closed before Close returned")
	}
	assertTCPReleased(t, addr)
}

func TestMasqueradeTLSAndRedirect(t *testing.T) {
	certFile, keyFile := testCertificateFiles(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	resources := &nodeResources{}
	defer resources.Close()
	config := &serverConfigMasquerade{ListenHTTPS: "127.0.0.1:0", ListenHTTP: "127.0.0.1:0", ForceHTTPS: true}
	err = startMasqueradeServers(config, &tls.Config{Certificates: []tls.Certificate{cert}}, 8443, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Alt-Svc", "upstream-value")
		_, _ = w.Write([]byte("hello"))
	}), resources, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	httpsServer := resources.closers[0].(*masqHTTPServer)
	httpServer := resources.closers[1].(*masqHTTPServer)
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("https://" + httpsServer.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("Alt-Svc") != `h3=":8443"; ma=2592000` {
		t.Fatalf("incorrect Alt-Svc: %q", resp.Header.Get("Alt-Svc"))
	}
	resp, err = client.Get("http://" + httpServer.listener.Addr().String() + "/path?q=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if want := "https://" + httpsServer.listener.Addr().String() + "/path?q=1"; resp.Header.Get("Location") != want {
		t.Fatalf("redirect=%q, want=%q", resp.Header.Get("Location"), want)
	}
}

type closeProbeServer struct {
	closed atomic.Int32
	err    error
}

func (*closeProbeServer) Serve() error { return nil }
func (s *closeProbeServer) Close() error {
	s.closed.Add(1)
	return s.err
}

func TestHyCoreCloseIsIdempotentAndClosesEveryNode(t *testing.T) {
	errOne, errTwo := errors.New("node one close failed"), errors.New("node two close failed")
	one := &closeProbeServer{err: errOne}
	two := &closeProbeServer{err: errTwo}
	h := &Hysteria2{Hy2nodes: map[string]Hysteria2node{
		"one": {Hy2server: one},
		"two": {Hy2server: two},
	}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := h.Close(); !errors.Is(err, errOne) || !errors.Is(err, errTwo) {
				t.Errorf("Close must retain both errors: %v", err)
			}
		})
	}
	wg.Wait()
	if one.closed.Load() != 1 || two.closed.Load() != 1 || len(h.Hy2nodes) != 0 {
		t.Fatal("Close did not release every node exactly once")
	}
	// A closed core must reject a new node before opening or reading anything.
	if err := h.AddNode("late", nil, nil); err == nil {
		t.Fatal("closed core accepted another node")
	}
}

func TestAddNodeReadConfigErrorIsReturned(t *testing.T) {
	h := &Hysteria2{Hy2nodes: make(map[string]Hysteria2node), Logger: zap.NewNop()}
	options := &conf.Options{Hysteria2ConfigPath: filepath.Join(t.TempDir(), "missing.yaml")}
	if err := h.AddNode("missing", nil, options); err == nil {
		t.Fatal("missing configuration must return an error")
	}
}
