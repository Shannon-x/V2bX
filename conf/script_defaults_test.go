package conf

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/apernet/hysteria/extras/v2/outbounds/acl"
	"github.com/apernet/hysteria/extras/v2/outbounds/acl/v2geo"
)

// 管理脚本有两个会写路由规则的入口：
//
//	菜单 15「生成 V2bX 配置文件」  -> write_default_route_json（整份重写）
//	菜单 19「更新路由禁止规则」    -> update_route_block_rules_xray（只换 block 段）
//
// 两者必须产出同一套禁止规则，否则会出现「生成的配置比更新后的弱」这种坑。
// 做法是让它们共用同一个 build_block_rules，这组测试就是把这件事钉死：
// 直接把脚本里的那段 shell 抽出来真跑一遍，比对产出。
//
// 另外 V2bX.sh 与 initconfig.sh 是复制粘贴关系，历史上已经漂移过，
// 所以两份的产出也要逐字节一致。

var scripts = []string{
	"../V2bX-script-master/V2bX.sh",
	"../V2bX-script-master/initconfig.sh",
}

// canonicalSection 抽出「禁止规则唯一权威来源」那一段 shell。
func canonicalSection(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	const marker = "# 判断 geosite.dat 里是否存在某个分类"
	start := strings.Index(s, marker)
	if start < 0 {
		t.Fatalf("%s 里找不到权威规则段", path)
	}
	fnIdx := strings.Index(s[start:], "write_default_hy2config() {")
	if fnIdx < 0 {
		t.Fatalf("%s 里找不到 write_default_hy2config", path)
	}
	endIdx := strings.Index(s[start+fnIdx:], "\n}\n")
	if endIdx < 0 {
		t.Fatalf("%s 里 write_default_hy2config 没有闭合", path)
	}
	return s[start : start+fnIdx+endIdx+3]
}

// runShell 在脚本片段后追加驱动代码并执行，返回 stdout。
func runShell(t *testing.T, section, driver string) []byte {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("环境里没有 jq，跳过")
	}
	asset := os.Getenv("V2BX_TEST_ASSET")
	if asset == "" {
		abs, err := filepath.Abs("../example")
		if err != nil {
			t.Fatal(err)
		}
		asset = abs
	}
	full := "yellow=''; plain=''; green=''; red=''\n" + section +
		"\nresolve_geosite_path() { echo \"" + filepath.Join(asset, "geosite.dat") + "\"; }\n" + driver
	cmd := exec.Command("bash", "-c", full)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("执行脚本片段失败: %v\nstderr: %s", err, errb.String())
	}
	return out.Bytes()
}

// decodeJSON 解成无序结构后用 reflect.DeepEqual 比较。
// 不能拿 json.Marshal 的字节串比：encoding/json/v2 默认不对 map 键排序，
// 同样的输入两次序列化可能得到不同字节，比出来的「差异」是假的。
func decodeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("产出不是合法 JSON: %v\n%s", err, raw)
	}
	return v
}

// 静态 heredoc 一旦回来，两个入口就又会各写各的，必须挡住。
func TestNoStaticRouteHeredoc(t *testing.T) {
	for _, script := range scripts {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("cat <<'EOF' > /etc/V2bX/route.json")) {
			t.Errorf("%s 里又出现了静态的 route.json heredoc，"+
				"应当统一走 write_default_route_json", script)
		}
		if !bytes.Contains(data, []byte("build_block_rules")) {
			t.Errorf("%s 没有引用 build_block_rules", script)
		}
	}
}

// 两个脚本里的权威规则段必须逐字节一致。
func TestCanonicalSectionIdenticalAcrossScripts(t *testing.T) {
	a := canonicalSection(t, scripts[0])
	b := canonicalSection(t, scripts[1])
	if a != b {
		t.Errorf("V2bX.sh 与 initconfig.sh 的权威规则段已经漂移（%d vs %d 字节）", len(a), len(b))
	}
}

