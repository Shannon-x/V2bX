package node

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	log "github.com/sirupsen/logrus"
)

// applyPanelCert 把面板下发的证书配置合并进本节点的 CertConfig。
//
// 优先级：面板下发 > 本地 config.json。这与 v2node 的行为一致
// （api/v2board/node.go 里 CertInfo 直接来自面板），也是 remote 模式
// 能成立的前提 —— 证书由面板生成，节点不该再用本地那套。
//
// 面板没下发证书配置时原样返回，完全不影响现有部署。
func (c *Controller) applyPanelCert(info *panel.CertInfo) {
	if info == nil || c.CertConfig == nil {
		return
	}

	// 面板给的模式必须是本节点真能执行的，否则宁可不动本地配置。
	//
	// 教训来自一次线上事故：面板管理端的证书模式默认值是 "selfSign"，
	// 而 node/cert.go 只认 none/file/self/http/dns/remote，
	// 于是 requestCert 返回 "unsupported certmode: selfsign"，
	// 节点起不来、systemd 无限重启 —— 而本地 config.json 里明明配着可用的 http。
	// 面板的一个默认值就把一台配置正确的机器打死了。
	//
	// 现在的原则：面板配置只能"改善"节点，不能让本来能跑的节点跑不起来。
	mode := normalizeCertMode(info.CertMode)
	if !supportedCertMode(mode) {
		log.WithField("tag", c.tag).Warnf(
			"panel sent unsupported cert mode %q, keeping local cert config (mode=%s)",
			info.CertMode, c.CertConfig.CertMode)
		return
	}
	// remote 模式没带证书内容等于没配，同样退回本地，不要把节点拖垮。
	if mode == "remote" && (info.TlsCert == "" || info.TlsKey == "") {
		log.WithField("tag", c.tag).Warnf(
			"panel cert mode is remote but tls_cert/tls_key are empty, keeping local cert config (mode=%s)",
			c.CertConfig.CertMode)
		return
	}

	c.CertConfig.CertMode = mode
	if info.CertDomain != "" {
		c.CertConfig.CertDomain = info.CertDomain
	}
	if info.Provider != "" {
		c.CertConfig.Provider = info.Provider
	}
	if info.Email != "" {
		c.CertConfig.Email = info.Email
	}
	if len(info.DNSEnv) > 0 {
		c.CertConfig.DNSEnv = info.DNSEnv
	}
	c.CertConfig.RejectUnknownSni = info.RejectUnknownSni
	c.CertConfig.TlsCert = info.TlsCert
	c.CertConfig.TlsKey = info.TlsKey

	// 面板可能不带路径（它只管内容，不管节点上放哪）。
	// 给个按 tag 区分的默认路径，避免多节点互相覆盖证书。
	if info.CertFile != "" {
		c.CertConfig.CertFile = info.CertFile
	}
	if info.KeyFile != "" {
		c.CertConfig.KeyFile = info.KeyFile
	}
	if c.CertConfig.CertFile == "" || c.CertConfig.KeyFile == "" {
		base := filepath.Join("/etc/V2bX/cert", sanitizeTag(c.tag))
		if c.CertConfig.CertFile == "" {
			c.CertConfig.CertFile = base + ".crt"
		}
		if c.CertConfig.KeyFile == "" {
			c.CertConfig.KeyFile = base + ".key"
		}
		_ = os.MkdirAll(filepath.Dir(c.CertConfig.CertFile), 0o755)
	}
}

// sanitizeTag 把 inbound tag 变成可安全用作文件名的形式。
func sanitizeTag(tag string) string {
	out := make([]rune, 0, len(tag))
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "node"
	}
	return string(out)
}

// CertFingerprints 是一张证书的两种指纹。
//
// 两个值必须分开算，因为各客户端固定的对象不一样：
//
//	CertSha256   sha256(证书 DER)  —— xray 的 pinnedPeerCertSha256(pcs)、
//	                                 hysteria 官方客户端的 tls.pinSHA256
//	PubKeySha256 sha256(公钥 SPKI) —— sing-box 的 certificate_public_key_sha256
//
// 拿错了会直接连不上，所以日志里两个都打出来。
type CertFingerprints struct {
	CertSha256   string
	PubKeySha256 string
}

// certFingerprints 读取 PEM 证书文件并算出两种指纹。
func certFingerprints(certFile string) (*CertFingerprints, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	certHash := sha256.Sum256(cert.Raw)
	spki, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, err
	}
	pubHash := sha256.Sum256(spki)
	return &CertFingerprints{
		CertSha256:   hex.EncodeToString(certHash[:]),
		PubKeySha256: hex.EncodeToString(pubHash[:]),
	}, nil
}

// logCertFingerprints 把证书指纹打进日志。
//
// 存在的意义：SNI 是伪装域名时客户端只能靠指纹固定来验证，
// 而运维此前没有任何便捷途径拿到这两个值（得手动 openssl x509 + dgst）。
// 面板下发了指纹时顺带核对一次，不一致说明节点上的证书不是面板那张
// —— 这种情况客户端一定连不上，早报早好。
func (c *Controller) logCertFingerprints(info *panel.CertInfo) {
	if c.CertConfig == nil || c.CertConfig.CertFile == "" {
		return
	}
	switch c.CertConfig.CertMode {
	case "none", "":
		return
	}
	fp, err := certFingerprints(c.CertConfig.CertFile)
	if err != nil {
		log.WithField("tag", c.tag).Warnf("read cert fingerprint failed: %s", err)
		return
	}
	log.WithField("tag", c.tag).Infof(
		"cert mode=%s domain=%s file=%s",
		c.CertConfig.CertMode, c.CertConfig.CertDomain, c.CertConfig.CertFile)
	log.WithField("tag", c.tag).Infof(
		"cert sha256 (xray pinnedPeerCertSha256 / hysteria pinSHA256): %s", fp.CertSha256)
	log.WithField("tag", c.tag).Infof(
		"pubkey sha256 (sing-box certificate_public_key_sha256): %s", fp.PubKeySha256)

	if info != nil && info.PinnedPeerCertSha256 != "" &&
		!strings.EqualFold(info.PinnedPeerCertSha256, fp.CertSha256) {
		log.WithField("tag", c.tag).Errorf(
			"cert fingerprint mismatch: panel says %s, node cert is %s "+
				"— clients pinning the panel value will fail to connect",
			info.PinnedPeerCertSha256, fp.CertSha256)
	}
}

// normalizeCertMode 把面板可能发来的各种写法归一化成 node/cert.go 认识的值。
//
// 面板管理端用的是 "selfSign"（驼峰），V2bX 这边一直叫 "self"；
// 两边命名不一致本身就是历史遗留，节点侧做兼容比要求所有面板改字段现实。
func normalizeCertMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "selfsign", "self_sign", "self-sign":
		return "self"
	}
	return m
}

// supportedCertMode 与 node/cert.go 的 requestCert 分支保持一致。
// 两处若不同步，就会重演「面板给了个执行不了的模式导致节点起不来」那次事故。
func supportedCertMode(mode string) bool {
	switch mode {
	case "", "none", "file", "self", "http", "dns", "remote":
		return true
	}
	return false
}
