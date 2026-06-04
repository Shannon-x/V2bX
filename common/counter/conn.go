package counter

import (
	"io"
	"net"

	"github.com/sagernet/sing/common/bufio"

	"github.com/sagernet/sing/common/buf"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
)

// ConnCounter wraps a sing net.Conn so byte counts are accumulated into the
// per-user TrafficStorage.
//
// W6 fix-up: previously Read/Write only incremented the atomic counters and
// the per-tag TrafficCounter.IterateDirty (used by GetUserTrafficSlice) never
// saw these users — sing traffic was effectively unreportable. The wrapper
// now also holds a pointer back to its parent TrafficCounter plus the uuid
// so it can call MarkDirty on every byte movement (cheap: a single
// sync.Map.Store on an already-present key amortizes to one atomic op).
type ConnCounter struct {
	network.ExtendedConn
	storage   *TrafficStorage
	parent    *TrafficCounter
	uuid      string
	readFunc  network.CountFunc
	writeFunc network.CountFunc
}

// NewConnCounter constructs a sing.net.Conn that counts traffic into the
// (uuid) slot of the given TrafficCounter. Both parent and uuid are
// required for IterateDirty-based reporting to see this user; passing them
// as separate args keeps TrafficStorage allocation-free.
func NewConnCounter(conn net.Conn, parent *TrafficCounter, uuid string) net.Conn {
	storage := parent.GetCounter(uuid)
	c := &ConnCounter{
		ExtendedConn: bufio.NewExtendedConn(conn),
		storage:      storage,
		parent:       parent,
		uuid:         uuid,
	}
	c.readFunc = func(n int64) {
		storage.UpCounter.Add(n)
		parent.MarkDirty(uuid)
	}
	c.writeFunc = func(n int64) {
		storage.DownCounter.Add(n)
		parent.MarkDirty(uuid)
	}
	return c
}

func (c *ConnCounter) markDirty() {
	if c.parent != nil {
		c.parent.MarkDirty(c.uuid)
	}
}

func (c *ConnCounter) Read(b []byte) (n int, err error) {
	n, err = c.ExtendedConn.Read(b)
	if n > 0 {
		c.storage.UpCounter.Add(int64(n))
		c.markDirty()
	}
	return
}

func (c *ConnCounter) Write(b []byte) (n int, err error) {
	n, err = c.ExtendedConn.Write(b)
	if n > 0 {
		c.storage.DownCounter.Add(int64(n))
		c.markDirty()
	}
	return
}

func (c *ConnCounter) ReadBuffer(buffer *buf.Buffer) error {
	err := c.ExtendedConn.ReadBuffer(buffer)
	if err != nil {
		return err
	}
	if buffer.Len() > 0 {
		c.storage.UpCounter.Add(int64(buffer.Len()))
		c.markDirty()
	}
	return nil
}

func (c *ConnCounter) WriteBuffer(buffer *buf.Buffer) error {
	dataLen := int64(buffer.Len())
	err := c.ExtendedConn.WriteBuffer(buffer)
	if err != nil {
		return err
	}
	if dataLen > 0 {
		c.storage.DownCounter.Add(dataLen)
		c.markDirty()
	}
	return nil
}

func (c *ConnCounter) UnwrapReader() (io.Reader, []network.CountFunc) {
	return c.ExtendedConn, []network.CountFunc{
		c.readFunc,
	}
}

func (c *ConnCounter) UnwrapWriter() (io.Writer, []network.CountFunc) {
	return c.ExtendedConn, []network.CountFunc{
		c.writeFunc,
	}
}

func (c *ConnCounter) Upstream() any {
	return c.ExtendedConn
}

// PacketConnCounter mirrors ConnCounter for UDP. Same MarkDirty contract.
type PacketConnCounter struct {
	network.PacketConn
	storage   *TrafficStorage
	parent    *TrafficCounter
	uuid      string
	readFunc  network.CountFunc
	writeFunc network.CountFunc
}

// NewPacketConnCounter — see NewConnCounter doc; the parent+uuid plumbing
// is identical, just for sing.network.PacketConn instead of net.Conn.
func NewPacketConnCounter(conn network.PacketConn, parent *TrafficCounter, uuid string) network.PacketConn {
	storage := parent.GetCounter(uuid)
	p := &PacketConnCounter{
		PacketConn: conn,
		storage:    storage,
		parent:     parent,
		uuid:       uuid,
	}
	p.readFunc = func(n int64) {
		storage.UpCounter.Add(n)
		parent.MarkDirty(uuid)
	}
	p.writeFunc = func(n int64) {
		storage.DownCounter.Add(n)
		parent.MarkDirty(uuid)
	}
	return p
}

func (p *PacketConnCounter) markDirty() {
	if p.parent != nil {
		p.parent.MarkDirty(p.uuid)
	}
}

func (p *PacketConnCounter) ReadPacket(buff *buf.Buffer) (destination M.Socksaddr, err error) {
	destination, err = p.PacketConn.ReadPacket(buff)
	if err != nil {
		return
	}
	if buff.Len() > 0 {
		p.storage.UpCounter.Add(int64(buff.Len()))
		p.markDirty()
	}
	return
}

func (p *PacketConnCounter) WritePacket(buff *buf.Buffer, destination M.Socksaddr) (err error) {
	n := buff.Len()
	err = p.PacketConn.WritePacket(buff, destination)
	if err != nil {
		return
	}
	if n > 0 {
		p.storage.DownCounter.Add(int64(n))
		p.markDirty()
	}
	return
}

func (p *PacketConnCounter) UnwrapPacketReader() (network.PacketReader, []network.CountFunc) {
	return p.PacketConn, []network.CountFunc{
		p.readFunc,
	}
}

func (p *PacketConnCounter) UnwrapPacketWriter() (network.PacketWriter, []network.CountFunc) {
	return p.PacketConn, []network.CountFunc{
		p.writeFunc,
	}
}

func (p *PacketConnCounter) Upstream() any {
	return p.PacketConn
}