// 菜单 15 的产出：两个脚本一致；有真实 geosite.dat 时还要与 example/route.json 一致。
func TestMenu15RouteOutput(t *testing.T) {
	var outs []any
	for _, script := range scripts {
		out := runShell(t, canonicalSection(t, script), "write_default_route_json /dev/stdout")
		outs = append(outs, decodeJSON(t, out))
	}
	if !reflect.DeepEqual(outs[0], outs[1]) {
		t.Fatal("两个脚本生成的 route.json 不一致")
	}
	if os.Getenv("V2BX_TEST_ASSET") == "" {
		t.Skip("仓库自带的 example/geosite.dat 已过期，缺分类会被裁掉，" +
			"设置 V2BX_TEST_ASSET 指向真实发布件资源目录可做完整比对")
	}
	want, err := os.ReadFile("../example/route.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outs[0], decodeJSON(t, want)) {
		t.Error("脚本生成的 route.json 与 example/route.json 不一致")
	}
}

// 菜单 19 对菜单 15 的产出必须是幂等的 —— 这正是「两个入口防护一致」的直接证明。
func TestMenu19IsIdempotentOnMenu15Output(t *testing.T) {
	section := canonicalSection(t, scripts[0])
	step15 := runShell(t, section, "write_default_route_json /dev/stdout")

	tmp := filepath.Join(t.TempDir(), "route.json")
	if err := os.WriteFile(tmp, step15, 0o644); err != nil {
		t.Fatal(err)
	}
	// 复刻 update_route_block_rules_xray 的合并语义
	driver := `blocks=$(build_block_rules)
jq --argjson nb "$blocks" '.rules = ($nb + ((.rules // []) | map(select(.outboundTag != "block"))))' ` + tmp
	step19 := runShell(t, section, driver)

	if !reflect.DeepEqual(decodeJSON(t, step15), decodeJSON(t, step19)) {
		t.Error("菜单 19 改动了菜单 15 的产出，说明两个入口的禁止规则不同源")
	}
}

