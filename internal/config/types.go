package config

import "time"

const (
	// HotReloadAddr 是 WP-A fork sing-box 暴露的本地热更端点监听地址（仅本机 agent 调，绝不外暴露）。
	// agent 把当前应在线的全量 uuid 集 POST 到 http://HotReloadAddr/hotreload/users → 无损换 VLESS
	// 认证 map + v2ray_api 计费集，不 reload、不断连。与 generator 生成的 hot_reload.listen 一致。
	HotReloadAddr = "127.0.0.1:10086"
	// VLESSInboundTag 是标准节点 VLESS inbound 的 tag（两个 generator 均用 "vless-in"）。
	// 热更端点据此定位要换认证 map 的 inbound。
	VLESSInboundTag = "vless-in"
	// HY2InboundTag 是标准节点 Hysteria2 inbound 的 tag（generator_multi 用 "hysteria2-in"）。
	// hy2 与 vless 同 name==password==UUID 语义，可一并热更（占实际流量 ~36%，最大头之一）。
	// 注意：hy2 inbound 条件生成（需 TLS 证书 + 启用 hy2），故只在它真生成时才加进 inbound_tags。
	HY2InboundTag = "hysteria2-in"
	// TrojanInboundTag 是标准节点 Trojan inbound 的 tag（generator_multi 用 "trojan-in"）。
	// trojan name==password==UUID（generator 即如此生成），与热更端点 UpdateUsers(uuids,uuids) 契约一致。
	// 占实际流量最大头（~43%）。同 hy2，条件生成（需 TLS 证书），只在真生成时加进 inbound_tags。
	TrojanInboundTag = "trojan-in"
	// VMessInboundTag 是标准节点 VMess inbound 的 tag（generator_multi 用 "vmess-in"）。
	// otun vmess 用户 name=uuid、alterId=0（现代 AEAD），与热更端点 UpdateUsers(uuids,uuids) 契约一致。
	// 占实际流量 ~8%。条件生成（需 TLS 证书），只在真生成时加进 inbound_tags。
	VMessInboundTag = "vmess-in"
)

// ManagementMode 管理模式
type ManagementMode string

const (
	// ModeLocal 本地管理模式：只使用本地 API 管理用户
	ModeLocal ManagementMode = "local"
	// ModeRemote 远程管理模式：从上游服务器同步用户
	ModeRemote ManagementMode = "remote"
	// ModeHybrid 混合模式：本地 + 远程用户合并
	ModeHybrid ManagementMode = "hybrid"
)

// AgentConfig 是 Agent 的运行配置
type AgentConfig struct {
	APIURL         string
	NodeAPIKey     string
	NodeID         string
	SyncInterval   time.Duration
	StatsInterval  time.Duration
	VLESSPort      int
	SSPort         int
	VmessPort      int    // VMess+TLS 端口 (多协议 VPN)
	TrojanPort     int    // Trojan 端口 (多协议 VPN)
	Hysteria2Port  int    // Hysteria2 端口 (多协议 VPN)
	TuicPort       int    // TUIC 端口 (多协议 VPN)
	VpnDomain      string // VPN TLS 域名 (多协议 VPN)
	SingboxBin     string
	SingboxConfig  string
	LogLevel       string
	ManagementMode ManagementMode // 管理模式
	ServerIP       string         // 服务器公网 IP（用于生成连接 URL）
	SingboxAPIAddr string         // sing-box V2Ray API 地址（本地统计端口）

	// 热更（WP-C）：仅用户集变时走热更端点不重启 sing-box。
	HotReloadAddr   string // fork sing-box 本地热更端点（默认 HotReloadAddr 常量）
	VLESSInboundTag string // 热更目标 inbound tag（默认 VLESSInboundTag 常量）

	// 多协议模式 (remote 模式动态获取)
	TLSServiceURL  string // TLS 服务地址 (从 manager 获取)
	TLSServiceKey  string // TLS 服务 API Key
}

// User 是从管理服务器获取的用户信息
type User struct {
	UUID          string     `json:"uuid"`
	Protocols     []string   `json:"protocols"`
	SSPassword    string     `json:"ss_password"`
	Enabled       bool       `json:"enabled"`
	TrafficLimit  int64      `json:"traffic_limit"`
	TrafficUsed   int64      `json:"traffic_used"`
	ExpireAt      *time.Time `json:"expire_at"`
	DeviceID      string     `json:"device_id"`       // 绑定的设备指纹
}

// UsersResponse 是管理服务器返回的用户列表
//
// 双 version（WP-C 热更分流）：
//   - Version      = 合并/顶层哈希（兼容字段，老 manager 只有这一个）。
//   - UserVersion  = 仅用户集稳定投影（剔 traffic_used + 按 uuid 排序）。只它变 → 热更（不断连）。
//   - ParamVersion = 节点参数（协议列表/Reality SNI/端口/证书等"变了必须 reload"的项）。它变 → reload。
//
// ★跨仓依赖：当前 otun-manager 标准分支 handleGetUsers 只下发 Version 单字段，UserVersion/ParamVersion
//   为空。此时 decideSyncAction 退回"顶层 version 变即 reload"的安全路径（行为同今天）。要让"仅用户变
//   走热更"真正生效，需 manager 侧补这两字段（类似 realm WP-2 三 version 拆分），不在本 WP-C agent 仓内。
type UsersResponse struct {
	Version      string `json:"version"`
	UserVersion  string `json:"user_version"`
	ParamVersion string `json:"param_version"`
	Users        []User `json:"users"`
	Config       struct {
		RealitySNI string `json:"reality_sni"`
	} `json:"config"`
}

// StatsEntry 是单个用户的流量统计
type StatsEntry struct {
	UUID     string `json:"uuid"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

// StatsReport 是上报的流量统计
type StatsReport struct {
	Timestamp time.Time    `json:"timestamp"`
	Stats     []StatsEntry `json:"stats"`
}

// Connection 活跃连接信息
type Connection struct {
	UserUUID    string    `json:"user_uuid"`
	ClientIP    string    `json:"client_ip"`
	ConnectedAt time.Time `json:"connected_at"`
	Upload      int64     `json:"upload"`
	Download    int64     `json:"download"`
}

// ConnectionsReport 连接上报
type ConnectionsReport struct {
	NodeID      string       `json:"node_id"`
	Timestamp   time.Time    `json:"timestamp"`
	Connections []Connection `json:"connections"`
}

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	Load      NodeLoad  `json:"load"`
	PublicIP  string    `json:"public_ip,omitempty"` // 公网 IPv4 地址
}

// NodeLoad 节点负载信息
type NodeLoad struct {
	CPUPercent        float64 `json:"cpu_percent"`
	MemoryPercent     float64 `json:"memory_percent"`
	BandwidthMbps     int     `json:"bandwidth_mbps"`
	ActiveConnections int     `json:"active_connections"`
	UserCount         int     `json:"user_count"`
}

// HeartbeatResponse 心跳响应
type HeartbeatResponse struct {
	OK          bool        `json:"ok"`
	KickUsers   []string    `json:"kick_users"`             // 需要踢掉的用户
	ReloadUsers bool        `json:"reload_users"`           // 是否需要重新拉取用户列表
	CertUpdate  *CertUpdate `json:"cert_update,omitempty"`  // 证书更新 (如果有)
}

// CertUpdate 证书更新信息
type CertUpdate struct {
	Domain    string `json:"domain"`     // 域名
	Cert      string `json:"cert"`       // 证书内容 (PEM)
	Key       string `json:"key"`        // 私钥内容 (PEM)
	ExpiresAt string `json:"expires_at"` // 过期时间 (ISO8601)
}
