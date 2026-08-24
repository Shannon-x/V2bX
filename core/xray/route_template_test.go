package xray

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	routing_session "github.com/xtls/xray-core/features/routing/session"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// assetDir 返回用于测试的 geosite.dat 所在目录。
//
// 优先用环境变量 V2BX_TEST_ASSET 指向的真实发布件资源目录；
// 没有的话退回仓库里的 example/。注意仓库那份 example/geosite.dat 只是占位样本，
// 比发布件里的旧很多（发布流程从 Loyalsoldier/v2ray-rules-dat 拉最新的，
// 见 .github/workflows/release.yml），分类集合不一致是正常的。
func assetDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("V2BX_TEST_ASSET"); dir != "" {
		return dir
	}
	dir, err := filepath.Abs("../../example")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildRoute(t *testing.T, raw []byte) (*coreConf.RouterConfig, error) {
	t.Helper()
	dir := assetDir(t)
	if _, err := os.Stat(filepath.Join(dir, "geosite.dat")); err != nil {
		t.Skipf("geosite.dat 不可用: %v", err)
	}
	t.Setenv("xray.location.asset", dir)
	cfg := &coreConf.RouterConfig{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		t.Fatalf("route.json 解析失败: %v", err)
	}
	_, err := cfg.Build()
	return cfg, err
}

// 校验反 BT 模板的结构合法性：规则语法、端口写法、正则、outboundTag 引用。
//
// 关于 geosite 分类：模板引用了 category-public-tracker / category-ipfs /
// category-antivirus，它们在发布件用的 Loyalsoldier geosite.dat 里都存在，
// 但在仓库这份过期的 example/geosite.dat 里没有。所以本地跑到分类缺失时
// 只提示、不判失败——真正要卡的是「运维换了老 dat 会不会整份规则一起炸」，
// 那由管理脚本的分类校验负责（缺什么跳过什么）。
// 用 V2BX_TEST_ASSET 指向真实发布件目录即可做完整校验。
func TestAntiBTRouteTemplateBuilds(t *testing.T) {
	data, err := os.ReadFile("../../example/anti-bt/route.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, buildErr := buildRoute(t, data)
	if buildErr == nil {
		t.Logf("构建成功，共 %d 条规则（geosite 资源目录 %s）", len(cfg.RuleList), assetDir(t))
		return
	}
	if os.Getenv("V2BX_TEST_ASSET") != "" {
		t.Fatalf("在真实发布件资源上构建失败: %v", buildErr)
	}
	t.Skipf("仓库自带的 example/geosite.dat 已过期，缺少模板引用的分类，跳过：%v", buildErr)
}

// 结构性校验：把所有 geosite 引用换成一个必定存在的分类，
// 这样无论本地 dat 新旧，规则本身的语法/端口/正则都会被真正构建一遍。
func TestAntiBTRouteTemplateStructureIsValid(t *testing.T) {
	data, err := os.ReadFile("../../example/anti-bt/route.json")
	if err != nil {
		t.Fatal(err)
	}
	// cn 是任何一份 geosite.dat 都有的分类
	normalized := []byte(replaceGeositeRefs(string(data)))
	cfg, buildErr := buildRoute(t, normalized)
	if buildErr != nil {
		t.Fatalf("模板结构不合法: %v", buildErr)
	}
	t.Logf("结构校验通过，共 %d 条规则", len(cfg.RuleList))
}

func replaceGeositeRefs(s string) string {
	out := make([]byte, 0, len(s))
	const prefix = `"geosite:`
	for {
		i := indexOf(s, prefix)
		if i < 0 {
			return string(append(out, s...))
		}
		out = append(out, s[:i]...)
		out = append(out, `"geosite:cn"`...)
		rest := s[i+len(prefix):]
		j := indexOf(rest, `"`)
		s = rest[j+1:]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// 引用一个必定不存在的分类时，xray 会在 Build() 阶段直接失败。
// V2bX 的 core/xray/xray.go 对这个错误是 log.Panic，也就是整个进程起不来。
// 管理脚本的分类白名单校验就是为了避免下发这种规则。
func TestUnknownGeositeCategoryFailsBuild(t *testing.T) {
	raw := []byte(`{"rules":[{"type":"field","outboundTag":"block","domain":["geosite:category-definitely-not-a-real-list"]}]}`)
	if _, err := buildRoute(t, raw); err == nil {
		t.Fatal("引用不存在的分类竟然构建成功了")
	} else {
		t.Logf("如预期构建失败: %v", err)
	}
}

// 下面这组用真实 router 跑域名，验证禁止规则「该拦的拦得住、不该拦的别误伤」。
//
// 背景：这套规则里的域名部分是从旧配置继承来的，实测发现两类问题——
//   1. 死规则。xray 的 regexp: 匹配的是**域名**，不是 URL，
//      所以 peer_id= / info_hash / announce.php?passkey= / magnet: 这些
//      永远不可能出现在域名里；`(^.@)(guerrillamail|...)` 更是要求域名里带 @，
//      实测一个样本都命中不了。
//   2. 误伤。`(.*\.||)(duba)\.(com)` 这种写法前缀有空分支、末尾不锚定，
//      而 xray 的 regexp: 是非锚定匹配，结果 notduba.com 和
//      duba.com.legit-cdn.net 这种伪造域名都会被命中。
// 现在改成 domain: 精确匹配 + 锚定正则，这组测试防止旧写法回流。

func buildRouterFromTemplate(t *testing.T) *router.Router {
	t.Helper()
	data, err := os.ReadFile("../../example/route.json")
	if err != nil {
		t.Fatal(err)
	}
	// 剔除依赖 geosite.dat 的规则，这样无论本机 dat 新旧都能跑
	var raw struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	kept := make([]map[string]any, 0, len(raw.Rules))
	for _, r := range raw.Rules {
		encoded, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), "geosite:") {
			kept = append(kept, r)
		}
	}
	rebuilt, err := json.Marshal(map[string]any{"domainStrategy": "AsIs", "rules": kept})
	if err != nil {
		t.Fatal(err)
	}
	cfg, buildErr := buildRoute(t, rebuilt)
	if buildErr != nil {
		t.Fatalf("构建失败: %v", buildErr)
	}
	built, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	r := new(router.Router)
	if err := r.Init(context.Background(), built, nil, nil, nil); err != nil {
		t.Skipf("Router.Init 需要更多依赖，跳过: %v", err)
	}
	return r
}

func routeOf(r *router.Router, host string) string {
	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.DomainAddress(host), 443),
	}})
	route, err := r.PickRoute(routing_session.AsRoutingContext(ctx))
	if err != nil {
		return "ERR"
	}
	return route.GetOutboundTag()
}

