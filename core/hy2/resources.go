package hy2

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/apernet/hysteria/core/v2/server"
)

// nodeResources owns auxiliary resources after construction. The server owns
// its PacketConn separately. Close is also safe on partial-construction rollback.
type nodeResources struct {
	closers []io.Closer
	once    sync.Once
	err     error
}

func (r *nodeResources) add(c io.Closer) { r.closers = append(r.closers, c) }

func (r *nodeResources) Close() error {
	r.once.Do(func() {
		for i := len(r.closers) - 1; i >= 0; i-- {
			r.err = errors.Join(r.err, ignoreClosed(r.closers[i].Close()))
		}
		r.closers = nil
	})
	return r.err
}

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// NewServer closes resources on some errors, but validation errors occur before
// it takes ownership. The caller must cover both paths.
func closeUnstartedConfig(c *server.Config) error {
	var err error
	if c.Conn != nil {
		err = ignoreClosed(c.Conn.Close())
	}
	if c.Cleanup != nil {
		err = errors.Join(err, c.Cleanup.Close())
	}
	return err
}
