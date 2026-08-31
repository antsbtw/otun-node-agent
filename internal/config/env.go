package config

import (
	"os"
	"strconv"
	"time"
)

// LoadFromEnv 从环境变量加载配置
func LoadFromEnv() *AgentConfig {
	// 解析管理模式
	mode := ManagementMode(getEnv("MANAGEMENT_MODE", "local"))
	if mode != ModeLocal && mode != ModeRemote && mode != ModeHybrid {
		mode = ModeLocal // 默认使用本地模式
	}

	return &AgentConfig{
		APIURL:         getEnv("OTUN_API_URL", "https://saasapi.situstechnologies.com"),
		NodeAPIKey:     getEnv("NODE_API_KEY", ""),
		NodeID:         getEnv("NODE_ID", "node-default"),
		SyncInterval:   getDurationEnv("SYNC_INTERVAL", 60) * time.Second,
		StatsInterval:  getDurationEnv("STATS_INTERVAL", 60) * time.Second,
		VLESSPort:      getIntEnv("VLESS_PORT", 443),
		SSPort:         getIntEnv("SS_PORT", 8388),
		VmessPort:      getIntEnv("VMESS_PORT", 0),     // 0 表示未启用
		TrojanPort:     getIntEnv("TROJAN_PORT", 0),    // 0 表示未启用
		Hysteria2Port:  getIntEnv("HYSTERIA2_PORT", 0), // 0 表示未启用
		TuicPort:       getIntEnv("TUIC_PORT", 0),      // 0 表示未启用
		VpnDomain:      getEnv("VPN_DOMAIN", ""),       // VPN TLS 域名
		SingboxBin:     getEnv("SINGBOX_BIN", "/usr/local/bin/sing-box"),
		SingboxConfig:  getEnv("SINGBOX_CONFIG", "/etc/sing-box/config.json"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		ManagementMode: mode,
		ServerIP:       getEnv("SERVER_IP", ""),            // 服务器公网 IP，用于生成连接 URL
		TLSServiceKey:  getEnv("TLS_SERVICE_API_KEY", ""),  // TLS 服务 API Key (用于拉取证书)
		// 热更（WP-C）：默认值对齐 generator 生成的 hot_reload 块，正常无需配置。
		HotReloadAddr:   getEnv("HOT_RELOAD_ADDR", HotReloadAddr),
		VLESSInboundTag: getEnv("VLESS_INBOUND_TAG", VLESSInboundTag),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getIntEnv(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal int) time.Duration {
	return time.Duration(getIntEnv(key, defaultVal))
}

// HotReloadSupported 报告目标 sing-box 二进制是否认识 experimental.hot_reload 块。
//
// 背景（2026-08-31 生产事故）：hot_reload 是自研 fork（WP-A）才有的扩展，上游/旧二进制
// 解析到未知字段会 **FATAL 退出**（`json: unknown field "hot_reload"`），不是忽略。
// 而 install.sh 把 sing-box 钉死在 v1.10.7（rev 253b4193，2025-01，早于 fork 的热更实现），
// agent 却从 releases/latest 浮动更新 —— agent 一滚版本，新装节点的 sing-box 就必然启动失败，
// 陷入指数退避崩溃循环：8080 还活着、DB 里仍是 active，但 443/SS 全拒，付费用户一个都连不上。
//
// 故默认 **不发** 该块（对齐旧二进制，安全）；确认节点跑的是带热更的 fork 后，
// 用 SINGBOX_HOT_RELOAD=true 显式打开。
//
// 关掉只是让用户集变更退回整进程 reload（会断连），功能不丢——agent 侧本就有三重兜底：
// 端点不存在/调用失败一律降级回 reload。
func HotReloadSupported() bool {
	return os.Getenv("SINGBOX_HOT_RELOAD") == "true"
}
