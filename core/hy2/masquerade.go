package hy2

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/apernet/hysteria/extras/v2/correctnet"
	"go.uber.org/zap"
)

// Track accepted connections as well as the listener: http.Server.Close does
// not close hijacked connections (for example masqueraded WebSocket proxies).
type masqListener struct {
	net.Listener
	mu    sync.Mutex
	conns map[*masqConn]struct{}
}

type masqConn struct {
	net.Conn
	owner *masqListener
	once  sync.Once
	err   error
}

func (l *masqListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	conn := &masqConn{Conn: c, owner: l}
	l.mu.Lock()
	l.conns[conn] = struct{}{}
	l.mu.Unlock()
	return conn, nil
}

func (c *masqConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
	})
	return c.err
}

func (l *masqListener) closeConnections() {
	l.mu.Lock()
	conns := make([]*masqConn, 0, len(l.conns))
	for c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

type masqHTTPServer struct {
	server   *http.Server
	listener *masqListener
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	err      error
}

func startMasqHTTPServer(addr string, tlsConfig *tls.Config, handler http.Handler, logger *zap.Logger) (*masqHTTPServer, error) {
	listener, err := correctnet.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &masqHTTPServer{
		listener: &masqListener{Listener: listener, conns: make(map[*masqConn]struct{})},
		cancel:   cancel,
		done:     make(chan struct{}),
		server: &http.Server{
			Handler:     handler,
			TLSConfig:   tlsConfig,
			BaseContext: func(net.Listener) context.Context { return ctx },
		},
	}
	go func() {
		defer close(m.done)
		var err error
		if tlsConfig == nil {
			err = m.server.Serve(m.listener)
		} else {
			err = m.server.ServeTLS(m.listener, "", "")
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) && logger != nil {
			logger.Error("masquerade HTTP(S) server stopped", zap.Error(err))
		}
	}()
	return m, nil
}

func (m *masqHTTPServer) Close() error {
	m.once.Do(func() {
		m.cancel()
		m.err = ignoreClosed(m.server.Close())
		// Serve may not yet have registered its listener with http.Server.
		m.err = errors.Join(m.err, ignoreClosed(m.listener.Close()))
		<-m.done
		m.listener.closeConnections()
	})
	return m.err
}

func startMasqueradeServers(c *serverConfigMasquerade, tlsConfig *tls.Config, quicPort int, handler http.Handler, resources *nodeResources, logger *zap.Logger) error {
	if c.ListenHTTP != "" && c.ListenHTTPS == "" {
		return fmt.Errorf("masquerade.listenHTTPS having only HTTP server without HTTPS is not supported")
	}
	altSvcHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&masqResponseWriter{ResponseWriter: w, port: quicPort}, r)
	})
	if c.ListenHTTPS != "" {
		s, err := startMasqHTTPServer(c.ListenHTTPS, tlsConfig, altSvcHandler, logger)
		if err != nil {
			return fmt.Errorf("listen masquerade HTTPS: %w", err)
		}
		resources.add(s)
		if c.ListenHTTP != "" {
			httpsPort := extractPortFromAddr(s.listener.Addr().String())
			httpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.ForceHTTPS {
					host := r.Host
					if h, _, err := net.SplitHostPort(host); err == nil {
						host = h
					}
					if httpsPort != 443 {
						host = net.JoinHostPort(host, strconv.Itoa(httpsPort))
					} else if net.ParseIP(host) != nil && net.ParseIP(host).To4() == nil {
						host = "[" + host + "]"
					}
					http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
					return
				}
				altSvcHandler.ServeHTTP(w, r)
			})
			s, err = startMasqHTTPServer(c.ListenHTTP, nil, httpHandler, logger)
			if err != nil {
				return fmt.Errorf("listen masquerade HTTP: %w", err)
			}
			resources.add(s)
		}
	}
	return nil
}

type masqResponseWriter struct {
	http.ResponseWriter
	port        int
	wroteHeader bool
}

func (w *masqResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%d"; ma=2592000`, w.port))
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *masqResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *masqResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *masqResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *masqResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
