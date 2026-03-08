package dispatcher

import (
	sync "sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

var managedWriterPool = sync.Pool{
	New: func() any { return &ManagedWriter{} },
}

type ManagedWriter struct {
	writer  buf.Writer
	manager *LinkManager
}

func newManagedWriter(writer buf.Writer, manager *LinkManager) *ManagedWriter {
	w := managedWriterPool.Get().(*ManagedWriter)
	w.writer = writer
	w.manager = manager
	return w
}

func (w *ManagedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	return w.writer.WriteMultiBuffer(mb)
}

func (w *ManagedWriter) Close() error {
	w.manager.RemoveWriter(w)
	err := common.Close(w.writer)
	w.writer = nil
	w.manager = nil
	managedWriterPool.Put(w)
	return err
}

type LinkManager struct {
	links map[*ManagedWriter]buf.Reader
	mu    sync.Mutex
}

func (m *LinkManager) AddLink(writer *ManagedWriter, reader buf.Reader) {
	m.mu.Lock()
	m.links[writer] = reader
	m.mu.Unlock()
}

func (m *LinkManager) RemoveWriter(writer *ManagedWriter) {
	m.mu.Lock()
	delete(m.links, writer)
	m.mu.Unlock()
}

func (m *LinkManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for w, r := range m.links {
		common.Close(w)
		common.Interrupt(r)
	}
}
