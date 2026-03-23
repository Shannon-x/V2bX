package dispatcher

import (
	"io"
	sync "sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type ManagedWriter struct {
	mu      sync.RWMutex
	writer  buf.Writer
	manager *LinkManager
	closed  bool
}

func newManagedWriter(writer buf.Writer, manager *LinkManager) *ManagedWriter {
	return &ManagedWriter{
		writer:  writer,
		manager: manager,
	}
}

func (w *ManagedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.mu.RLock()
	writer := w.writer
	w.mu.RUnlock()
	if writer == nil {
		return io.ErrClosedPipe
	}
	return writer.WriteMultiBuffer(mb)
}

func (w *ManagedWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	writer := w.writer
	manager := w.manager
	w.writer = nil
	w.manager = nil
	w.mu.Unlock()

	if manager != nil {
		manager.RemoveWriter(w)
	}
	return common.Close(writer)
}

type LinkManager struct {
	links map[*ManagedWriter]buf.Reader
	mu    sync.RWMutex
}

func (m *LinkManager) AddLink(writer *ManagedWriter, reader buf.Reader) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.links[writer] = reader
	m.mu.Unlock()
}

func (m *LinkManager) RemoveWriter(writer *ManagedWriter) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.links, writer)
	m.mu.Unlock()
}

func (m *LinkManager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	links := make(map[*ManagedWriter]buf.Reader, len(m.links))
	for w, r := range m.links {
		links[w] = r
	}
	m.links = make(map[*ManagedWriter]buf.Reader)
	m.mu.Unlock()
	for w, r := range links {
		common.Close(w)
		common.Interrupt(r)
	}
}
