package dispatcher

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
)

type sniffEOFReader struct {
	chunks []string
	reads  int
}

func (r *sniffEOFReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	r.reads++
	if len(r.chunks) == 0 {
		return nil, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	b := buf.New()
	_, _ = b.Write([]byte(chunk))
	if len(r.chunks) == 0 {
		return buf.MultiBuffer{b}, io.EOF
	}
	return buf.MultiBuffer{b}, nil
}

func (r *sniffEOFReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return r.ReadMultiBuffer()
}

func TestSnifferHTTPWithFinalEOF(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
	}{
		{"first read", []string{"GET / HTTP/1.1\r\nHost: Example.COM\r\n\r\n"}},
		{"split request", []string{"GET / HTTP/1.1\r\n", "Host: Example.COM\r\n\r\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), core.XrayKey(1), &core.Instance{})
			source := &sniffEOFReader{chunks: tc.chunks}
			reader := &cachedReader{reader: source}
			defer reader.Interrupt()
			result, err := sniffer(ctx, reader, false, net.Network_TCP)
			if err != nil {
				t.Fatalf("sniff final HTTP data: %v", err)
			}
			if result == nil || result.Protocol() != "http1" || result.Domain() != "example.com" {
				t.Fatalf("unexpected HTTP sniff result: %#v", result)
			}
			if source.reads != len(tc.chunks) {
				t.Fatalf("sniffer performed %d reads, want %d", source.reads, len(tc.chunks))
			}
			mb, err := reader.ReadMultiBuffer()
			defer buf.ReleaseMulti(mb)
			payload := make([]byte, mb.Len())
			mb.Copy(payload)
			if err != nil || string(payload) != strings.Join(tc.chunks, "") {
				t.Fatalf("replayed HTTP data = %q, %v", payload, err)
			}
			mb, err = reader.ReadMultiBuffer()
			defer buf.ReleaseMulti(mb)
			if !mb.IsEmpty() || err != io.EOF {
				t.Fatalf("read after replay = %d bytes, %v; want EOF", mb.Len(), err)
			}
		})
	}
}

func TestSnifferIncompleteFinalEOFDoesNotRetry(t *testing.T) {
	ctx := context.WithValue(context.Background(), core.XrayKey(1), &core.Instance{})
	source := &sniffEOFReader{chunks: []string{"G"}}
	reader := &cachedReader{reader: source}
	defer reader.Interrupt()
	result, err := sniffer(ctx, reader, false, net.Network_TCP)
	if result != nil || err != io.EOF {
		t.Fatalf("incomplete final data result = %#v, %v; want EOF", result, err)
	}
	if source.reads != 1 {
		t.Fatalf("sniffer retried after EOF: %d reads", source.reads)
	}
}
