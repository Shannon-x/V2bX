package conf

type CertConfig struct {
	// CertMode 取值：none / file / self / http / dns / remote
	//   none    不配置 TLS（QUIC 类协议会因此起不来）
	//   file    使用已有的证书文件
	//   self    本机生成自签证书，写入 CertFile/KeyFile 后长期复用
	//   http    Let's Encrypt HTTP-01
	//   dns     Let's Encrypt DNS-01
	//   remote  证书由面板生成并下发，节点只负责落盘
	//
	// remote 模式用于「SNI 是伪装域名、真证书无法验证」的场景：
	// 面板生成长效自签证书 + 算出指纹，把证书下发给节点、把指纹写进订阅，
	// 客户端用指纹固定（xray 的 pinnedPeerCertSha256 / hysteria 的 pinSHA256）
	// 完成验证，既不需要真证书，也不必裸奔 allowInsecure
	// —— xray-core 已经移除 allowInsecure，配了会直接报错。
	CertMode         string            `json:"CertMode"`
	RejectUnknownSni bool              `json:"RejectUnknownSni"`
	CertDomain       string            `json:"CertDomain"`
	CertFile         string            `json:"CertFile"`
	KeyFile          string            `json:"KeyFile"`
	Provider         string            `json:"Provider"` // alidns, cloudflare, gandi, godaddy....
	Email            string            `json:"Email"`
	DNSEnv           map[string]string `json:"DNSEnv"`

	// TlsCert / TlsKey 仅在 remote 模式下使用，内容由面板下发（PEM）。
	// 不从本地 config.json 读取，故不参与 JSON 反序列化。
	TlsCert string `json:"-"`
	TlsKey  string `json:"-"`
}

func NewCertConfig() *CertConfig {
	return &CertConfig{
		CertMode: "none",
	}
}
