package core

import (
	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

type AddUsersParams struct {
	Tag   string
	Users []panel.UserInfo
	*panel.NodeInfo
}

// UpdateUsersParams carries one authoritative snapshot and its incremental diff.
// Cores without UserUpdater retain their existing add/delete semantics.
type UpdateUsersParams struct {
	AddUsersParams
	Added   []panel.UserInfo
	Deleted []panel.UserInfo
}

type UserUpdater interface {
	UpdateUsers(*UpdateUsersParams) error
}

func ApplyUserUpdate(c Core, p *UpdateUsersParams) error {
	if updater, ok := c.(UserUpdater); ok {
		return updater.UpdateUsers(p)
	}
	if len(p.Deleted) > 0 {
		if err := c.DelUsers(p.Deleted, p.Tag, p.NodeInfo); err != nil {
			return err
		}
	}
	if len(p.Added) > 0 {
		_, err := c.AddUsers(&AddUsersParams{Tag: p.Tag, NodeInfo: p.NodeInfo, Users: p.Added})
		return err
	}
	return nil
}

type Core interface {
	Start() error
	Close() error
	AddNode(tag string, info *panel.NodeInfo, config *conf.Options) error
	DelNode(tag string) error
	AddUsers(p *AddUsersParams) (added int, err error)
	GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error)
	// ReturnUserTraffic re-adds a slice of traffic deltas to the per-user
	// counters. Used by the report task to recover from a failed panel push
	// after GetUserTrafficSlice(tag, true) has already swapped the counters
	// to zero — without it the reset traffic would be permanently lost.
	// W3.1 / audit #13. Implementations must use atomic Add so concurrent
	// connection traffic accumulated between Swap and ReturnUserTraffic is
	// preserved (the recovered traffic is added on top, not stored over).
	ReturnUserTraffic(tag string, traffic []panel.UserTraffic) error
	DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error
	UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options)
	// UpdateDNS re-applies the panel-pushed DNS-unlock routes (info.RawDNS) to
	// the running core during a hot config reload, so editing a DNS route in the
	// panel takes effect without a full process restart. Only the xray core does
	// real work; hy2/sing are no-ops. The xray implementation re-renders the DNS
	// file (deduped via bytes.Equal in saveDnsConfig), and the config watcher
	// picks up the change to reload — closing the gap where DNS routes only
	// applied on initial AddNode and were ignored on every later panel edit.
	UpdateDNS(tag string, info *panel.NodeInfo) error
	// AddNodeCustomOutbounds loads panel-supplied raw outbound JSON, filtered
	// by the deployer-controlled CustomOutbound policy in `opts`. W6 / audit
	// #8: `opts` was added so the implementation can consult the trust
	// boundary instead of blindly accepting every panel push.
	AddNodeCustomOutbounds(info *panel.NodeInfo, opts *conf.Options) error
	Protocols() []string
	Type() string
}
