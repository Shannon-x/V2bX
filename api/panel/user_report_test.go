package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/InazumaV/V2bX/conf"
)

func TestReportNodeOnlineUsersReturnsAliveDeltaAndSendsEmptyObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/UniProxy/alive" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		var body map[int][]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body == nil || len(body) != 0 {
			t.Fatalf("empty full snapshot must be a non-nil empty object: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":true,"alive":{"42":1,"43":0}}`))
	}))
	defer server.Close()

	client, err := New(&conf.ApiConfig{APIHost: server.URL, Key: "token", NodeType: "vless", NodeID: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	empty := make(map[int][]string)
	delta, err := client.ReportNodeOnlineUsersWithDeltaCtx(context.Background(), &empty)
	if err != nil {
		t.Fatal(err)
	}
	if delta[42] != 1 || delta[43] != 0 {
		t.Fatalf("unexpected alive delta: %#v", delta)
	}
}
