package config

import (
	"encoding/json"
	"fmt"
	"os"

	"otun-node-agent/internal/client"
)

// MultiProtocolGenerator 多协议配置生成器 (用于 remote 模式的 VPN 节点)
type MultiProtocolGenerator struct {
	nodeConfig *client.NodeConfigResponse
	privateKey string
	shortIDs   []string
	certPath   string // TLS 证书路径
	keyPath    string // TLS 私钥路径
}

// NewMultiProtocolGenerator 创建多协议配置生成器
func NewMultiProtocolGenerator(
	nodeConfig *client.NodeConfigResponse,
	privateKey string,
	shortIDs []string,
	certPath, keyPath string,
) *MultiProtocolGenerator {
	return &MultiProtocolGenerator{
		nodeConfig: nodeConfig,
		privateKey: privateKey,
		shortIDs:   shortIDs,
		certPath:   certPath,
		keyPath:    keyPath,
	}
}

// Generate 生成多协议 sing-box 配置
func (g *MultiProtocolGenerator) Generate(users []User, circuitBreakerEnabled bool) map[string]any {
	// 按协议分类用户
	var vlessUsers []map[string]any
	var ssUsers []map[string]any
	var vmessUsers []map[string]any
	var trojanUsers []map[string]any
	var hysteria2Users []map[string]any
	var tuicUsers []map[string]any
	var statsUsers []string

	for _, u := range users {
		if circuitBreakerEnabled || !u.Enabled {
			continue
		}

		statsUsers = append(statsUsers, u.UUID)

		for _, proto := range u.Protocols {
			switch proto {
			case "vless":
				vlessUsers = append(vlessUsers, map[string]any{
					"uuid": u.UUID,
					"flow": "xtls-rprx-vision",
				})
			case "shadowsocks":
				ssUsers = append(ssUsers, map[string]any{
					"name":     u.UUID,
					"password": u.SSPassword,
				})
			case "vmess":
				vmessUsers = append(vmessUsers, map[string]any{
					"uuid": u.UUID,
				})
			case "trojan":
				trojanUsers = append(trojanUsers, map[string]any{
					"password": u.UUID, // trojan 使用 UUID 作为密码
				})
			case "hysteria2":
				hysteria2Users = append(hysteria2Users, map[string]any{
					"password": u.UUID,
				})
			case "tuic":
				tuicUsers = append(tuicUsers, map[string]any{
					"uuid":     u.UUID,
					"password": u.SSPassword, // TUIC 使用 SS 密码
				})
			}
		}
	}

	config := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"outbounds": []map[string]any{
			{"type": "direct", "tag": "direct"},
		},
	}

	var inbounds []map[string]any

	// 1. VLESS + Reality (TCP 443)
	if g.nodeConfig.HasProtocol("vless") {
		vlessInbound := map[string]any{
			"type":        "vless",
			"tag":         "vless-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.VlessPort,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": g.nodeConfig.RealitySNI,
				"reality": map[string]any{
					"enabled": true,
					"handshake": map[string]any{
						"server":      g.nodeConfig.RealitySNI,
						"server_port": 443,
					},
					"private_key": g.privateKey,
					"short_id":    g.shortIDs,
				},
			},
		}
		if len(vlessUsers) > 0 {
			vlessInbound["users"] = vlessUsers
		} else {
			vlessInbound["users"] = []map[string]any{}
		}
		inbounds = append(inbounds, vlessInbound)
	}

	// 2. Shadowsocks (TCP/UDP)
	if g.nodeConfig.HasProtocol("shadowsocks") && len(ssUsers) > 0 {
		ssInbound := map[string]any{
			"type":        "shadowsocks",
			"tag":         "ss-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.SSPort,
			"method":      "chacha20-ietf-poly1305",
			"users":       ssUsers,
		}
		inbounds = append(inbounds, ssInbound)
	}

	// 以下协议需要 TLS 证书
	hasTLSCert := g.certPath != "" && g.keyPath != ""

	// 3. VMess + TLS (TCP 8443)
	if g.nodeConfig.HasProtocol("vmess") && g.nodeConfig.VmessPort > 0 && hasTLSCert {
		vmessInbound := map[string]any{
			"type":        "vmess",
			"tag":         "vmess-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.VmessPort,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": g.nodeConfig.VpnDomain,
				"certificate_path": g.certPath,
				"key_path":         g.keyPath,
			},
		}
		if len(vmessUsers) > 0 {
			vmessInbound["users"] = vmessUsers
		} else {
			vmessInbound["users"] = []map[string]any{}
		}
		inbounds = append(inbounds, vmessInbound)
	}

	// 4. Trojan (TCP 8444)
	if g.nodeConfig.HasProtocol("trojan") && g.nodeConfig.TrojanPort > 0 && hasTLSCert {
		trojanInbound := map[string]any{
			"type":        "trojan",
			"tag":         "trojan-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.TrojanPort,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": g.nodeConfig.VpnDomain,
				"certificate_path": g.certPath,
				"key_path":         g.keyPath,
			},
		}
		if len(trojanUsers) > 0 {
			trojanInbound["users"] = trojanUsers
		} else {
			trojanInbound["users"] = []map[string]any{}
		}
		inbounds = append(inbounds, trojanInbound)
	}

	// 5. Hysteria2 (UDP 8445)
	if g.nodeConfig.HasProtocol("hysteria2") && g.nodeConfig.Hysteria2Port > 0 && hasTLSCert {
		hysteria2Inbound := map[string]any{
			"type":        "hysteria2",
			"tag":         "hysteria2-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.Hysteria2Port,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": g.nodeConfig.VpnDomain,
				"certificate_path": g.certPath,
				"key_path":         g.keyPath,
			},
		}
		if len(hysteria2Users) > 0 {
			hysteria2Inbound["users"] = hysteria2Users
		} else {
			hysteria2Inbound["users"] = []map[string]any{}
		}
		inbounds = append(inbounds, hysteria2Inbound)
	}

	// 6. TUIC (UDP 8446)
	if g.nodeConfig.HasProtocol("tuic") && g.nodeConfig.TuicPort > 0 && hasTLSCert {
		tuicInbound := map[string]any{
			"type":        "tuic",
			"tag":         "tuic-in",
			"listen":      "::",
			"listen_port": g.nodeConfig.TuicPort,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": g.nodeConfig.VpnDomain,
				"certificate_path": g.certPath,
				"key_path":         g.keyPath,
			},
			"congestion_control": "bbr",
		}
		if len(tuicUsers) > 0 {
			tuicInbound["users"] = tuicUsers
		} else {
			tuicInbound["users"] = []map[string]any{}
		}
		inbounds = append(inbounds, tuicInbound)
	}

	config["inbounds"] = inbounds

	// V2Ray API for stats
	config["experimental"] = map[string]any{
		"v2ray_api": map[string]any{
			"listen": "127.0.0.1:10085",
			"stats": map[string]any{
				"enabled": true,
				"users":   statsUsers,
			},
		},
	}

	return config
}

// WriteToFile 将配置写入文件
func (g *MultiProtocolGenerator) WriteToFile(config map[string]any, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}
