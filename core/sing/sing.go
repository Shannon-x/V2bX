package sing

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
)

var _ vCore.Core = (*Sing)(nil)

type DNSConfig struct {
	Servers []map[string]interface{} `json:"servers"`
	Rules   []map[string]interface{} `json:"rules"`
}

type Sing struct {
	box        *box.Box
	ctx        context.Context
	hookServer *HookServer
	router     adapter.Router
	logFactory log.Factory
	users      *UserMap
	// W2.3 / audit #32 #33: nodeReportMinTrafficBytes and inboundOptions are
	// written from AddNode / UpdateNodeReportMinTraffic / DelNode and read
	// concurrently from GetUserTrafficSlice / AddUsers / DelUsers on independent
	// goroutines. optsMu serializes the map mutations to prevent
	// `fatal error: concurrent map read and map write`.
	// (The existing users.mapLock protects users.uidMap only.)
	optsMu                    sync.RWMutex
	nodeReportMinTrafficBytes map[string]int64
	inboundOptions            map[string]any // tag -> inbound options, for rebuild on user change

	// W2.9 / W6 / audit #31: per-tag mutex so AddUsers/DelUsers/rebuildInbound
	// for tag A doesn't block tag B's user updates. The previous design held
	// b.users.mapLock (a global lock) for the entire rebuildInbound call,
	// which involves sing-box level listener teardown + recreate — easily
	// hundreds of ms on a busy node, and entirely blocking for the duration.
	tagLocks sync.Map // map[string]*sync.Mutex
}

// tagMutex returns the per-tag serialization mutex, allocating on first use.
// Callers MUST Lock/Unlock the returned mutex around any code path that
// mutates that tag's inboundOptions or rebuilds the inbound.
func (b *Sing) tagMutex(tag string) *sync.Mutex {
	if v, ok := b.tagLocks.Load(tag); ok {
		return v.(*sync.Mutex)
	}
	v, _ := b.tagLocks.LoadOrStore(tag, &sync.Mutex{})
	return v.(*sync.Mutex)
}

type UserMap struct {
	uidMap  map[string]int
	mapLock sync.RWMutex
}

func init() {
	vCore.RegisterCore("sing", New)
}

func New(c *conf.CoreConfig) (vCore.Core, error) {
	ctx := context.Background()
	ctx = box.Context(ctx, include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry())
	options := option.Options{}
	if len(c.SingConfig.OriginalPath) != 0 {
		data, err := os.ReadFile(c.SingConfig.OriginalPath)
		if err != nil {
			return nil, fmt.Errorf("read original config error: %s", err)
		}
		options, err = json.UnmarshalExtendedContext[option.Options](ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unmarshal original config error: %s", err)
		}
	}
	options.Log = &option.LogOptions{
		Disabled:  c.SingConfig.LogConfig.Disabled,
		Level:     c.SingConfig.LogConfig.Level,
		Timestamp: c.SingConfig.LogConfig.Timestamp,
		Output:    c.SingConfig.LogConfig.Output,
	}
	options.NTP = &option.NTPOptions{
		Enabled:       c.SingConfig.NtpConfig.Enable,
		WriteToSystem: true,
		ServerOptions: option.ServerOptions{
			Server:     c.SingConfig.NtpConfig.Server,
			ServerPort: c.SingConfig.NtpConfig.ServerPort,
		},
	}
	os.Setenv("SING_DNS_PATH", "")
	b, err := box.New(box.Options{
		Context: ctx,
		Options: options,
	})
	if err != nil {
		return nil, err
	}
	hs := &HookServer{
		counter: sync.Map{},
	}
	b.Router().AppendTracker(hs)
	return &Sing{
		ctx:        ctx,
		box:        b,
		hookServer: hs,
		router:     b.Router(),
		logFactory: b.LogFactory(),
		users: &UserMap{
			uidMap: make(map[string]int),
		},
		nodeReportMinTrafficBytes: make(map[string]int64),
		inboundOptions:            make(map[string]any),
	}, nil
}

func (b *Sing) Start() error {
	return b.box.Start()
}

func (b *Sing) Close() error {
	return b.box.Close()
}

func (b *Sing) Protocols() []string {
	return []string{
		"vmess",
		"vless",
		"shadowsocks",
		"trojan",
		"tuic",
		"anytls",
		"hysteria",
		"hysteria2",
	}
}

func (b *Sing) Type() string {
	return "sing"
}

func (b *Sing) AddNodeCustomOutbounds(info *panel.NodeInfo, opts *conf.Options) error {
	// Not supported for sing-box currently, quietly ignore.
	_ = opts
	return nil
}

// rebuildInbound removes and re-creates an inbound with updated options.
// sing-box v1.13+ does not expose AddUsers/DelUsers, so full rebuild is needed.
func (b *Sing) rebuildInbound(tag string, inboundType string, opts any) error {
	in := b.box.Inbound()
	_ = in.Remove(tag)
	err := in.Create(
		b.ctx,
		b.box.Router(),
		b.logFactory.NewLogger(F.ToString("inbound/", inboundType, "[", tag, "]")),
		tag,
		inboundType,
		opts,
	)
	if err != nil {
		return fmt.Errorf("rebuild inbound error: %s", err)
	}
	return nil
}
