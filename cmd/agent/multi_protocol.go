package main

import (
	"log"

	"otun-node-agent/internal/client"
	"otun-node-agent/internal/config"
)

// MultiProtocolContext 多协议上下文
type MultiProtocolContext struct {
	NodeConfig   *client.NodeConfigResponse
	CertManager  *config.CertManager
	TLSClient    *client.TLSClient
	Generator    *config.MultiProtocolGenerator
}

// initMultiProtocol 初始化多协议模式
// 优先使用本地环境变量配置（真实来源原则），manager 配置作为参考
func (a *Agent) initMultiProtocol(dataDir string) (*MultiProtocolContext, error) {
	log.Println("[MultiProtocol] Checking local multi-protocol configuration...")

	// 1. 首先检查本地环境变量是否配置了多协议端口
	// 这是真实来源：Agent 自己知道应该运行哪些协议
	localHasTLS := a.cfg.VmessPort > 0 || a.cfg.TrojanPort > 0 ||
		a.cfg.Hysteria2Port > 0 || a.cfg.TuicPort > 0

	if !localHasTLS {
		log.Println("[MultiProtocol] No TLS protocols configured locally, using standard mode")
		return nil, nil
	}

	log.Printf("[MultiProtocol] Local TLS ports - VMess: %d, Trojan: %d, Hysteria2: %d, TUIC: %d",
		a.cfg.VmessPort, a.cfg.TrojanPort, a.cfg.Hysteria2Port, a.cfg.TuicPort)

	// 2. 从 manager 获取节点配置（主要获取 TLS 服务 URL 等信息）
	log.Println("[MultiProtocol] Fetching node configuration from manager...")
	managerClient := client.NewManagerClient(a.cfg.APIURL, a.cfg.NodeAPIKey)
	nodeConfig, err := managerClient.GetNodeConfig()
	if err != nil {
		log.Printf("[MultiProtocol] Warning: Failed to get config from manager: %v", err)
		// 即使 manager 不可用，也尝试使用本地配置继续
		nodeConfig = &client.NodeConfigResponse{
			NodeID:    a.cfg.NodeID,
			VpnDomain: a.cfg.VpnDomain,
		}
	}

	// 3. 使用本地配置覆盖 manager 返回的端口配置（真实来源原则）
	// Agent 自己的环境变量配置才是权威的
	nodeConfig.VlessPort = a.cfg.VLESSPort
	nodeConfig.SSPort = a.cfg.SSPort
	nodeConfig.VmessPort = a.cfg.VmessPort
	nodeConfig.TrojanPort = a.cfg.TrojanPort
	nodeConfig.Hysteria2Port = a.cfg.Hysteria2Port
	nodeConfig.TuicPort = a.cfg.TuicPort
	if a.cfg.VpnDomain != "" {
		nodeConfig.VpnDomain = a.cfg.VpnDomain
	}

	// 更新协议列表
	nodeConfig.Protocols = []string{"vless", "shadowsocks"}
	if nodeConfig.VmessPort > 0 {
		nodeConfig.Protocols = append(nodeConfig.Protocols, "vmess")
	}
	if nodeConfig.TrojanPort > 0 {
		nodeConfig.Protocols = append(nodeConfig.Protocols, "trojan")
	}
	if nodeConfig.Hysteria2Port > 0 {
		nodeConfig.Protocols = append(nodeConfig.Protocols, "hysteria2")
	}
	if nodeConfig.TuicPort > 0 {
		nodeConfig.Protocols = append(nodeConfig.Protocols, "tuic")
	}

	log.Printf("[MultiProtocol] Node: %s, Protocols: %v, VPN domain: %s",
		nodeConfig.NodeID, nodeConfig.Protocols, nodeConfig.VpnDomain)

	// 4. 初始化证书管理器
	certManager := config.NewCertManager(dataDir)

	// 5. 如果需要 TLS 协议，获取证书
	var tlsClient *client.TLSClient
	if nodeConfig.TLSServiceURL != "" && nodeConfig.VpnDomain != "" {
		// 确定 API Key: 优先使用环境变量，否则使用 Node API Key
		apiKey := a.cfg.TLSServiceKey
		if apiKey == "" {
			apiKey = a.cfg.NodeAPIKey // 回退使用节点 API Key
		}

		tlsClient = client.NewTLSClient(nodeConfig.TLSServiceURL, apiKey)

		// 检查是否已有证书
		if !certManager.HasValidCert() {
			log.Println("[MultiProtocol] Fetching TLS certificate...")
			if err := certManager.FetchAndSaveCert(tlsClient, nodeConfig.VpnDomain); err != nil {
				log.Printf("[MultiProtocol] Warning: Failed to fetch certificate: %v", err)
				log.Println("[MultiProtocol] TLS protocols will be disabled")
				// 清除 TLS 协议，只保留基础协议
				nodeConfig.VmessPort = 0
				nodeConfig.TrojanPort = 0
				nodeConfig.Hysteria2Port = 0
				nodeConfig.TuicPort = 0
			}
		} else {
			log.Println("[MultiProtocol] Using existing TLS certificate")
		}
	}

	// 6. 创建多协议生成器
	certPath := ""
	keyPath := ""
	if certManager.HasValidCert() {
		certPath = certManager.GetCertPath()
		keyPath = certManager.GetKeyPath()
	}

	generator := config.NewMultiProtocolGenerator(
		nodeConfig,
		a.secrets.PrivateKey,
		a.secrets.ShortIDs,
		certPath,
		keyPath,
	)

	return &MultiProtocolContext{
		NodeConfig:  nodeConfig,
		CertManager: certManager,
		TLSClient:   tlsClient,
		Generator:   generator,
	}, nil
}

// generateMultiProtocolConfig 使用多协议生成器生成配置
func (a *Agent) generateMultiProtocolConfig(ctx *MultiProtocolContext, users []config.User, circuitBreakerEnabled bool) error {
	singboxCfg := ctx.Generator.Generate(users, circuitBreakerEnabled)
	return ctx.Generator.WriteToFile(singboxCfg, a.cfg.SingboxConfig)
}