// 菜单 19 换掉禁止规则的同时，必须保留运维自己加的分流规则。
func TestMenu19PreservesCustomRules(t *testing.T) {
	legacy := `{"domainStrategy":"AsIs","rules":[
	  {"type":"field","outboundTag":"block","ip":["geoip:private"]},
	  {"type":"field","outboundTag":"warp","domain":["geosite:openai"]},
	  {"type":"field","outboundTag":"IPv4_out","network":"udp,tcp"}]}`
	tmp := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(tmp, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `blocks=$(build_block_rules)
jq --argjson nb "$blocks" '.rules = ($nb + ((.rules // []) | map(select(.outboundTag != "block"))))' ` + tmp
	out := runShell(t, canonicalSection(t, scripts[0]), driver)

	var cfg struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, r := range cfg.Rules {
		if tag, _ := r["outboundTag"].(string); tag != "block" {
			kept = append(kept, tag)
		}
	}
	if !reflect.DeepEqual(kept, []string{"warp", "IPv4_out"}) {
		t.Errorf("自定义分流规则被丢掉了，实际保留 %v", kept)
	}
}

// 无论 geosite.dat 新旧、甚至完全缺失，这几条基线防护都必须在。
func TestBaselineProtectionAlwaysPresent(t *testing.T) {
	section := canonicalSection(t, scripts[0])
	for name, driver := range map[string]string{
		"本机 geosite.dat":  "build_block_rules",
		"geosite.dat 不存在": "resolve_geosite_path() { echo /nonexistent/geosite.dat; }\nbuild_block_rules",
	} {
		out := runShell(t, section, driver)
		var rules []map[string]any
		if err := json.Unmarshal(out, &rules); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		tags := map[string]bool{}
		for _, r := range rules {
			if s, ok := r["ruleTag"].(string); ok {
				tags[s] = true
			}
		}
		for _, must := range []string{
			"block-private",           // 防 SSRF / 内网穿透
			"block-bt-protocol",       // BT 协议嗅探
			"block-bt-tcp-ports",      // 常见 BT 端口
			"block-smtp",              // 防垃圾邮件投诉
			"block-bt-dht-bootstrap",  // DHT 引导节点
			"block-bt-tracker-domain", // tracker 域名
			"block-abuse-regexp",      // 迅雷 / 临时邮箱 / 竞品
		} {
			if !tags[must] {
				t.Errorf("%s 场景下缺少基线防护规则 %q", name, must)
			}
		}
	}
}

// 空字符串域名在 xray 里匹配所有域名。历史上 example/route.json 用它把
// 全部域名流量导向 socks5-warp（127.0.0.1:40000，默认无人监听），
// 等于把域名访问全部打死。防止它以任何形式回来。
func TestNoEmptyDomainCatchAll(t *testing.T) {
	for _, f := range []string{"../example/route.json", "../example/anti-bt/route.json"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Rules []struct {
				Domain []string `json:"domain"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for i, r := range cfg.Rules {
			for _, d := range r.Domain {
				if strings.TrimSpace(d) == "" {
					t.Errorf("%s 第 %d 条规则里有空域名，会匹配所有域名", f, i)
				}
			}
		}
	}
}

// hy2 的 ACL 现在也只有一个来源 hy2_acl_block：
// 菜单 15 通过 write_default_hy2config 写整份配置，
// 菜单 19 把它灌进已有配置的 acl 段。两条路径都要能过 hysteria 自己的解析器。
func TestHy2ACLSingleSource(t *testing.T) {
	// 脚本里不应再有写死的 hy2config heredoc
	for _, script := range scripts {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("cat <<'EOF' > /etc/V2bX/hy2config.yaml")) {
			t.Errorf("%s 里又出现了静态的 hy2config heredoc，应统一走 write_default_hy2config", script)
		}
	}

	var outs [][]string
	for _, script := range scripts {
		raw := runShell(t, canonicalSection(t, script), "hy2_acl_block")
		lines := aclLines(string(raw))
		if len(lines) < 30 {
			t.Fatalf("%s 的 hy2_acl_block 只产出 %d 条 ACL", script, len(lines))
		}
		outs = append(outs, lines)
	}
	if !reflect.DeepEqual(outs[0], outs[1]) {
		t.Fatal("两个脚本的 hy2 ACL 不一致")
	}

	// 与已校验模板一致
	tmpl, err := os.ReadFile("../example/anti-bt/hy2config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outs[0], aclLines(string(tmpl))) {
		t.Error("脚本的 hy2 ACL 与 example/anti-bt/hy2config.yaml 不一致")
	}

	// 必须能通过 hysteria 自己的解析器 —— 语法错会让节点直接起不来
	rules, err := acl.ParseTextRules(strings.Join(outs[0], "\n"))
	if err != nil {
		t.Fatalf("ACL 解析失败: %v", err)
	}
	obs := map[string]scriptACLOb{"direct": {"direct"}, "reject": {"reject"}, "default": {"default"}}
	if _, err := acl.Compile[scriptACLOb](rules, obs, 128, scriptACLGeo{}); err != nil {
		t.Fatalf("ACL 编译失败: %v", err)
	}
}

// write_default_hy2config 产出的整份配置必须是合法 YAML 骨架 + 那份 ACL。
func TestWriteDefaultHy2ConfigIsComplete(t *testing.T) {
	out := runShell(t, canonicalSection(t, scripts[0]), "write_default_hy2config /dev/stdout")
	text := string(out)
	for _, must := range []string{"quic:", "resolver:", "acl:", "  inline:", "masquerade:"} {
		if !strings.Contains(text, must) {
			t.Errorf("默认 hy2config 缺少 %q 段", must)
		}
	}
	if n := len(aclLines(text)); n < 30 {
		t.Errorf("默认 hy2config 只有 %d 条 ACL", n)
	}
	// ACL 段不能把后面的 masquerade 吞掉
	if strings.Index(text, "masquerade:") < strings.Index(text, "acl:") {
		t.Error("masquerade 段位置异常")
	}
}

type scriptACLOb struct{ name string }

type scriptACLGeo struct{}

func (scriptACLGeo) LoadGeoIP() (map[string]*v2geo.GeoIP, error) {
	return map[string]*v2geo.GeoIP{"cn": {}}, nil
}

func (scriptACLGeo) LoadGeoSite() (map[string]*v2geo.GeoSite, error) {
	return map[string]*v2geo.GeoSite{"cn": {}, "google": {}}, nil
}

var aclLineRe = regexp.MustCompile(`(?m)^\s+- ([a-z]+\(.*\))\s*$`)

func aclLines(s string) []string {
	var out []string
	for _, m := range aclLineRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// 全新安装时机器上往往没有 jq。这一组测试锁住那次线上故障：
// initconfig.sh 定义了 ensure_jq 却忘了调用，导致 install.sh 走首装流程时
// 一路报 "jq: command not found"，route.json 根本没落地；
// 如果节点用的是 xray 内核，V2bX 会因为读不到路由文件而直接起不来。
func TestGenerateConfigEnsuresJq(t *testing.T) {
	for _, script := range scripts {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		idx := strings.Index(s, "generate_config_file() {")
		if idx < 0 {
			t.Fatalf("%s 里找不到 generate_config_file", script)
		}
		// 只看函数体开头那一段，避免匹配到别处的 ensure_jq
		head := s[idx:]
		if len(head) > 4000 {
			head = head[:4000]
		}
		if !strings.Contains(head, "ensure_jq") {
			t.Errorf("%s 的 generate_config_file 没有调用 ensure_jq", script)
		}
	}
}

// 装不上 jq 也必须能拿到完整的路由规则。
func TestRouteJSONFallbackWithoutJq(t *testing.T) {
	// 在 shell 里用同名函数遮蔽 command 内建，模拟「找不到 jq」
	const shadow = `command() { if [ "$1" = "-v" ] && [ "$2" = "jq" ]; then return 1; fi; builtin command "$@"; }
`
	for _, script := range scripts {
		out := runShell(t, canonicalSection(t, script),
			shadow+"write_default_route_json /dev/stdout")
		var cfg struct {
			Rules []map[string]any `json:"rules"`
		}
		if err := json.Unmarshal(out, &cfg); err != nil {
			t.Fatalf("%s 无 jq 时产出的不是合法 JSON: %v\n%s", script, err, out)
		}
		if len(cfg.Rules) < 10 {
			t.Errorf("%s 无 jq 时只产出 %d 条规则", script, len(cfg.Rules))
		}
		want, err := os.ReadFile("../example/route.json")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decodeJSON(t, out), decodeJSON(t, want)) {
			t.Errorf("%s 的无 jq 兜底与 example/route.json 不一致", script)
		}
	}
}

// 静态兜底与 jq 路径必须逐字节同步，否则两条路径会悄悄分叉。
func TestStaticFallbackMatchesJqPath(t *testing.T) {
	if os.Getenv("V2BX_TEST_ASSET") == "" {
		t.Skip("需要真实发布件的 geosite.dat 才能比对（jq 路径会按分类裁剪），" +
			"设置 V2BX_TEST_ASSET 后再跑")
	}
	section := canonicalSection(t, scripts[0])
	static := runShell(t, section, "route_json_static")
	viaJq := runShell(t, section, "write_default_route_json /dev/stdout")
	if !reflect.DeepEqual(decodeJSON(t, static), decodeJSON(t, viaJq)) {
		t.Error("静态兜底与 jq 路径的产出已经分叉")
	}
}

// install.sh 得把 jq 一并装上，别把兜底当常态。
func TestInstallScriptInstallsJq(t *testing.T) {
	data, err := os.ReadFile("../V2bX-script-master/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	idx := strings.Index(s, "install_base() {")
	if idx < 0 {
		t.Fatal("install.sh 里找不到 install_base")
	}
	end := strings.Index(s[idx:], "\n}\n")
	body := s[idx : idx+end]
	for _, mgr := range []string{"yum install", "apk add", "apt install"} {
		i := strings.Index(body, mgr)
		if i < 0 {
			continue
		}
		line := body[i:]
		if j := strings.Index(line, "\n"); j > 0 {
			line = line[:j]
		}
		if !strings.Contains(line, " jq") {
			t.Errorf("install_base 的 %q 分支没有安装 jq:\n    %s", mgr, strings.TrimSpace(line))
		}
	}
}
