package dispatcher

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
)

type fakeReader struct {
	batches []buf.MultiBuffer
	i       int
}

func (r *fakeReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.i >= len(r.batches) {
		return nil, io.EOF
	}
	mb := r.batches[r.i]
	r.i++
	return mb, nil
}

type fakeTimeoutReader struct{ fakeReader }

func (r *fakeTimeoutReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return r.ReadMultiBuffer()
}

func mkbuf(payload []byte) *buf.Buffer {
	b := buf.New()
	b.Write(payload)
	return b
}

var (
	dhtPacket   = []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe")
	dnsPacket   = []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 'w', 'w', 'w'}
	quicPacket  = append([]byte{0xC3, 0x00, 0x00, 0x00, 0x01}, make([]byte, 40)...)
	emptyPacket = []byte{}
)

func TestBTUDPFilterDropsOnlyBittorrent(t *testing.T) {
	src := &fakeReader{batches: []buf.MultiBuffer{
		{mkbuf(dnsPacket), mkbuf(dhtPacket), mkbuf(quicPacket)},
		{mkbuf(dhtPacket)},
		{mkbuf(dnsPacket)},
	}}
	hits := 0
	r := newBTUDPFilterReader(src, func() { hits++ })

	// 第一批：DHT 被摘掉，DNS 与 QUIC 原样保留
	mb, err := r.ReadMultiBuffer()
	if err != nil {
		t.Fatalf("第一批读取失败: %v", err)
	}
	if len(mb) != 2 {
		t.Fatalf("第一批应保留 2 个包，实际 %d", len(mb))
	}
	buf.ReleaseMulti(mb)

	// 第二批：整批都是 DHT，全部丢弃后返回空批（不是错误）
	mb, err = r.ReadMultiBuffer()
	if err != nil {
		t.Fatalf("第二批读取失败: %v", err)
	}
	if len(mb) != 0 {
		t.Fatalf("第二批应全部丢弃，实际保留 %d", len(mb))
	}

	// 第三批：普通流量不受影响，会话没有被掐断
	mb, err = r.ReadMultiBuffer()
	if err != nil {
		t.Fatalf("第三批读取失败: %v", err)
	}
	if len(mb) != 1 {
		t.Fatalf("第三批应保留 1 个包，实际 %d", len(mb))
	}
	buf.ReleaseMulti(mb)

	if _, err = r.ReadMultiBuffer(); !errors.Is(err, io.EOF) {
		t.Fatalf("期望 EOF，实际 %v", err)
	}
	if hits != 2 {
		t.Fatalf("命中回调应触发 2 次，实际 %d", hits)
	}
}

// 内层支持超时读取时，包装后必须仍然满足 buf.TimeoutReader，
// 否则 vless / shadowsocks_2022 出站的断言会失败并退化为无超时路径。
func TestBTUDPFilterPreservesTimeoutReader(t *testing.T) {
	src := &fakeTimeoutReader{fakeReader{batches: []buf.MultiBuffer{
		{mkbuf(dhtPacket), mkbuf(dnsPacket)},
	}}}
	r := newBTUDPFilterReader(src, nil)

	tr, ok := r.(buf.TimeoutReader)
	if !ok {
		t.Fatal("包装后丢失了 buf.TimeoutReader 接口")
	}
	mb, err := tr.ReadMultiBufferTimeout(time.Second)
	if err != nil {
		t.Fatalf("超时读取失败: %v", err)
	}
	if len(mb) != 1 {
		t.Fatalf("超时路径也应过滤掉 DHT，实际保留 %d", len(mb))
	}
	buf.ReleaseMulti(mb)
}

// 内层不支持超时读取时，不应凭空伪造出一个 TimeoutReader。
func TestBTUDPFilterDoesNotFakeTimeoutReader(t *testing.T) {
	r := newBTUDPFilterReader(&fakeReader{}, nil)
	if _, ok := r.(buf.TimeoutReader); ok {
		t.Fatal("内层不支持超时读取，包装后不应对外声称支持")
	}
}

func TestBTUDPFilterHandlesEmptyBuffers(t *testing.T) {
	src := &fakeReader{batches: []buf.MultiBuffer{
		{mkbuf(emptyPacket), mkbuf(dnsPacket)},
	}}
	r := newBTUDPFilterReader(src, nil)
	mb, err := r.ReadMultiBuffer()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(mb) != 2 {
		t.Fatalf("空包不应被当成 BT 丢掉，期望保留 2 个，实际 %d", len(mb))
	}
	buf.ReleaseMulti(mb)
}
