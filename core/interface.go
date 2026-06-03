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
	AddNodeCustomOutbounds(info *panel.NodeInfo) error
	Protocols() []string
	Type() string
}
