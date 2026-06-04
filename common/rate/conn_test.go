package rate

import (
	"io"
	"net"
	"testing"
	"time"
)

// fixedRead is a net.Conn that returns at most replySize bytes per Read,
// regardless of the buffer length. Lets us simulate a slow peer to expose
// the pre-Read over-debit hazard.
type fixedRead struct {
	net.Conn
	replySize int
	payload   []byte
	pos       int
}

func (f *fixedRead) Read(b []byte) (int, error) {
	if f.pos >= len(f.payload) {
		return 0, io.EOF
	}
	limit := f.replySize
	if limit > len(b) {
		limit = len(b)
	}
	remain := len(f.payload) - f.pos
	if limit > remain {
		limit = remain
	}
	copy(b, f.payload[f.pos:f.pos+limit])
	f.pos += limit
	return limit, nil
}

// TestReadShortPeerNotOverDebited pins the W6 follow-up: a peer returning
// 4 KB into a 32 KB buffer must NOT cost 32 KB of tokens.
//
// Before the post-Read accounting fix, Wait(32 KB) was charged on a
// 100 KB/s limiter (0.32 s) before Read() even ran; the peer then handed
// back 4 KB. Tokens "spent": 32 KB. Bytes delivered: 4 KB. Effective
// throughput: ~1/8 of configured rate.
func TestReadShortPeerNotOverDebited(t *testing.T) {
	const rate = 100 * 1024 // 100 KB/s
	bucket := NewDynamicBucket(rate)

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Stuff the pipe with a small write so client Read returns short.
	go func() { _, _ = server.Write([]byte("hello world!")) }()

	rc := NewConnRateLimiter(client, bucket)

	// Bucket starts full (capacity tokens). Drain it so we exercise the
	// post-Read Wait path, not the initial-credit fast path.
	bucket.Get().TakeAvailable(rate)

	start := time.Now()
	buf := make([]byte, 32*1024)
	n, err := rc.Read(buf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != len("hello world!") {
		t.Fatalf("got n=%d, want %d", n, len("hello world!"))
	}

	// Time-charge expected: ~n bytes / rate = 12 / 100 KB ≈ 0.1 ms.
	// Old pre-Read would have charged the full buffer = 32 KB / 100 KB/s
	// ≈ 320 ms. Allow generous slack for scheduler jitter — anything
	// under 80 ms confirms we are NOT over-debiting.
	if elapsed > 80*time.Millisecond {
		t.Fatalf("read short-payload took %v, expected < 80ms (would indicate over-debit on buffer length, not actual n)", elapsed)
	}
}

// TestReadIdleConnectionCostsZero pins that a Read that returns 0 bytes
// (e.g. immediate EOF on a closed peer) does NOT consume tokens.
func TestReadIdleConnectionCostsZero(t *testing.T) {
	const rate = 1024 // tiny rate so over-debit would be VERY slow
	bucket := NewDynamicBucket(rate)

	server, client := net.Pipe()
	server.Close() // closed → client.Read returns 0, EOF
	defer client.Close()

	rc := NewConnRateLimiter(client, bucket)

	// Drain the bucket so a buggy implementation that pre-charged len(b)
	// would block for many seconds.
	bucket.Get().TakeAvailable(rate)

	start := time.Now()
	buf := make([]byte, 64*1024)
	n, _ := rc.Read(buf)
	elapsed := time.Since(start)

	if n != 0 {
		t.Fatalf("expected n=0 on closed peer, got %d", n)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("idle Read took %v — pre-Read Wait was charging on len(b) instead of n", elapsed)
	}
}

// TestReadChunkCapBoundsLatency pins that a single Read on a low-rate
// bucket with an oversized buffer doesn't induce a multi-second wait.
//
// Without the chunkSizeFor cap: 256 KB buffer on a 1 KB/s rate → single
// Read blocks for 256 seconds.
func TestReadChunkCapBoundsLatency(t *testing.T) {
	const rate = 1024 // 1 KB/s; capacity / 10 = 102 B → floored to 1 KB
	bucket := NewDynamicBucket(rate)
	// Drain so we don't ride initial-credit.
	bucket.Get().TakeAvailable(rate)

	payload := make([]byte, 256*1024) // 256 KB available
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	_, client := net.Pipe()
	defer client.Close()
	fake := &fixedRead{Conn: client, replySize: 1024, payload: payload}

	rc := NewConnRateLimiter(fake, bucket)

	start := time.Now()
	buf := make([]byte, 256*1024)
	n, err := rc.Read(buf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// We should get up to chunk_size bytes back (1 KB floor). Definitely
	// not the whole 256 KB in one call.
	if n > 64*1024 {
		t.Fatalf("single Read returned %d bytes, expected ≤ 64 KB chunk cap", n)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("single Read took %v — chunk cap not effective (would be 256s without)", elapsed)
	}
}
