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

// initMultiProtocol 初始化多协议模式 (当 remote 模式且 manager 返回多协议配置时)
func (a *Agent) initMultiProtocol(dataDir string) (*MultiProtocolContext, error) {
	log.Println("[MultiProtocol] Fetching node configuration from manager...")

	// 1. 从 manager 获取节点配置
	managerClient := client.NewManagerClient(a.cfg.APIURL, a.cfg.NodeAPIKey)
	nodeConfig, err := managerClient.GetNodeConfig()
	if err != nil {
		return nil, err
	}

	log.Printf("[MultiProtocol] Node: %s, Protocols: %v", nodeConfig.NodeID, nodeConfig.Protocols)

	// 如果只有基础协议 (vless + shadowsocks)，不需要多协议模式
	if !nodeConfig.HasTLSProtocol() {
		log.Println("[MultiProtocol] No TLS protocols enabled, using standard mode")
		return nil, nil
	}

	log.Printf("[MultiProtocol] TLS protocols detected, VPN domain: %s", nodeConfig.VpnDomain)

	// 2. 初始化证书管理器
	certManager := config.NewCertManager(dataDir)

	// 3. 如果需要 TLS 协议，获取证书
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

	// 4. 创建多协议生成器
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
