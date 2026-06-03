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

// closeAllParallelThreshold is the link-count at which CloseAll switches
// from sequential to bounded-parallel close. Below this, goroutine setup
// is more expensive than the close work itself.
const closeAllParallelThreshold = 16

// closeAllWorkers caps the number of concurrent close goroutines so a
// million-link CloseAll doesn't try to spawn a million goroutines.
const closeAllWorkers = 32

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

	// W6 / B4: parallelize Close+Interrupt for large user sets. On a
	// ten-thousand-user DelNode this turns a sequential ~seconds-long
	// teardown into a parallel sub-second one. Below the threshold the
	// goroutine-setup overhead isn't worth it.
	if len(links) < closeAllParallelThreshold {
		for w, r := range links {
			common.Close(w)
			common.Interrupt(r)
		}
		return
	}

	sem := make(chan struct{}, closeAllWorkers)
	var wg sync.WaitGroup
	for w, r := range links {
		w, r := w, r
		sem <- struct{}{} // bounded — never spawn more than closeAllWorkers in flight
		wg.Add(1)
		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()
			common.Close(w)
			common.Interrupt(r)
		}()
	}
	wg.Wait()
}
