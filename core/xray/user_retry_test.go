package xray

import (
	"context"
	"errors"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/format"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/core/xray/app/dispatcher"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/proxy"
)

type retryUserManager struct {
	proxy.Inbound
	proxy.UserManager
	users      map[string]*protocol.MemoryUser
	adds       map[string]int
	removes    map[string]int
	failAdd    string
	failRemove string
}

func (m *retryUserManager) AddUser(_ context.Context, u *protocol.MemoryUser) error {
	m.adds[u.Email]++
	if m.failAdd == u.Email {
		m.failAdd = ""
		return errors.New("injected add failure")
	}
	if m.users[u.Email] != nil {
		return errors.New("duplicate user")
	}
	m.users[u.Email] = u
	return nil
}
func (m *retryUserManager) RemoveUser(_ context.Context, email string) error {
	m.removes[email]++
	if m.failRemove == email {
		m.failRemove = ""
		return errors.New("injected remove failure")
	}
	if m.users[email] == nil {
		return errors.New("user not found")
	}
	delete(m.users, email)
	return nil
}
func (m *retryUserManager) GetUser(_ context.Context, email string) *protocol.MemoryUser {
	return m.users[email]
}

type retryHandler struct {
	inbound.Handler
	manager *retryUserManager
}

func (h *retryHandler) GetInbound() proxy.Inbound { return h.manager }

type retryInboundManager struct {
	inbound.Manager
	manager *retryUserManager
}

func (m *retryInboundManager) GetHandler(context.Context, string) (inbound.Handler, error) {
	return &retryHandler{manager: m.manager}, nil
}
func retryXray() (*Xray, *retryUserManager) {
	m := &retryUserManager{users: map[string]*protocol.MemoryUser{}, adds: map[string]int{}, removes: map[string]int{}}
	x := &Xray{users: &UserMap{uidMap: map[string]int{}}, ihm: &retryInboundManager{manager: m}, dispatcher: &dispatcher.DefaultDispatcher{}}
	return x, m
}

func TestUserSnapshotRetryAfterPartialAddPreservesTraffic(t *testing.T) {
	x, m := retryXray()
	info := &panel.NodeInfo{Type: "hysteria2"}
	old := panel.UserInfo{Id: 1, Uuid: "old"}
	keep := panel.UserInfo{Id: 2, Uuid: "keep"}
	first := panel.UserInfo{Id: 3, Uuid: "first"}
	second := panel.UserInfo{Id: 4, Uuid: "second"}
	if _, err := x.AddUsers(&vCore.AddUsersParams{Tag: "n", NodeInfo: info, Users: []panel.UserInfo{old, keep}}); err != nil {
		t.Fatal(err)
	}
	tc := counter.NewTrafficCounter()
	x.dispatcher.Counter.Store("n", tc)
	oldKey, keepKey, firstKey, secondKey := format.UserTag("n", old.Uuid), format.UserTag("n", keep.Uuid), format.UserTag("n", first.Uuid), format.UserTag("n", second.Uuid)
	tc.GetCounter(oldKey).UpCounter.Add(11)
	keepCounter := tc.GetCounter(keepKey)
	keepCounter.UpCounter.Add(23)
	m.failAdd = secondKey
	p := &vCore.UpdateUsersParams{AddUsersParams: vCore.AddUsersParams{Tag: "n", NodeInfo: info, Users: []panel.UserInfo{keep, first, second}}, Added: []panel.UserInfo{first, second}, Deleted: []panel.UserInfo{old}}
	if err := vCore.ApplyUserUpdate(x, p); err == nil {
		t.Fatal("expected partial add failure")
	}
	if _, ok := x.users.uidMap[secondKey]; ok {
		t.Fatal("failed registration was published")
	}
	firstCounter := tc.GetCounter(firstKey)
	firstCounter.UpCounter.Add(37)
	if err := vCore.ApplyUserUpdate(x, p); err != nil {
		t.Fatalf("snapshot retry failed: %v", err)
	}
	if m.removes[oldKey] != 1 || m.adds[firstKey] != 1 || m.adds[secondKey] != 2 {
		t.Fatalf("non-idempotent retry: adds=%v removes=%v", m.adds, m.removes)
	}
	if tc.GetCounter(firstKey) != firstCounter || firstCounter.UpCounter.Load() != 37 || tc.GetCounter(keepKey) != keepCounter || keepCounter.UpCounter.Load() != 23 {
		t.Fatal("retry reset existing traffic")
	}
	if _, ok := tc.Counters.Load(oldKey); ok {
		t.Fatal("successful deletion did not clean counter")
	}
	if len(x.users.uidMap) != 3 || x.users.uidMap[secondKey] != 4 {
		t.Fatalf("incorrect applied users: %v", x.users.uidMap)
	}
}

func TestAddUsersPublishesOnlyAfterValidationAndRegistration(t *testing.T) {
	x, m := retryXray()
	p := &vCore.AddUsersParams{Tag: "n", NodeInfo: &panel.NodeInfo{Type: "unsupported"}, Users: []panel.UserInfo{{Id: 1, Uuid: "invalid"}}}
	if _, err := x.AddUsers(p); err == nil {
		t.Fatal("expected validation error")
	}
	if len(x.users.uidMap) != 0 || len(m.adds) != 0 {
		t.Fatal("invalid batch changed live users")
	}
	p.NodeInfo = &panel.NodeInfo{Type: "hysteria2"}
	if _, err := x.AddUsers(p); err != nil {
		t.Fatal(err)
	}
	m.users = map[string]*protocol.MemoryUser{} // inbound rebuilt; bookkeeping still exists
	if n, err := x.AddUsers(p); err != nil || n != 1 {
		t.Fatalf("replacement inbound did not receive existing user: n=%d err=%v", n, err)
	}
}

func TestFailedDeleteRetainsAccountingUntilRetry(t *testing.T) {
	x, m := retryXray()
	info := &panel.NodeInfo{Type: "hysteria2"}
	users := []panel.UserInfo{{Id: 1, Uuid: "user"}}
	if _, err := x.AddUsers(&vCore.AddUsersParams{Tag: "n", NodeInfo: info, Users: users}); err != nil {
		t.Fatal(err)
	}
	key := format.UserTag("n", "user")
	tc := counter.NewTrafficCounter()
	x.dispatcher.Counter.Store("n", tc)
	storage := tc.GetCounter(key)
	storage.UpCounter.Add(19)
	m.failRemove = key
	if err := x.DelUsers(users, "n", info); err == nil {
		t.Fatal("expected remove failure")
	}
	if x.users.uidMap[key] != 1 || tc.GetCounter(key) != storage || storage.UpCounter.Load() != 19 {
		t.Fatal("failed delete discarded accounting")
	}
	if err := x.DelUsers(users, "n", info); err != nil {
		t.Fatal(err)
	}
	if err := x.DelUsers(users, "n", info); err != nil {
		t.Fatalf("repeated delete failed: %v", err)
	}
}
