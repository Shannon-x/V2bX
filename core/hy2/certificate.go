package hy2

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const certificateRefreshInterval = 30 * time.Second

// certificateCache publishes immutable certificates. Handshakes never access
// the filesystem; a failed renewal leaves the last valid pair in service.
type certificateCache struct {
	certFile, keyFile string
	current           atomic.Pointer[tls.Certificate]
	refreshMu         sync.Mutex
	certHash, keyHash [sha256.Size]byte
	stop, done        chan struct{}
	closeOnce         sync.Once
}

func newCertificateCache(certFile, keyFile string, interval time.Duration, logger *zap.Logger) (*certificateCache, error) {
	c := &certificateCache{
		certFile: certFile,
		keyFile:  keyFile,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if err := c.refresh(); err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = certificateRefreshInterval
	}
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		failed := false
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				if err := c.refresh(); err != nil {
					if !failed && logger != nil {
						logger.Error("certificate refresh failed; retaining previous certificate", zap.Error(err))
					}
					failed = true
				} else {
					failed = false
				}
			}
		}
	}()
	return c, nil
}

func (c *certificateCache) refresh() error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	certPEM, err := os.ReadFile(c.certFile)
	if err != nil {
		return fmt.Errorf("read certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(c.keyFile)
	if err != nil {
		return fmt.Errorf("read certificate key: %w", err)
	}
	certHash, keyHash := sha256.Sum256(certPEM), sha256.Sum256(keyPEM)
	if c.current.Load() != nil && certHash == c.certHash && keyHash == c.keyHash {
		return nil
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("parse certificate pair: %w", err)
	}
	c.certHash, c.keyHash = certHash, keyHash
	c.current.Store(&cert)
	return nil
}

func (c *certificateCache) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.current.Load(), nil
}

func (c *certificateCache) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	<-c.done
	return nil
}
