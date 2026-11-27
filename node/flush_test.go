package node

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
)

// fakeCore keeps per-uid pending traffic the way the real cores do:
// GetUserTrafficSlice(reset) takes it, ReturnUserTraffic adds it back.
type fakeCore struct {
	vCore.Core
	mu      sync.Mutex
	pending map[int][2]int64
	delNode int
}

func (f *fakeCore) add(uid int, up, down int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pending[uid]
	f.pending[uid] = [2]int64{p[0] + up, p[1] + down}
}

func (f *fakeCore) GetUserTrafficSlice(_ string, reset bool) ([]panel.UserTraffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []panel.UserTraffic
	for uid, p := range f.pending {
		if p[0]+p[1] > 0 {
			out = append(out, panel.UserTraffic{UID: uid, Upload: p[0], Download: p[1]})
		}
	}
	if reset {
		f.pending = map[int][2]int64{}
	}
	return out, nil
}

func (f *fakeCore) ReturnUserTraffic(_ string, traffic []panel.UserTraffic) error {
	for _, t := range traffic {
		f.add(t.UID, t.Upload, t.Download)
	}
	return nil
}

func (f *fakeCore) DelNode(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delNode++
	return nil
}

type fakePanel struct {
	mu     sync.Mutex
	status int
	pushes []map[string][]int64
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/push") {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var m map[string][]int64
	_ = json.Unmarshal(body, &m)
	p.mu.Lock()
	p.pushes = append(p.pushes, m)
	status := p.status
	p.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"data":true}`))
}

func (p *fakePanel) got() []map[string][]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string][]int64(nil), p.pushes...)
}

func newFlushController(t *testing.T, fp *fakePanel) (*Controller, *fakeCore) {
	t.Helper()
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, NodeID: 1, Key: "k", NodeType: "vless", Timeout: 2})
	if err != nil {
		t.Fatal(err)
	}
	core := &fakeCore{pending: map[int][2]int64{}}
	c := NewController(core, api, &conf.NodeConfig{})
	c.tag = "flush-test"
	return c, core
}

// Traffic that accrued after the last periodic push used to be dropped by
// DelNode on every restart and config reload.
func TestCloseFlushesUnreportedTraffic(t *testing.T) {
	fp := &fakePanel{}
	c, core := newFlushController(t, fp)
	core.add(7, 100, 2000)

	n := &Node{controllers: []*Controller{c}}
	n.Close()

	pushes := fp.got()
	if len(pushes) != 1 {
		t.Fatalf("pushes = %d, want 1 (Node.Close then Controller.Close must flush once)", len(pushes))
	}
	if got := pushes[0]["7"]; len(got) != 2 || got[0] != 100 || got[1] != 2000 {
		t.Fatalf("pushed %v, want [100 2000]", pushes[0])
	}
	if core.delNode != 1 {
		t.Fatalf("DelNode calls = %d, want 1", core.delNode)
	}
}

func TestCloseWithNothingPendingDoesNotPush(t *testing.T) {
	fp := &fakePanel{}
	c, _ := newFlushController(t, fp)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(fp.got()); n != 0 {
		t.Fatalf("pushes = %d, want 0", n)
	}
}

// A failing panel must not stop the node from closing.
func TestCloseContinuesWhenFlushFails(t *testing.T) {
	fp := &fakePanel{status: http.StatusBadGateway}
	c, core := newFlushController(t, fp)
	core.add(7, 1, 2)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(fp.got()); n != 1 {
		t.Fatalf("pushes = %d, want 1", n)
	}
	if core.delNode != 1 {
		t.Fatalf("DelNode calls = %d, want 1", core.delNode)
	}
}

// The flush waits for a periodic push that is still in flight, so traffic a
// failed periodic push adds back is included in the final push.
func TestCloseWaitsForInFlightPush(t *testing.T) {
	fp := &fakePanel{}
	c, core := newFlushController(t, fp)

	c.reportMu.Lock() // a periodic push is in flight
	done := make(chan struct{})
	go func() {
		_ = c.Close()
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("Close did not wait for the in-flight push")
	default:
	}
	core.add(7, 5, 50) // the in-flight push failed and added its traffic back
	c.reportMu.Unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish")
	}
	pushes := fp.got()
	if len(pushes) != 1 || pushes[0]["7"][1] != 50 {
		t.Fatalf("pushes = %v, want one push carrying the backfilled traffic", pushes)
	}
}
