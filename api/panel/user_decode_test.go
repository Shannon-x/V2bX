package panel

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/InazumaV/V2bX/conf"
	"github.com/vmihailenco/msgpack/v5"
)

func TestUserSnapshotDecoders(t *testing.T) {
	for _, raw := range []string{`{}`, `{"users":null}`, `{"users":[null]}`, `{"users":[]`, `{"users":[]} {}`, `{"users":[],"users":[]}`, `{"nested":{"users":[]}}`} {
		if _, err := decodeJSONUsers(strings.NewReader(raw), 10); err == nil {
			t.Errorf("accepted malformed snapshot %s", raw)
		}
	}
	for _, raw := range []string{`{"users":[]}`, `{"other":{"nested":true},"users":[],"tail":1}`} {
		users, err := decodeJSONUsers(strings.NewReader(raw), 10)
		if err != nil || users == nil || len(users) != 0 {
			t.Fatalf("empty JSON snapshot: %v %v", users, err)
		}
	}
	for _, value := range []any{map[string]any{}, map[string]any{"users": nil}, map[string]any{"users": []any{nil}}, map[string]any{"users": []any{[]any{}}}} {
		data, _ := msgpack.Marshal(value)
		if _, err := decodeMsgpackUsers(bytes.NewReader(data), 10); err == nil {
			t.Fatalf("accepted msgpack %v", value)
		}
	}
	data, _ := msgpack.Marshal(map[string]any{"users": []UserInfo{}})
	users, err := decodeMsgpackUsers(bytes.NewReader(data), 10)
	if err != nil || users == nil {
		t.Fatalf("empty msgpack snapshot: %v %v", users, err)
	}
	data, _ = msgpack.Marshal(map[string]any{"users": []UserInfo{{Id: 1}, {Id: 2}}})
	if _, err := decodeMsgpackUsers(bytes.NewReader(data), 1); err == nil {
		t.Fatal("msgpack count limit ignored")
	}
	if _, err := decodeJSONUsers(strings.NewReader(`{"users":[{"id":1},{"id":2}]}`), 1); err == nil {
		t.Fatal("JSON count limit ignored")
	}
}

func TestUserSnapshotETagAndEmptyVsUnchanged(t *testing.T) {
	mode := "bad"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "bad" {
			if got := r.Header.Get("If-None-Match"); got != "" {
				t.Errorf("bad ETag committed: %s", got)
			}
			w.Header().Set("ETag", "bad")
			fmt.Fprint(w, `{"users":[]`)
		} else if mode == "empty" {
			w.Header().Set("ETag", "empty")
			fmt.Fprint(w, `{"users":[]}`)
		} else {
			if got := r.Header.Get("If-None-Match"); got != "empty" {
				t.Errorf("missing ETag: %s", got)
			}
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	defer server.Close()
	c, err := New(&conf.ApiConfig{APIHost: server.URL, NodeType: "vless"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for range 2 {
		if _, err := c.GetUserListCtx(context.Background()); err == nil {
			t.Fatal("accepted truncated snapshot")
		}
	}
	mode = "empty"
	if u, err := c.GetUserList(); err != nil || u == nil {
		t.Fatalf("empty: %v %v", u, err)
	}
	mode = "unchanged"
	if u, err := c.GetUserList(); err != nil || u != nil {
		t.Fatalf("unchanged: %v %v", u, err)
	}
}

func TestPanelResponseLimitAfterDecompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		fmt.Fprint(z, `{"users":[],"padding":"`+strings.Repeat("x", 4096)+`"}`)
		z.Close()
	}))
	defer server.Close()
	c, err := New(&conf.ApiConfig{APIHost: server.URL, NodeType: "vless", MaxResponseBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.GetUserList(); err == nil {
		t.Fatal("oversized decompressed response accepted")
	}
	if _, err := c.GetNodeInfo(); err == nil {
		t.Fatal("buffered response limit ignored")
	}
}
