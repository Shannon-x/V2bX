package dispatcher

import (
	"io"
	sync "sync"
	"sync/atomic"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

// ManagedWriter wraps a buf.Writer so dispatcher can close it on user-delete.
//
// W3.8 / audit #53: writer is replaced atomically rather than guarded by an
// RWMutex. The hot path (WriteMultiBuffer) is hit on every MultiBuffer write
// — at 10 Gbps that's ~830k writes/sec; each previously cost two atomic
// operations from RLock/RUnlock. The new path is a single atomic Load.
//
// closeMu only protects the one-shot transition Close() makes (writer +
// manager → nil, closed → true); the read side never takes it.
type ManagedWriter struct {
	writer  atomic.Pointer[bufWriterHolder]
	manager *LinkManager
	closeMu sync.Mutex
	closed  bool
}

// bufWriterHolder wraps the interface value so it can be stored via
// atomic.Pointer[T] (which requires T be a concrete type, not an interface).
type bufWriterHolder struct {
	w buf.Writer
}

func newManagedWriter(writer buf.Writer, manager *LinkManager) *ManagedWriter {
	mw := &ManagedWriter{manager: manager}
	mw.writer.Store(&bufWriterHolder{w: writer})
	return mw
}

func (w *ManagedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	h := w.writer.Load()
	if h == nil || h.w == nil {
		return io.ErrClosedPipe
	}
	return h.w.WriteMultiBuffer(mb)
}

func (w *ManagedWriter) Close() error {
	w.closeMu.Lock()
	if w.closed {
		w.closeMu.Unlock()
		return nil
	}
	w.closed = true
	prev := w.writer.Swap(nil)
	manager := w.manager
	w.manager = nil
	w.closeMu.Unlock()

	if manager != nil {
		manager.RemoveWriter(w)
	}
	if prev != nil {
		return common.Close(prev.w)
	}
	return nil
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
