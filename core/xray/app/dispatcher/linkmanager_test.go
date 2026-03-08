package dispatcher

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

type testWriter struct {
	closeCalls atomic.Int32
}

func (w *testWriter) WriteMultiBuffer(buf.MultiBuffer) error {
	return nil
}

func (w *testWriter) Close() error {
	w.closeCalls.Add(1)
	return nil
}

func TestManagedWriterCloseIsIdempotent(t *testing.T) {
	underlying := &testWriter{}
	manager := &LinkManager{
		links: make(map[*ManagedWriter]buf.Reader),
	}
	writer := newManagedWriter(underlying, manager)
	manager.AddLink(writer, nil)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writer.Close(); err != nil {
				t.Errorf("Close() returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := underlying.closeCalls.Load(); got != 1 {
		t.Fatalf("underlying close calls = %d, want 1", got)
	}

	manager.mu.Lock()
	_, exists := manager.links[writer]
	manager.mu.Unlock()
	if exists {
		t.Fatal("writer should be removed from manager after close")
	}
}

func TestManagedWriterWriteAfterClose(t *testing.T) {
	writer := newManagedWriter(&testWriter{}, nil)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if err := writer.WriteMultiBuffer(nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("WriteMultiBuffer() error = %v, want %v", err, io.ErrClosedPipe)
	}
}

func TestLinkManagerNilReceiverSafe(t *testing.T) {
	var manager *LinkManager
	manager.AddLink(nil, nil)
	manager.RemoveWriter(nil)
	manager.CloseAll()
}
