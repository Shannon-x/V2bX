package dispatcher

import (
	"bytes"
	"context"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/proxy"
)

type countingWriter struct {
	inner buf.Writer
	n     atomic.Int64
}

func (w *countingWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.n.Add(int64(mb.Len()))
	return w.inner.WriteMultiBuffer(mb)
}

// tcpPair returns both ends of a loopback TCP connection.
func tcpPair(t *testing.T) (dialed, accepted *net.TCPConn) {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acc := make(chan *net.TCPConn, 1)
	go func() {
		c, err := ln.AcceptTCP()
		if err != nil {
			acc <- nil
			return
		}
		acc <- c
	}()
	d, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	a := <-acc
	if a == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { d.Close(); a.Close() })
	return d, a
}

// TestSpliceDownlinkIsBilled drives xray's own splice copy (the path freedom
// takes for an XTLS Vision downlink once both sides allow splice) with the
// writer the dispatcher installs, and checks that every byte reaches the
// user's counter. Before the dispatcher used xray's SizeStatWriter type,
// xray's type assertion failed and this counter stayed at zero.
func TestSpliceDownlinkIsBilled(t *testing.T) {
	const size = 8 << 20
	payload := bytes.Repeat([]byte("v2bx-splice-billing:"), size/20)

	// target <-> freedom's outbound connection
	freedomConn, targetConn := tcpPair(t)
	// client <-> inbound connection (session.Inbound.Conn)
	clientConn, inboundConn := tcpPair(t)

	go func() {
		_, _ = targetConn.Write(payload)
		_ = targetConn.CloseWrite()
	}()
	received := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(io.Discard, clientConn)
		received <- n
	}()

	tc := counter.NewTrafficCounter()
	ts := tc.GetCounter("tag|user")
	inner := &countingWriter{inner: buf.NewWriter(inboundConn)}
	writer := newUserStatWriter(inner, &ts.DownCounter, tc, "tag|user")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = session.ContextWithInbound(ctx, &session.Inbound{CanSpliceCopy: 1})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{CanSpliceCopy: 1}})
	timer := signal.CancelAfterInactivity(ctx, cancel, time.Minute)

	if err := proxy.CopyRawConnIfExist(ctx, freedomConn, inboundConn, writer, timer, nil); err != nil {
		t.Fatalf("CopyRawConnIfExist: %v", err)
	}
	_ = inboundConn.CloseWrite()

	select {
	case n := <-received:
		if n != int64(len(payload)) {
			t.Fatalf("client received %d bytes, want %d", n, len(payload))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("client did not finish reading")
	}
	if runtime.GOOS == "linux" && inner.n.Load() != 0 {
		t.Fatalf("splice path was not taken: %d bytes went through the pipe writer", inner.n.Load())
	}
	if got := ts.DownCounter.Load(); got != int64(len(payload)) {
		t.Fatalf("billed download = %d, want %d", got, len(payload))
	}

	visited := 0
	tc.IterateDirty(true, func(uuid string, _ *counter.TrafficStorage) bool {
		if uuid == "tag|user" {
			visited++
		}
		return true
	})
	if visited != 1 {
		t.Fatal("spliced bytes did not mark the user dirty; the next report would skip them")
	}
}

// TestUserStatWriterCountsPipeWrites covers the ordinary (non-splice) path.
func TestUserStatWriterCountsPipeWrites(t *testing.T) {
	tc := counter.NewTrafficCounter()
	ts := tc.GetCounter("tag|user")
	w := newUserStatWriter(buf.Discard, &ts.UpCounter, tc, "tag|user")

	b := buf.New()
	b.Write([]byte("hello"))
	if err := w.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMultiBuffer(nil); err != nil {
		t.Fatal(err)
	}
	if got := ts.UpCounter.Load(); got != 5 {
		t.Fatalf("billed upload = %d, want 5", got)
	}

	visited := 0
	tc.IterateDirty(true, func(string, *counter.TrafficStorage) bool { visited++; return true })
	if visited != 1 {
		t.Fatalf("dirty users = %d, want 1", visited)
	}
	// An empty write must not mark the user dirty again.
	_ = w.WriteMultiBuffer(nil)
	visited = 0
	tc.IterateDirty(true, func(string, *counter.TrafficStorage) bool { visited++; return true })
	if visited != 0 {
		t.Fatalf("empty write marked the user dirty")
	}
}
