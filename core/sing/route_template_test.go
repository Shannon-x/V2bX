package sing

import (
	"context"
	"os"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// 校验随仓库分发的 sing-box 反 BT 模板能被 sing-box 自己的配置解析器接受。
// sing-box 1.12 起 inbound 级 sniff 已被移除、改成 route action "sniff"，
// 语法一错节点就起不来，必须在 CI 阶段拦住。
func TestAntiBTSingTemplateParses(t *testing.T) {
	data, err := os.ReadFile("../../example/anti-bt/sing_origin.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx := box.Context(context.Background(),
		include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(),
		include.DNSTransportRegistry(), include.ServiceRegistry())

	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		t.Fatalf("模板无法被 sing-box 解析: %v", err)
	}
	if opts.Route == nil || len(opts.Route.Rules) == 0 {
		t.Fatal("模板没有解析出任何路由规则")
	}
	t.Logf("解析出 %d 条路由规则，final = %s", len(opts.Route.Rules), opts.Route.Final)

	// 第一条必须是 sniff，否则后面所有 protocol 规则都永远不会命中。
	if got := opts.Route.Rules[0].DefaultOptions.Action; got != "sniff" {
		t.Fatalf("第一条规则的 action = %q，必须是 sniff", got)
	}
	sniffers := opts.Route.Rules[0].DefaultOptions.SniffOptions.Sniffer
	var hasBT bool
	for _, s := range sniffers {
		if s == "bittorrent" {
			hasBT = true
		}
	}
	if !hasBT {
		t.Fatalf("sniff 动作没有启用 bittorrent 嗅探器: %v", sniffers)
	}

	// 真正把配置构建起来，语义错误（未知出站 tag、非法端口区间等）在这一步暴露。
	opts.Log = &option.LogOptions{Disabled: true}
	if _, err := box.New(box.Options{Context: ctx, Options: opts}); err != nil {
		t.Fatalf("模板无法构建成 sing-box 实例: %v", err)
	}
	t.Log("模板通过 sing-box 的解析与构建校验")
}
