package dispatcher

import (
	"io"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	xraydispatcher "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
)

type accountingWriter struct{}

func (accountingWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	buf.ReleaseMulti(mb)
	return nil
}

func TestStatWriterNormalAndSpliceAccounting(t *testing.T) {
	c := counter.NewTrafficCounter()
	ts := c.GetCounter("user")
	var writer buf.Writer = &SizeStatWriter{
		Counter: &counter.XrayTrafficCounter{V: &ts.DownCounter, Storage: ts},
		Writer:  accountingWriter{},
	}
	mb := buf.New()
	mb.Extend(123)
	if err := writer.WriteMultiBuffer(buf.MultiBuffer{mb}); err != nil {
		t.Fatal(err)
	}
	// This is the concrete assertion used by Xray's Linux splice path.
	// A separate locally-defined writer type silently fails it.
	upstream, ok := writer.(*xraydispatcher.SizeStatWriter)
	if !ok {
		t.Fatal("writer is not recognized by Xray splice accounting")
	}
	var collected int64
	drain := func() {
		c.IterateDirty(true, func(_ string, ts *counter.TrafficStorage) bool {
			collected += ts.DownCounter.Swap(0)
			return true
		})
	}
	drain()
	upstream.Counter.Add(4096)
	drain()
	if collected != 4219 {
		t.Fatalf("normal + splice bytes = %d, want 4219", collected)
	}
}

type finalDataReader struct{}

func (finalDataReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	b := buf.New()
	b.Extend(17)
	return buf.MultiBuffer{b}, io.EOF
}

func (r finalDataReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return r.ReadMultiBuffer()
}

func TestCounterReaderPreservesFinalData(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		c := counter.NewTrafficCounter()
		ts := c.GetCounter("user")
		r := &CounterReader{Reader: finalDataReader{}, Counter: &ts.UpCounter, Storage: ts}
		read := r.ReadMultiBuffer
		if timeout {
			read = func() (buf.MultiBuffer, error) { return r.ReadMultiBufferTimeout(time.Second) }
		}
		mb, err := read()
		if mb.Len() != 17 || err != io.EOF {
			t.Fatalf("timeout=%v: final read = %d, %v", timeout, mb.Len(), err)
		}
		buf.ReleaseMulti(mb)
		var collected int64
		c.IterateDirty(true, func(_ string, ts *counter.TrafficStorage) bool {
			collected += ts.UpCounter.Swap(0)
			return true
		})
		if collected != 17 {
			t.Fatalf("timeout=%v: counted %d, want 17", timeout, collected)
		}
	}
}

func TestSniffCachePreservesFinalData(t *testing.T) {
	r := &cachedReader{reader: finalDataReader{}}
	payload := buf.New()
	defer payload.Release()
	if err := r.Cache(payload, time.Second); err != io.EOF {
		t.Fatalf("cache error = %v, want EOF", err)
	}
	mb, err := r.ReadMultiBuffer()
	defer buf.ReleaseMulti(mb)
	if mb.Len() != 17 || err != nil {
		t.Fatalf("cached final data = %d, %v", mb.Len(), err)
	}
}

func BenchmarkCachedLinkManager(b *testing.B) {
	d := &DefaultDispatcher{}
	d.getLinkManager("user")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.getLinkManager("user")
	}
}
