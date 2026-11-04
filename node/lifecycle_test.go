package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/task"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/limiter"
)

type lifecycleCore struct {
	vCore.Core
	addNode     func(string, *panel.NodeInfo) error
	addUsers    func(*vCore.AddUsersParams) (int, error)
	delNode     func(string) error
	updateUsers func(*vCore.UpdateUsersParams) error
}

func (f *lifecycleCore) AddNode(tag string, n *panel.NodeInfo, _ *conf.Options) error {
	if f.addNode != nil {
		return f.addNode(tag, n)
	}
	return nil
}
func (f *lifecycleCore) AddUsers(p *vCore.AddUsersParams) (int, error) {
	if f.addUsers != nil {
		return f.addUsers(p)
	}
	return len(p.Users), nil
}
func (f *lifecycleCore) DelNode(tag string) error {
	if f.delNode != nil {
		return f.delNode(tag)
	}
	return nil
}
func (f *lifecycleCore) AddNodeCustomOutbounds(*panel.NodeInfo, *conf.Options) error       { return nil }
func (f *lifecycleCore) UpdateNodeReportMinTraffic(string, *panel.NodeInfo, *conf.Options) {}
func (f *lifecycleCore) UpdateDNS(string, *panel.NodeInfo) error                           { return nil }
func (f *lifecycleCore) UpdateUsers(p *vCore.UpdateUsersParams) error                      { return f.updateUsers(p) }

func TestRebuildRollbackRestoresAppliedConfigAndUsers(t *testing.T) {
	for _, phase := range []string{"node", "users"} {
		t.Run(phase, func(t *testing.T) {
			old := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 1000}}
			next := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 2000}}
			oldUsers := []panel.UserInfo{{Id: 1, Uuid: "old"}}
			newUsers := []panel.UserInfo{{Id: 2, Uuid: "new"}}
			live := old
			liveUsers := oldUsers
			core := &lifecycleCore{}
			core.delNode = func(string) error { live = nil; liveUsers = nil; return nil }
			core.addNode = func(_ string, n *panel.NodeInfo) error {
				if phase == "node" && n == next {
					return errors.New("candidate failed")
				}
				live = n
				return nil
			}
			core.addUsers = func(p *vCore.AddUsersParams) (int, error) {
				if phase == "users" && p.NodeInfo == next {
					return 0, errors.New("users failed")
				}
				liveUsers = p.Users
				return len(p.Users), nil
			}
			c := &Controller{server: core, tag: "rollback", Options: &conf.Options{}, appliedInfo: old, appliedSig: inboundSignature(old), userList: oldUsers, pendingUsers: newUsers}
			c.info.Store(next) // metadata already reflects the requested config
			if err := c.rebuildInbound(next); err == nil {
				t.Fatal("expected rebuild failure")
			}
			if live != old || !reflect.DeepEqual(liveUsers, oldUsers) {
				t.Fatalf("rollback restored wrong state: node=%+v users=%v", live, liveUsers)
			}
			if c.appliedInfo != old || c.appliedSig != inboundSignature(old) {
				t.Fatal("failed candidate published as applied")
			}
		})
	}
}

func TestNodeStartRollsBackControllersOnPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			w.Write([]byte(`{"server_port":12345,"network":"tcp","tls":0,"base_config":{"pull_interval":3600,"push_interval":3600}}`))
		case strings.HasSuffix(r.URL.Path, "/user"):
			w.Write([]byte(`{"users":[{"id":1,"uuid":"11111111-1111-1111-1111-111111111111"}]}`))
		case strings.HasSuffix(r.URL.Path, "/alivelist"):
			w.Write([]byte(`{"alive":{}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	live := map[string]bool{}
	core := &lifecycleCore{addNode: func(tag string, _ *panel.NodeInfo) error { live[tag] = true; return nil }, delNode: func(tag string) error { delete(live, tag); return nil }}
	core.addUsers = func(p *vCore.AddUsersParams) (int, error) {
		if p.Tag == "panic-second" {
			panic("injected startup panic")
		}
		return len(p.Users), nil
	}
	n := New()
	configs := []conf.NodeConfig{
		{ApiConfig: conf.ApiConfig{APIHost: srv.URL, NodeType: "vless", NodeID: 1}, Options: conf.Options{Name: "panic-first"}},
		{ApiConfig: conf.ApiConfig{APIHost: srv.URL, NodeType: "vless", NodeID: 2}, Options: conf.Options{Name: "panic-second"}},
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("startup panic was swallowed")
			}
		}()
		_ = n.Start(configs, core)
	}()
	if len(live) != 0 || len(n.controllers) != 0 {
		t.Fatalf("startup leaked state: %v", live)
	}
	for _, tag := range []string{"panic-first", "panic-second"} {
		if _, err := limiter.GetLimiter(tag); err == nil {
			t.Fatalf("limiter %s leaked", tag)
		}
	}
}

func TestControllerCloseWaitsBeforeReleasingResources(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	c := &Controller{tag: "close-order", nodeAdded: true, limiterAdded: true}
	c.limiter = limiter.AddLimiter("vless", c.tag, &conf.LimitConfig{}, nil, nil)
	c.nodeInfoMonitorPeriodic = &task.Task{Interval: time.Hour, ExecuteCtx: func(ctx context.Context) error { close(started); <-ctx.Done(); close(finished); return ctx.Err() }}
	c.server = &lifecycleCore{delNode: func(tag string) error {
		select {
		case <-finished:
		default:
			t.Error("released node before callback stopped")
		}
		if _, err := limiter.GetLimiter(tag); err != nil {
			t.Error("limiter removed before inbound stopped")
		}
		return nil
	}}
	if err := c.nodeInfoMonitorPeriodic.Start(true); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c.reloadAPIClient() // shutdown must reject reload even with nil apiConfig
	if _, err := limiter.GetLimiter(c.tag); err == nil {
		t.Fatal("limiter leaked")
	}
}

func TestUserSnapshotEmpty304AndFailedRetry(t *testing.T) {
	var request atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			w.WriteHeader(http.StatusNotModified)
		case strings.HasSuffix(r.URL.Path, "/user"):
			if request.Add(1) == 1 {
				w.Write([]byte(`{"users":[]}`))
			} else {
				w.WriteHeader(http.StatusNotModified)
			}
		case strings.HasSuffix(r.URL.Path, "/alivelist"):
			w.Write([]byte(`{"alive":{}}`))
		}
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, NodeType: "vless", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	info := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 1000}}
	old := []panel.UserInfo{{Id: 1, Uuid: "old"}}
	calls := 0
	core := &lifecycleCore{updateUsers: func(p *vCore.UpdateUsersParams) error {
		calls++
		if p.Users == nil || len(p.Users) != 0 || len(p.Deleted) != 1 {
			t.Fatalf("incorrect empty snapshot: %+v", p)
		}
		if calls == 1 {
			return errors.New("temporary apply failure")
		}
		return nil
	}}
	c := &Controller{server: core, apiClient: api, tag: "snapshot-test", Options: &conf.Options{}, userList: old, appliedInfo: info, appliedSig: inboundSignature(info)}
	c.info.Store(info)
	c.limiter = limiter.AddLimiter("vless", c.tag, &conf.LimitConfig{}, old, nil)
	defer limiter.DeleteLimiter(c.tag)
	for i := 0; i < 3; i++ {
		if err := c.nodeInfoMonitor(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(c.userList) != 0 || c.pendingUsers != nil {
		t.Fatalf("snapshot retry failed: calls=%d users=%v pending=%v", calls, c.userList, c.pendingUsers)
	}
}

func TestFailedRebuildStillSyncsUsersOnLiveInbound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			w.WriteHeader(http.StatusNotModified)
		case strings.HasSuffix(r.URL.Path, "/user"):
			w.Write([]byte(`{"users":[{"id":2,"uuid":"new"}]}`))
		case strings.HasSuffix(r.URL.Path, "/alivelist"):
			w.Write([]byte(`{"alive":{}}`))
		}
	}))
	defer srv.Close()
	api, err := panel.New(&conf.ApiConfig{APIHost: srv.URL, NodeType: "vless", NodeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	old := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 1000}}
	broken := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ServerPort: 2000}}
	oldUsers := []panel.UserInfo{{Id: 1, Uuid: "old"}}
	var updated *vCore.UpdateUsersParams
	core := &lifecycleCore{
		addNode: func(_ string, n *panel.NodeInfo) error {
			if n == broken {
				return errors.New("port in use")
			}
			return nil
		},
		updateUsers: func(p *vCore.UpdateUsersParams) error { updated = p; return nil },
	}
	c := &Controller{server: core, apiClient: api, tag: "rebuild-fail-sync", Options: &conf.Options{}, userList: oldUsers, appliedInfo: old, appliedSig: inboundSignature(old)}
	c.info.Store(broken)
	c.limiter = limiter.AddLimiter("vless", c.tag, &conf.LimitConfig{}, oldUsers, nil)
	defer limiter.DeleteLimiter(c.tag)
	if err := c.nodeInfoMonitor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if updated == nil {
		t.Fatal("user changes were not applied after a failed rebuild")
	}
	if updated.NodeInfo != old {
		t.Fatal("user changes targeted the config that failed to apply")
	}
	if len(updated.Added) != 1 || len(updated.Deleted) != 1 {
		t.Fatalf("wrong diff: added=%v deleted=%v", updated.Added, updated.Deleted)
	}
	if len(c.userList) != 1 || c.userList[0].Uuid != "new" || c.pendingUsers != nil {
		t.Fatalf("user list not advanced: %v pending=%v", c.userList, c.pendingUsers)
	}
	if c.appliedSig != inboundSignature(old) {
		t.Fatal("failed config must stay pending so the rebuild is retried")
	}
}
