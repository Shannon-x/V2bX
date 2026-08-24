package sing

import (
	"io"
	"testing"

	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type fakePacketConn struct {
	N.PacketConn
	packets [][]byte
	i       int
}

func (c *fakePacketConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	if c.i >= len(c.packets) {
		return M.Socksaddr{}, io.EOF
	}
	p := c.packets[c.i]
	c.i++
	b.Reset()
	if _, err := b.Write(p); err != nil {
		return M.Socksaddr{}, err
	}
	return M.ParseSocksaddr("1.2.3.4:443"), nil
}

var (
	singDHT = []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe")
	singDNS = []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00}
)

// BT 包应当被静默丢弃并继续读下一个，而不是把整条 UDP 会话掐掉——
// 同一个会话里还跑着用户的 DNS 和 QUIC。
func TestBTPacketFilterSkipsBittorrentAndKeepsSession(t *testing.T) {
	src := &fakePacketConn{packets: [][]byte{singDHT, singDHT, singDNS, singDHT, singDNS}}
	hits := 0
	f := newBTPacketFilter(src, func() { hits++ })

	b := buf.New()
	defer b.Release()

	for i := 0; i < 2; i++ {
		if _, err := f.ReadPacket(b); err != nil {
			t.Fatalf("第 %d 次读取失败: %v", i+1, err)
		}
		if string(b.Bytes()) != string(singDNS) {
			t.Fatalf("第 %d 次读到的不是 DNS 包，实际 %q", i+1, b.Bytes())
		}
	}
	if _, err := f.ReadPacket(b); err != io.EOF {
		t.Fatalf("期望 EOF，实际 %v", err)
	}
	if hits != 3 {
		t.Fatalf("应丢弃 3 个 BT 包，实际 %d", hits)
	}
}

// 不实现 ReaderWithUpstream 是刻意的：一旦实现，sing 的 UnwrapCountPacketReader
// 会连同计数器一起把这层剥掉，过滤就形同虚设。
func TestBTPacketFilterIsNotUnwrappable(t *testing.T) {
	f := newBTPacketFilter(&fakePacketConn{}, nil)
	if u, ok := f.(N.ReaderWithUpstream); ok && u.ReaderReplaceable() {
		t.Fatal("过滤层声明了可替换，会被 sing 的 unwrap 机制绕过")
	}
	if _, ok := f.(N.PacketReadCounter); ok {
		t.Fatal("过滤层不应被识别为计数器，否则会被 UnwrapCountPacketReader 剥掉")
	}
	if N.UnwrapPacketReader(f) != N.PacketReader(f) {
		t.Fatal("UnwrapPacketReader 把过滤层剥掉了")
	}
}

type fakeWaitConn struct {
	fakePacketConn
	initialized bool
}

func (c *fakeWaitConn) CreateReadWaiter() (N.PacketReadWaiter, bool) { return c, true }

func (c *fakeWaitConn) InitializeReadWaiter(N.ReadWaitOptions) bool {
	c.initialized = true
	return false
}

func (c *fakeWaitConn) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	b := buf.New()
	dest, err := c.ReadPacket(b)
	if err != nil {
		b.Release()
		return nil, dest, err
	}
	return b, dest, nil
}

// 关键性能保证：包装之后 sing 的零拷贝快路径必须还在。
// 如果这条测试挂了，说明 CopyPacket 会退化成每包一次缓冲区分配的慢路径。
func TestBTPacketFilterPreservesReadWaiterFastPath(t *testing.T) {
	src := &fakeWaitConn{fakePacketConn: fakePacketConn{packets: [][]byte{singDHT, singDNS}}}
	hits := 0
	f := newBTPacketFilter(src, func() { hits++ })

	waiter, created := bufio.CreatePacketReadWaiter(f)
	if !created {
		t.Fatal("包装后拿不到 PacketReadWaiter，零拷贝快路径丢失")
	}
	waiter.InitializeReadWaiter(N.ReadWaitOptions{})
	if !src.initialized {
		t.Fatal("InitializeReadWaiter 没有透传到底层 conn")
	}

	b, _, err := waiter.WaitReadPacket()
	if err != nil {
		t.Fatalf("WaitReadPacket 失败: %v", err)
	}
	if string(b.Bytes()) != string(singDNS) {
		t.Fatalf("BT 包应在快路径上被跳过，实际读到 %q", b.Bytes())
	}
	b.Release()
	if hits != 1 {
		t.Fatalf("应丢弃 1 个 BT 包，实际 %d", hits)
	}
}

// 底层不支持快路径时不能凭空造一个，否则会走进没有实现的分支。
func TestBTPacketFilterNoFakeReadWaiter(t *testing.T) {
	f := newBTPacketFilter(&fakePacketConn{}, nil)
	if _, created := bufio.CreatePacketReadWaiter(f); created {
		t.Fatal("底层不支持 ReadWaiter 时不应对外声称支持")
	}
}
