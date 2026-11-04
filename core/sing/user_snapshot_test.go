package sing

import (
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/core"
	"github.com/sagernet/sing-box/option"
)

func TestUserSnapshotScopesAccountingAndClearsUsers(t *testing.T) {
	instance, err := New(&conf.CoreConfig{SingConfig: conf.NewSingConfig()})
	if err != nil {
		t.Fatal(err)
	}
	b := instance.(*Sing)
	defer b.Close()
	info := &panel.NodeInfo{Type: "vmess", Common: &panel.CommonNode{ServerPort: 0}, VAllss: &panel.VAllssNode{Network: "tcp"}}
	users := []panel.UserInfo{{Id: 1, Uuid: "11111111-1111-1111-1111-111111111111"}}
	for _, tag := range []string{"a", "b"} {
		if err := b.AddNode(tag, info, &conf.Options{ListenIP: "127.0.0.1", SingOptions: conf.NewSingOptions()}); err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info, Users: users}); err != nil {
			t.Fatal(err)
		}
	}
	old := b.inboundOptions["a"].(*option.VMessInboundOptions)
	if err := b.UpdateUsers(&core.UpdateUsersParams{AddUsersParams: core.AddUsersParams{Tag: "a", NodeInfo: info, Users: []panel.UserInfo{}}, Deleted: users}); err != nil {
		t.Fatal(err)
	}
	if len(old.Users) != 1 {
		t.Fatal("candidate modified old snapshot")
	}
	if len(b.inboundOptions["a"].(*option.VMessInboundOptions).Users) != 0 {
		t.Fatal("empty snapshot did not clear users")
	}
	if b.users.uidMap[format.UserTag("b", users[0].Uuid)] != 1 {
		t.Fatal("snapshot erased another node's user")
	}
	if _, ok := b.users.uidMap[format.UserTag("a", users[0].Uuid)]; ok {
		t.Fatal("removed user accounting retained")
	}
}

func TestShadowsocksEmptySnapshotKeepsMultiUserMode(t *testing.T) {
	old := &option.ShadowsocksInboundOptions{Method: "aes-128-gcm", Users: []option.ShadowsocksUser{{Name: "old", Password: "old"}}}
	candidate, err := userOptions(old, &core.AddUsersParams{Users: []panel.UserInfo{}}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := candidate.(*option.ShadowsocksInboundOptions)
	if !next.Managed || len(next.Users) != 0 {
		t.Fatal("empty snapshot can fall back to single-user auth")
	}
	if len(old.Users) != 1 {
		t.Fatal("old options mutated")
	}
}

func TestShadowsocks2022SkipsOnlyShortUUIDUsers(t *testing.T) {
	good := "11111111-1111-1111-1111-111111111111"
	for _, method := range []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"} {
		t.Run(method, func(t *testing.T) {
			old := &option.ShadowsocksInboundOptions{Method: method}
			p := &core.AddUsersParams{Users: []panel.UserInfo{{Id: 1, Uuid: "short"}, {Id: 2, Uuid: good}, {Id: 3, Uuid: "x"}}}
			candidate, err := userOptions(old, p, true, nil)
			if err != nil {
				t.Fatalf("short UUID failed the whole snapshot: %v", err)
			}
			next := candidate.(*option.ShadowsocksInboundOptions)
			if len(next.Users) != 1 || next.Users[0].Name != good || next.Users[0].Password == "" {
				t.Fatalf("expected only the valid user, got %+v", next.Users)
			}
			if len(p.Users) != 3 {
				t.Fatal("caller's user list mutated")
			}
		})
	}
}