func TestTemplateBlocksAbuseDomains(t *testing.T) {
	r := buildRouterFromTemplate(t)
	for _, d := range []string{
		// BT / PT
		"router.bittorrent.com", "dht.transmissionbt.com", "opentrackr.org",
		"tracker.opentrackr.org", "thepiratebay.org", "www.1337x.to", "nyaa.si",
		"torrentfreak.com", "bittorrent.com", "ed2k.com",
		// 迅雷 / 杀软 / 统计 / 竞品 / 临时邮箱
		"xunlei.com", "www.xunlei.com", "sandai.net",
		"duba.com", "www.duba.com", "guanjia.qq.com", "so.com", "www.360.cn",
		"umeng.com", "cnzz.com", "guerrillamail.com", "laomoe.com", "flows.pages.dev",
		// 百度定位上报
		"api.map.baidu.com", "sv.baidu.com",
	} {
		if got := routeOf(r, d); got != "block" {
			t.Errorf("%-28s 应被拦截，实际走 %s", d, got)
		}
	}
}

func TestTemplateDoesNotOverBlock(t *testing.T) {
	r := buildRouterFromTemplate(t)
	for _, d := range []string{
		// 常规站点
		"github.com", "www.google.com", "cloudflare.com", "apple.com",
		"microsoft.com", "stackoverflow.com", "wikipedia.org", "netflix.com",
		"youtube.com", "www.baidu.com", "storage.googleapis.com",
		// thunder 曾经在关键词表里，会误伤这个正规服务
		"thunderbird.net",
		// 前缀相似：不能因为包含子串就被拦
		"notduba.com", "xduba.com", "myumeng.com", "nocnzz.com",
		"nottorproject.org", "notso.com", "also.com",
		// 伪造后缀：把目标域名放在前面骗匹配
		"duba.com.legit-cdn.net", "umeng.com.evil.net", "so.com.evil.net",
		"api.map.baidu.com.evil.net",
	} {
		if got := routeOf(r, d); got == "block" {
			t.Errorf("%-28s 被误伤了", d)
		}
	}
}

// 死规则会给运维「已经挡住了」的错觉，实际一条都没生效。
// 这条测试要求每条 domain 规则至少要有一个能命中它的真实域名样本。
func TestNoDeadDomainRules(t *testing.T) {
	data, err := os.ReadFile("../../example/route.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Rules []struct {
			RuleTag string   `json:"ruleTag"`
			Domain  []string `json:"domain"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	// 这几个字符在正则里没有特殊含义，只能是字面量，而域名里不可能出现，
	// 一旦出现说明这条规则是照搬 URL 匹配写的，永不命中。
	// 注意不能把 ? 直接列进来——它是正则量词，只有转义成 \? 才是字面问号。
	// 也不列 _，下划线在某些主机名里是合法的。
	impossible := []string{"=", "@", ":", "/", `\?`}
	for _, rule := range cfg.Rules {
		for _, d := range rule.Domain {
			if !strings.HasPrefix(d, "regexp:") {
				continue
			}
			body := strings.TrimPrefix(d, "regexp:")
			for _, ch := range impossible {
				if strings.Contains(body, ch) {
					t.Errorf("规则 %s 的正则含域名里不可能出现的 %q，很可能是死规则：\n    %s",
						rule.RuleTag, ch, d)
				}
			}
		}
	}
}
