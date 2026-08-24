package hy2

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/apernet/hysteria/extras/v2/outbounds/acl"
	"github.com/apernet/hysteria/extras/v2/outbounds/acl/v2geo"
)

type aclTemplateOb struct{ name string }

type aclTemplateGeo struct{}

func (aclTemplateGeo) LoadGeoIP() (map[string]*v2geo.GeoIP, error) {
	return map[string]*v2geo.GeoIP{"cn": {}}, nil
}
func (aclTemplateGeo) LoadGeoSite() (map[string]*v2geo.GeoSite, error) {
	return map[string]*v2geo.GeoSite{"cn": {}, "google": {}}, nil
}

// 直接用 hysteria 自己的解析器+编译器校验模板里的 ACL，
// 避免把语法错误留到节点启动时才炸。
// 校验随仓库分发的 hy2 反 BT 模板能被 hysteria 自己的解析器接受。
// ACL 语法陷阱不少（address 不能含逗号、单端口不 TrimSpace、# 会截断整行），
// 语法错误会让 hy2 节点直接起不来，必须在 CI 阶段就拦住。
func TestAntiBTACLCompiles(t *testing.T) {
	raw, err := os.ReadFile("../../example/anti-bt/hy2config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// 从 yaml 里抠出 "- xxx(...)" 这些 inline 行
	re := regexp.MustCompile(`(?m)^\s+- ([a-z]+\(.*\))\s*$`)
	var lines []string
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		lines = append(lines, m[1])
	}
	if len(lines) < 30 {
		t.Fatalf("只抽到 %d 条 ACL，模板可能没被正确解析", len(lines))
	}
	t.Logf("待校验 ACL 共 %d 条", len(lines))

	rules, err := acl.ParseTextRules(strings.Join(lines, "\n"))
	if err != nil {
		t.Fatalf("ParseTextRules 失败: %v", err)
	}
	obs := map[string]aclTemplateOb{"direct": {"direct"}, "reject": {"reject"}, "default": {"default"}}
	if _, err := acl.Compile[aclTemplateOb](rules, obs, 128, aclTemplateGeo{}); err != nil {
		t.Fatalf("Compile 失败: %v", err)
	}
	t.Log("全部 ACL 通过 hysteria 自身的语法与编译校验")
}

// 反向确认 HY2-01：hysteria ACL 语法确实无法表达按应用层协议阻断。
func TestHysteriaCannotExpressBittorrent(t *testing.T) {
	rules, err := acl.ParseTextRules("reject(all, bittorrent)")
	if err != nil {
		t.Logf("解析阶段即失败: %v", err)
		return
	}
	obs := map[string]aclTemplateOb{"direct": {"direct"}, "reject": {"reject"}, "default": {"default"}}
	if _, err := acl.Compile[aclTemplateOb](rules, obs, 128, aclTemplateGeo{}); err == nil {
		t.Fatal("意外：hysteria 竟然接受了 reject(all, bittorrent)")
	} else {
		t.Logf("如预期，编译失败: %v —— hy2 无法按应用层协议阻断", err)
	}
}
