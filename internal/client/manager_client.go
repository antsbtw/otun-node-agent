package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ManagerClient otun-manager 客户端
type ManagerClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewManagerClient 创建 Manager 客户端
func NewManagerClient(baseURL, apiKey string) *ManagerClient {
	return &ManagerClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NodeConfigResponse 节点配置响应
type NodeConfigResponse struct {
	NodeID        string   `json:"node_id"`
	Protocols     []string `json:"protocols"`       // 启用的协议: ["vless", "shadowsocks", "vmess", "trojan", "hysteria2", "tuic"]
	VpnDomain     string   `json:"vpn_domain"`      // VPN TLS 域名
	TLSServiceURL string   `json:"tls_service_url"` // TLS 服务地址
	RealitySNI    string   `json:"reality_sni"`     // Reality SNI
	VlessPort     int      `json:"vless_port"`
	SSPort        int      `json:"ss_port"`
	VmessPort     int      `json:"vmess_port,omitempty"`
	TrojanPort    int      `json:"trojan_port,omitempty"`
	Hysteria2Port int      `json:"hysteria2_port,omitempty"`
	TuicPort      int      `json:"tuic_port,omitempty"`
}

// GetNodeConfig 获取节点自身配置
func (c *ManagerClient) GetNodeConfig() (*NodeConfigResponse, error) {
	url := fmt.Sprintf("%s/api/node/config", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var configResp NodeConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&configResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &configResp, nil
}

// HasTLSProtocol 检查是否启用了需要 TLS 证书的协议
func (cfg *NodeConfigResponse) HasTLSProtocol() bool {
	for _, p := range cfg.Protocols {
		switch p {
		case "vmess", "trojan", "hysteria2", "tuic":
			return true
		}
	}
	return false
}

// HasProtocol 检查是否启用了指定协议
func (cfg *NodeConfigResponse) HasProtocol(protocol string) bool {
	for _, p := range cfg.Protocols {
		if p == protocol {
			return true
		}
	}
	return false
}
