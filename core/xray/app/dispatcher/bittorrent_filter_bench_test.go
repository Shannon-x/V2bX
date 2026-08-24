package dispatcher

import (
	"crypto/rand"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

// benchReader 反复吐出同一批包，用来量包装层本身的净开销。
// 复用同一块 MultiBuffer 底层数组，避免每次调用都新建切片——
// 那部分分配会随逃逸分析结果在对照组/实验组之间产生差异，掩盖真实开销。
type benchReader struct {
	payloads [][]byte
	pool     []*buf.Buffer
	scratch  buf.MultiBuffer
}

func (r *benchReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb := r.scratch[:0]
	for i, p := range r.payloads {
		b := r.pool[i]
		b.Clear()
		b.Write(p)
		mb = append(mb, b)
	}
	return mb, nil
}

func newBenchReader(payloads [][]byte) *benchReader {
	pool := make([]*buf.Buffer, len(payloads))
	for i := range pool {
		pool[i] = buf.New()
	}
	return &benchReader{
		payloads: payloads,
		pool:     pool,
		scratch:  make(buf.MultiBuffer, 0, len(payloads)),
	}
}

func benchUDPPayloads() [][]byte {
	quic := make([]byte, 1350)
	rand.Read(quic)
	quic[0] = 0xC3
	dns := make([]byte, 64)
	rand.Read(dns)
	dns[2] = 0x01
	return [][]byte{quic, dns, quic, quic}
}

// 对照组：不套过滤层，直接读。
func BenchmarkUDPReadWithoutFilter(b *testing.B) {
	r := newBenchReader(benchUDPPayloads())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mb, _ := r.ReadMultiBuffer()
		_ = mb
	}
}

// 实验组：套上逐包 BT 过滤。两者之差就是这一层的代价。
func BenchmarkUDPReadWithBTFilter(b *testing.B) {
	r := newBenchReader(benchUDPPayloads())
	f := newBTUDPFilterReader(r, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mb, _ := f.ReadMultiBuffer()
		_ = mb
	}
}
