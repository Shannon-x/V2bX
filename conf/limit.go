package conf

type LimitConfig struct {
	EnableRealtime          bool                     `json:"EnableRealtime"`
	SpeedLimit              int                      `json:"SpeedLimit"`
	IPLimit                 int                      `json:"DeviceLimit"`
	ConnLimit               int                      `json:"ConnLimit"`
	EnableIpRecorder        bool                     `json:"EnableIpRecorder"`
	IpRecorderConfig        *IpReportConfig          `json:"IpRecorderConfig"`
	EnableDynamicSpeedLimit bool                     `json:"EnableDynamicSpeedLimit"`
	DynamicSpeedLimitConfig *DynamicSpeedLimitConfig `json:"DynamicSpeedLimitConfig"`
	// BlockBittorrentUDP 打开后，xray 内核会对出站 UDP 逐包检测并丢弃
	// BitTorrent 报文（DHT/KRPC、uTP、UDP Tracker）。
	//
	// 之所以需要单独的开关而不是只靠 route.json 的
	// {"protocol":["bittorrent"]}：xray 的 UDP 入站对每个客户端会话只做一次
	// 嗅探和一次路由判定，之后同一会话内发往任意目标的包都不再经过路由，
	// 所以纯路由规则挡不住 DHT。面板下发 protocol 规则含 bittorrent 时会
	// 自动启用，无需手动开启。
	BlockBittorrentUDP bool `json:"BlockBittorrentUDP"`
}

type RecorderConfig struct {
	Url     string `json:"Url"`
	Token   string `json:"Token"`
	Timeout int    `json:"Timeout"`
}

type RedisConfig struct {
	Address  string `json:"Address"`
	Password string `json:"Password"`
	Db       int    `json:"Db"`
	Expiry   int    `json:"Expiry"`
}

type IpReportConfig struct {
	Periodic       int             `json:"Periodic"`
	Type           string          `json:"Type"`
	RecorderConfig *RecorderConfig `json:"RecorderConfig"`
	RedisConfig    *RedisConfig    `json:"RedisConfig"`
	EnableIpSync   bool            `json:"EnableIpSync"`
}

type DynamicSpeedLimitConfig struct {
	Periodic   int   `json:"Periodic"`
	Traffic    int64 `json:"Traffic"`
	SpeedLimit int   `json:"SpeedLimit"`
	ExpireTime int   `json:"ExpireTime"`
}
