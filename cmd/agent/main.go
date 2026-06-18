package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"otun-node-agent/internal/api"
	"otun-node-agent/internal/config"
	"otun-node-agent/internal/local"
	"otun-node-agent/internal/quota"
	"otun-node-agent/internal/singbox"
	"otun-node-agent/internal/stats"
)

// Agent 是主控制器
type Agent struct {
	cfg        *config.AgentConfig
	secrets    *config.NodeSecrets
	syncer     *config.Syncer
	cache      *config.Cache
	generator  *config.Generator
	manager    *singbox.Manager
	connMgr    *singbox.ConnectionManager
	hotReload  *singbox.HotReloadClient // WP-C：仅用户集变时走它（不 reload、不断连）
	monitor    *quota.Monitor
	collector  *stats.Collector
	reporter   *stats.Reporter

	// 本地用户管理
	localStore *local.Store
	localAPI   *api.LocalAPIServer

	// 多协议模式 (remote 模式 VPN 节点)
	multiProto *MultiProtocolContext
	dataDir    string // 数据目录路径

	currentVersion     string
	currentUserVersion string              // WP-C：上次下发的 user_version（双 version 分流用）
	currentParamVersion string             // WP-C：上次下发的 param_version
	currentUserSet     map[string]struct{} // WP-C：上次在线的 uuid 集，用于热更后算"被删用户"主动踢
	mu                 sync.RWMutex
}

func main() {
	log.Println("========================================")
	log.Println("  OTun Node Agent v1.1.0")
	log.Println("========================================")

	// 加载配置
	cfg := config.LoadFromEnv()
	if cfg.NodeAPIKey == "" {
		log.Fatal("NODE_API_KEY is required")
	}

	log.Printf("Node ID: %s", cfg.NodeID)
	log.Printf("Management Mode: %s", cfg.ManagementMode)

	// 只在远程/混合模式下显示 API URL
	if cfg.ManagementMode == config.ModeRemote || cfg.ManagementMode == config.ModeHybrid {
		log.Printf("API URL: %s", cfg.APIURL)
	}

	// 初始化 Agent
	agent, err := NewAgent(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize agent: %v", err)
	}

	// 设置优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutdown signal received...")
		cancel()
	}()

	// 启动 Agent
	agent.Run(ctx)
}

// NewAgent 创建新的 Agent 实例
func NewAgent(cfg *config.AgentConfig) (*Agent, error) {
	// 确保数据目录存在
	dataDir := "./data"
	statsCache := "./data/stats"
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(statsCache, 0755)

	// 加载或生成节点密钥（包含随机 SS 端口）
	secrets, err := config.LoadOrGenerateSecrets(dataDir)
	if err != nil {
		return nil, err
	}

	log.Printf("Reality Public Key: %s", secrets.PublicKey)
	log.Printf("Short ID: %s", secrets.ShortIDs[0])

	// 使用随机端口（如果环境变量未指定）
	ssPort := cfg.SSPort
	if ssPort == 8388 { // 默认值，使用随机端口
		ssPort = secrets.SSPort
	}
	log.Printf("VLESS Port: %d", cfg.VLESSPort)
	log.Printf("Shadowsocks Port: %d", ssPort)

	// 创建各个组件
	singboxAPIAddr := "127.0.0.1:10085"
	syncer := config.NewSyncer(cfg.APIURL, cfg.NodeAPIKey)
	cache := config.NewCache(dataDir)
	generator := config.NewGenerator(cfg.VLESSPort, ssPort, secrets.PrivateKey, secrets.ShortIDs)
	manager := singbox.NewManager(cfg.SingboxBin, cfg.SingboxConfig)
	connMgr := singbox.NewConnectionManager(singboxAPIAddr)
	hotReload := singbox.NewHotReloadClient(cfg.HotReloadAddr)
	collector := stats.NewCollector(singboxAPIAddr)
	reporter := stats.NewReporter(cfg.APIURL, cfg.NodeAPIKey, statsCache)

	agent := &Agent{
		cfg:       cfg,
		secrets:   secrets,
		syncer:    syncer,
		cache:     cache,
		generator: generator,
		manager:   manager,
		connMgr:   connMgr,
		hotReload: hotReload,
		collector: collector,
		reporter:  reporter,
		dataDir:   dataDir,
	}

	// 更新配置中的实际端口
	cfg.SSPort = ssPort

	// 创建限额监控器（带移除回调）
	agent.monitor = quota.NewMonitor(func(uuid, reason string) {
		log.Printf("User quota exceeded: %s (%s), kicking...", uuid, reason)
		if kicked, err := connMgr.KickUser(uuid); err != nil {
			log.Printf("Failed to kick user %s: %v", uuid, err)
		} else if kicked > 0 {
			log.Printf("Kicked %d connections for user %s", kicked, uuid)
		}
	})

	// 本地/混合模式：初始化本地用户存储
	if cfg.ManagementMode == config.ModeLocal || cfg.ManagementMode == config.ModeHybrid {
		agent.localStore = local.NewStore(dataDir, func() {
			// 用户变更回调：重新生成配置
			log.Println("Local users changed, regenerating config...")
			agent.regenerateConfig()
		})

		// 创建本地 API 服务
		nodeConfig := &api.NodeConfig{
			NodeID:    cfg.NodeID,
			ServerIP:  cfg.ServerIP,
			PublicKey: secrets.PublicKey,
			ShortID:   secrets.ShortIDs[0],
			VLESSPort: cfg.VLESSPort,
			SSPort:    ssPort,
			SSMethod:  "chacha20-ietf-poly1305",
		}
		agent.localAPI = api.NewLocalAPIServer(agent.localStore, cfg.NodeAPIKey, nodeConfig)

		log.Printf("Local management API enabled")
		if cfg.ServerIP != "" {
			log.Printf("Server IP: %s", cfg.ServerIP)
		} else {
			log.Printf("Warning: SERVER_IP not set, connection URLs will be incomplete")
		}
	}

	return agent, nil
}

// Run 启动 Agent 主循环
func (a *Agent) Run(ctx context.Context) {
	// 启动 HTTP 服务（健康检查 + 本地 API）
	a.startHTTPServer()

	// 根据管理模式执行不同的初始化
	switch a.cfg.ManagementMode {
	case config.ModeLocal:
		// 本地模式：只使用本地用户
		log.Println("Running in LOCAL mode")
		a.initLocalMode()

	case config.ModeRemote:
		// 远程模式：与原来行为一致
		log.Println("Running in REMOTE mode")
		a.initRemoteMode()

	case config.ModeHybrid:
		// 混合模式：本地 + 远程
		log.Println("Running in HYBRID mode")
		a.initHybridMode()
	}

	// 启动 sing-box
	if os.Getenv("SKIP_SINGBOX") != "true" {
		if err := a.manager.Start(); err != nil {
			log.Printf("Failed to start sing-box: %v", err)
		}
	} else {
		log.Println("SKIP_SINGBOX=true, skipping sing-box start")
	}

	// 启动主循环
	a.runMainLoop(ctx)
}

// startHTTPServer 启动 HTTP 服务
func (a *Agent) startHTTPServer() {
	mux := http.NewServeMux()

	// 健康检查
	healthServer := api.NewHealthServer(func() bool {
		return a.manager.IsRunning() || os.Getenv("SKIP_SINGBOX") == "true"
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthServer.HandleHealth(w, r)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		healthServer.HandleReady(w, r)
	})

	// 注册本地 API 路由（如果启用）
	if a.localAPI != nil {
		a.localAPI.RegisterRoutes(mux)
		log.Println("Local API routes registered")
	}

	go func() {
		log.Println("HTTP server starting on :8080")
		server := &http.Server{
			Addr:         ":8080",
			Handler:      mux,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		}
		if err := server.ListenAndServe(); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()
}

// initLocalMode 初始化本地模式
func (a *Agent) initLocalMode() {
	// 从本地用户生成配置
	a.regenerateConfig()
}

// initRemoteMode 初始化远程模式
func (a *Agent) initRemoteMode() {
	// 尝试初始化多协议模式 (如果 manager 返回了多协议配置)
	multiProto, err := a.initMultiProtocol(a.dataDir)
	if err != nil {
		log.Printf("Multi-protocol init failed (will use standard mode): %v", err)
	}
	a.multiProto = multiProto

	// 节点注册
	if err := a.register(); err != nil {
		log.Printf("Node registration failed: %v", err)
	}

	// 首次同步配置
	if err := a.syncAndApply(); err != nil {
		log.Printf("Initial sync failed: %v", err)
		if a.cache.HasCache() {
			log.Println("Using cached configuration...")
			if err := a.applyFromCache(); err != nil {
				log.Printf("Failed to apply cache: %v", err)
			}
		}
	}

	// 尝试上报缓存的统计
	if err := a.reporter.FlushCache(); err != nil {
		log.Printf("Failed to flush stats cache: %v", err)
	}
}

// initHybridMode 初始化混合模式
func (a *Agent) initHybridMode() {
	// 节点注册
	if err := a.register(); err != nil {
		log.Printf("Node registration failed: %v", err)
	}

	// 同步远程用户并合并本地用户
	if err := a.syncAndApplyHybrid(); err != nil {
		log.Printf("Initial sync failed: %v", err)
		// 回退到本地用户
		a.regenerateConfig()
	}

	// 尝试上报缓存的统计
	if err := a.reporter.FlushCache(); err != nil {
		log.Printf("Failed to flush stats cache: %v", err)
	}
}

// runMainLoop 主循环
func (a *Agent) runMainLoop(ctx context.Context) {
	// 根据模式决定是否启用远程同步定时器
	var syncTicker *time.Ticker
	var statsTicker *time.Ticker
	var heartbeatTicker *time.Ticker
	var connectionsTicker *time.Ticker

	if a.cfg.ManagementMode == config.ModeRemote || a.cfg.ManagementMode == config.ModeHybrid {
		syncTicker = time.NewTicker(a.cfg.SyncInterval)
		statsTicker = time.NewTicker(a.cfg.StatsInterval)
		heartbeatTicker = time.NewTicker(30 * time.Second)
		connectionsTicker = time.NewTicker(10 * time.Second)
		defer syncTicker.Stop()
		defer statsTicker.Stop()
		defer heartbeatTicker.Stop()
		defer connectionsTicker.Stop()
	}

	quotaTicker := time.NewTicker(10 * time.Second)
	defer quotaTicker.Stop()

	log.Printf("Agent is running (mode: %s)", a.cfg.ManagementMode)

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping agent...")
			if a.cfg.ManagementMode != config.ModeLocal {
				a.collectAndReport()
			}
			a.manager.Stop()
			return

		case <-quotaTicker.C:
			a.monitor.CheckAllUsers()

		default:
			// 远程/混合模式的定时任务
			if syncTicker != nil {
				select {
				case <-syncTicker.C:
					if a.cfg.ManagementMode == config.ModeHybrid {
						if err := a.syncAndApplyHybrid(); err != nil {
							log.Printf("Sync error: %v", err)
						}
					} else {
						if err := a.syncAndApply(); err != nil {
							log.Printf("Sync error: %v", err)
						}
					}
				case <-statsTicker.C:
					a.collectAndReport()
				case <-heartbeatTicker.C:
					a.sendHeartbeat()
				case <-connectionsTicker.C:
					a.reportConnections()
				default:
				}
			}

			// 小睡一下避免 CPU 空转
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// regenerateConfig 重新生成 sing-box 配置（从本地用户）
func (a *Agent) regenerateConfig() {
	if a.localStore == nil {
		return
	}

	localUsers := a.localStore.ListUsers()
	users := make([]config.User, 0, len(localUsers))

	for _, lu := range localUsers {
		users = append(users, config.User{
			UUID:         lu.UUID,
			Protocols:    lu.Protocols,
			SSPassword:   lu.SSPassword,
			Enabled:      lu.Enabled,
			TrafficLimit: lu.TrafficLimit,
			TrafficUsed:  lu.TrafficUsed,
			ExpireAt:     lu.ExpireAt,
		})
	}

	// 检查熔断状态
	circuitBreakerEnabled := a.localStore.IsCircuitBreakerEnabled()
	if circuitBreakerEnabled {
		log.Println("Circuit breaker is enabled, all users will be disabled")
	}

	log.Printf("Regenerating config with %d local users (circuit breaker: %v)", len(users), circuitBreakerEnabled)

	// 更新限额监控
	a.monitor.UpdateUsers(users)

	// 生成配置
	singboxCfg := a.generator.Generate(users, "www.microsoft.com", circuitBreakerEnabled)

	if err := a.generator.WriteToFile(singboxCfg, a.cfg.SingboxConfig); err != nil {
		log.Printf("Failed to write config: %v", err)
		return
	}

	// 重载 sing-box
	if a.manager.IsRunning() {
		log.Println("Reloading sing-box...")
		if err := a.manager.Reload(); err != nil {
			log.Printf("Failed to reload sing-box: %v", err)
		}
	}
}

// syncAndApplyHybrid 混合模式：同步远程用户并合并本地用户
func (a *Agent) syncAndApplyHybrid() error {
	log.Println("Syncing configuration (hybrid mode)...")

	// 获取远程用户
	resp, err := a.syncer.FetchUsers()
	if err != nil {
		return err
	}

	// 获取本地用户
	localUsers := a.localStore.ListUsers()

	// 合并用户列表（本地优先级更高）
	userMap := make(map[string]config.User)

	// 先添加远程用户
	for _, u := range resp.Users {
		userMap[u.UUID] = u
	}

	// 再添加本地用户（覆盖同 UUID 的远程用户）
	for _, lu := range localUsers {
		userMap[lu.UUID] = config.User{
			UUID:         lu.UUID,
			Protocols:    lu.Protocols,
			SSPassword:   lu.SSPassword,
			Enabled:      lu.Enabled,
			TrafficLimit: lu.TrafficLimit,
			TrafficUsed:  lu.TrafficUsed,
			ExpireAt:     lu.ExpireAt,
		}
	}

	// 转换为列表
	users := make([]config.User, 0, len(userMap))
	for _, u := range userMap {
		users = append(users, u)
	}

	log.Printf("Merged users: %d remote + %d local = %d total",
		len(resp.Users), len(localUsers), len(users))

	// 检查熔断状态（混合模式下也检查本地熔断）。
	// 熔断开启时所有用户被禁 → 这是"结构性变更"，必须 reload（不能热更，否则认证集与盘上不一致）。
	circuitBreakerEnabled := false
	if a.localStore != nil {
		circuitBreakerEnabled = a.localStore.IsCircuitBreakerEnabled()
	}

	// WP-C：本地用户合并参与最终用户集，故热更/reload 的判定与应用都基于合并后的 users。
	//   熔断开启 → 强制 reload（applyHybrid 内部不识别熔断，这里直接走 reload 分支）。
	if !circuitBreakerEnabled {
		a.mu.RLock()
		prev := a.snapshotState()
		a.mu.RUnlock()

		switch decideSyncAction(prev, resp) {
		case actionNone:
			log.Printf("Configuration unchanged (version: %s)", resp.Version)
			return nil
		case actionHotReload:
			log.Printf("User set changed (user_version: %s) → hot-reload (hybrid)", resp.UserVersion)
			return a.applyHybridReload(resp, users, circuitBreakerEnabled, true)
		default: // actionReload
			log.Printf("New configuration version: %s → reload (hybrid)", resp.Version)
			return a.applyHybridReload(resp, users, circuitBreakerEnabled, false)
		}
	}

	return a.applyHybridReload(resp, users, circuitBreakerEnabled, false)
}

// applyHybridReload 是 hybrid 模式的应用路径：合并用户集 + 熔断态 → 写盘 + 热更或 reload。
// hybrid 用基础 generator（带熔断参数），故不复用 syncAndApply 的 writeConfig（后者走 multiProto）。
// hot=true 且 sing-box 在跑 → 走热更端点（失败降级回 reload）；否则整进程 reload。
func (a *Agent) applyHybridReload(resp *config.UsersResponse, users []config.User, circuitBreakerEnabled, hot bool) error {
	a.monitor.UpdateUsers(users)

	if err := a.cache.SaveUsers(resp); err != nil {
		log.Printf("Failed to cache users: %v", err)
	}

	singboxCfg := a.generator.Generate(users, resp.Config.RealitySNI, circuitBreakerEnabled)
	if err := a.generator.WriteToFile(singboxCfg, a.cfg.SingboxConfig); err != nil {
		return err
	}

	uuids := enabledUUIDs(users)

	// 热更分支：sing-box 在跑且决策为热更 → 推全量集，失败降级 reload。
	if hot && a.manager.IsRunning() {
		removed := a.diffRemovedUsers(uuids)
		hrResp, err := a.hotReload.UpdateUsers(uuids)
		if err != nil {
			log.Printf("Hot-reload endpoint failed (%v) → FALL BACK TO RELOAD (drops connections)", err)
			// 落入下方 reload 分支。
		} else {
			a.mu.Lock()
			a.currentVersion = resp.Version
			a.currentUserVersion = resp.UserVersion
			a.currentParamVersion = resp.ParamVersion
			a.currentUserSet = uuidSet(uuids)
			a.mu.Unlock()
			if len(removed) > 0 {
				log.Printf("Hot-reload (hybrid): %d user(s) removed, kicking: %v", len(removed), removed)
				a.kickUsers(removed)
			}
			log.Printf("Hot-reload OK (hybrid): %d users live, billing_sync=%v", hrResp.UserCount, hrResp.BillingSync)
			return nil
		}
	}

	a.mu.Lock()
	a.currentVersion = resp.Version
	a.currentUserVersion = resp.UserVersion
	a.currentParamVersion = resp.ParamVersion
	a.currentUserSet = uuidSet(uuids)
	a.mu.Unlock()

	if a.manager.IsRunning() {
		log.Println("Reloading sing-box...")
		return a.manager.Reload()
	}

	return nil
}

// register 向管理服务器注册
func (a *Agent) register() error {
	// 使用多协议注册配置
	regCfg := &config.RegisterConfig{
		NodeID:        a.cfg.NodeID,
		PublicKey:     a.secrets.PublicKey,
		ShortIDs:      a.secrets.ShortIDs,
		VlessPort:     a.cfg.VLESSPort,
		SSPort:        a.cfg.SSPort,
		VmessPort:     a.cfg.VmessPort,     // 可选：VMess+TLS
		TrojanPort:    a.cfg.TrojanPort,    // 可选：Trojan
		Hysteria2Port: a.cfg.Hysteria2Port, // 可选：Hysteria2
		TuicPort:      a.cfg.TuicPort,      // 可选：TUIC
		VpnDomain:     a.cfg.VpnDomain,     // 可选：VPN TLS 域名
	}
	return a.syncer.RegisterWithConfig(regCfg)
}

// sendHeartbeat 发送心跳
func (a *Agent) sendHeartbeat() {
	// 获取系统负载
	sysLoad := stats.GetSystemLoad()

	// 获取连接数
	connections, _ := a.connMgr.GetActiveConnections()

	// 获取公网 IPv4 地址
	publicIP := stats.GetPublicIPv4()

	req := &config.HeartbeatRequest{
		NodeID:    a.cfg.NodeID,
		Timestamp: time.Now().UTC(),
		Load: config.NodeLoad{
			CPUPercent:        sysLoad.CPUPercent,
			MemoryPercent:     sysLoad.MemoryPercent,
			ActiveConnections: len(connections),
			UserCount:         a.monitor.GetUserCount(),
		},
		PublicIP: publicIP,
	}

	resp, err := a.syncer.Heartbeat(req)
	if err != nil {
		log.Printf("Heartbeat failed: %v", err)
		return
	}

	// 处理踢人指令
	if len(resp.KickUsers) > 0 {
		a.kickUsers(resp.KickUsers)
	}

	// 处理证书更新
	if resp.CertUpdate != nil {
		a.handleCertUpdate(resp.CertUpdate)
	}

	// 检查是否需要重新加载用户
	if resp.ReloadUsers {
		log.Println("Manager requested user reload")
		if a.cfg.ManagementMode == config.ModeHybrid {
			a.syncAndApplyHybrid()
		} else {
			a.syncAndApply()
		}
	}
}

// handleCertUpdate 处理证书更新
func (a *Agent) handleCertUpdate(certUpdate *config.CertUpdate) {
	log.Printf("[CertUpdate] Received certificate update for domain: %s", certUpdate.Domain)

	// 创建证书管理器并保存证书
	certMgr := config.NewCertManager(a.dataDir)
	if err := certMgr.SaveCertFromUpdate(certUpdate); err != nil {
		log.Printf("[CertUpdate] Failed to save certificate: %v", err)
		return
	}

	log.Printf("[CertUpdate] Certificate saved successfully, expires at: %s", certUpdate.ExpiresAt)

	// 确认证书更新
	if err := a.syncer.AckCertUpdate(a.cfg.NodeID); err != nil {
		log.Printf("[CertUpdate] Failed to acknowledge cert update: %v", err)
	} else {
		log.Println("[CertUpdate] Certificate update acknowledged")
	}

	// 重新加载 sing-box 以应用新证书
	if a.manager.IsRunning() {
		log.Println("[CertUpdate] Reloading sing-box to apply new certificate...")
		if err := a.manager.Reload(); err != nil {
			log.Printf("[CertUpdate] Failed to reload sing-box: %v", err)
		}
	}
}

// reportConnections 上报活跃连接
func (a *Agent) reportConnections() {
	connections, err := a.connMgr.GetActiveConnections()
	if err != nil {
		// sing-box 可能未运行
		return
	}

	if len(connections) == 0 {
		return
	}

	report := &config.ConnectionsReport{
		NodeID:      a.cfg.NodeID,
		Timestamp:   time.Now().UTC(),
		Connections: make([]config.Connection, 0, len(connections)),
	}

	for _, conn := range connections {
		// 解析客户端 IP
		clientIP := conn.Metadata.Source
		if host, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = host
		}

		// 解析连接时间
		connectedAt, _ := time.Parse(time.RFC3339, conn.Start)

		report.Connections = append(report.Connections, config.Connection{
			UserUUID:    conn.Metadata.User,
			ClientIP:    clientIP,
			ConnectedAt: connectedAt,
			Upload:      conn.Upload,
			Download:    conn.Download,
		})
	}

	resp, err := a.syncer.ReportConnections(report)
	if err != nil {
		log.Printf("Report connections failed: %v", err)
		return
	}

	// 处理踢人指令
	if len(resp.KickUsers) > 0 {
		a.kickUsers(resp.KickUsers)
	}
}

// kickUsers 踢掉指定用户
func (a *Agent) kickUsers(uuids []string) {
	for _, uuid := range uuids {
		uuid = strings.TrimSpace(uuid)
		if uuid == "" {
			continue
		}

		kicked, err := a.connMgr.KickUser(uuid)
		if err != nil {
			log.Printf("Failed to kick user %s: %v", uuid, err)
		} else if kicked > 0 {
			log.Printf("Kicked %d connections for user %s (by Manager)", kicked, uuid)
		}
	}
}

// syncAndApply 同步配置并应用（纯远程模式）。
//
// WP-C：按 decideSyncAction 分流——仅用户集变 → 热更（不断连）；节点参数变/首次/老 manager → reload。
func (a *Agent) syncAndApply() error {
	log.Println("Syncing configuration...")

	resp, err := a.syncer.FetchUsers()
	if err != nil {
		return err
	}

	a.mu.RLock()
	prev := a.snapshotState()
	a.mu.RUnlock()

	switch decideSyncAction(prev, resp) {
	case actionNone:
		log.Printf("Configuration unchanged (version: %s)", resp.Version)
		return nil
	case actionHotReload:
		log.Printf("User set changed (user_version: %s, %d users) → hot-reload", resp.UserVersion, len(resp.Users))
		return a.applyHotReload(resp, resp.Users)
	default: // actionReload
		log.Printf("New configuration version: %s (%d users) → reload", resp.Version, len(resp.Users))
		return a.applyReload(resp, resp.Users)
	}
}

// applyReload 写配置 + 缓存 + 整进程 reload（参数变 / 首次 / 老 manager 兜底 / 热更失败降级）。
// users 是最终生效的用户集（远程或合并后），与 syncAndApply/syncAndApplyHybrid 各自传入。
func (a *Agent) applyReload(resp *config.UsersResponse, users []config.User) error {
	a.monitor.UpdateUsers(users)

	if err := a.cache.SaveUsers(resp); err != nil {
		log.Printf("Failed to cache users: %v", err)
	}

	if err := a.writeConfig(resp, users); err != nil {
		return err
	}

	a.mu.Lock()
	a.currentVersion = resp.Version
	a.currentUserVersion = resp.UserVersion
	a.currentParamVersion = resp.ParamVersion
	a.currentUserSet = uuidSet(enabledUUIDs(users))
	a.mu.Unlock()

	if a.manager.IsRunning() {
		log.Println("Reloading sing-box...")
		return a.manager.Reload()
	}

	return nil
}

// applyHotReload 仅用户集变：把全量 uuid 集推给运行中的 sing-box（不 reload、不断连）。
// 仍同步缓存 + 限额视图 + 盘上配置（防 sing-box 崩溃重启后从旧盘配置恢复丢新用户——热更只改内存）。
// 任一前提不满足或端点调用失败 → 降级回 applyReload，保证最终一致（三重兜底）。
func (a *Agent) applyHotReload(resp *config.UsersResponse, users []config.User) error {
	// 兜底1：sing-box 未运行 → 没有进程可热更，退回 reload 路径（其内部 IsRunning=false 时只写盘）。
	if !a.manager.IsRunning() {
		log.Println("Hot-reload requested but sing-box not running → fall back to reload (write config only)")
		return a.applyReload(resp, users)
	}

	a.monitor.UpdateUsers(users)

	// 兜底2：盘上配置必须随热更同步（防崩溃重启丢新用户）。写失败则不热更，退回 reload。
	if err := a.writeConfig(resp, users); err != nil {
		log.Printf("Hot-reload: failed to persist config to disk (%v) → fall back to reload", err)
		return a.applyReload(resp, users)
	}

	// 推全量 user 集（仅 enabled，对齐 generator 的 inbound users 过滤）。
	uuids := enabledUUIDs(users)

	// 算"本次掉出集合"的用户（删除/禁用）——热更只移除其认证，已建连接不会自动断，需主动踢。
	removed := a.diffRemovedUsers(uuids)

	hrResp, err := a.hotReload.UpdateUsers(uuids)
	if err != nil {
		// 兜底3：端点拒绝/非 200/老二进制 (v1.10.7) 无端点 → 降级回 reload + 醒目日志。
		log.Printf("Hot-reload endpoint failed (%v) → FALL BACK TO RELOAD (drops connections)", err)
		return a.applyReload(resp, users)
	}

	// 热更成功：同步缓存 + version 快照。
	if err := a.cache.SaveUsers(resp); err != nil {
		log.Printf("Failed to cache users: %v", err)
	}
	a.mu.Lock()
	a.currentVersion = resp.Version
	a.currentUserVersion = resp.UserVersion
	a.currentParamVersion = resp.ParamVersion
	a.currentUserSet = uuidSet(uuids)
	a.mu.Unlock()

	// 被删用户的现有连接不会因热更自动断 → 主动踢（不影响其他在线用户）。
	if len(removed) > 0 {
		log.Printf("Hot-reload: %d user(s) removed, kicking their live connections: %v", len(removed), removed)
		a.kickUsers(removed)
	}

	log.Printf("Hot-reload OK: %d users live, billing_sync=%v (no connections dropped)", hrResp.UserCount, hrResp.BillingSync)
	return nil
}

// writeConfig 按是否有多协议上下文选择生成器写盘（reload/hot-reload 共用，避免逻辑漂移）。
func (a *Agent) writeConfig(resp *config.UsersResponse, users []config.User) error {
	if a.multiProto != nil {
		return a.generateMultiProtocolConfig(a.multiProto, users, false)
	}
	singboxCfg := a.generator.Generate(users, resp.Config.RealitySNI, false)
	return a.generator.WriteToFile(singboxCfg, a.cfg.SingboxConfig)
}

// syncAction 是同步路径的决策结果。
type syncAction int

const (
	actionNone      syncAction = iota // 配置无变化，不动
	actionHotReload                   // 仅用户集变 → 热更（不断连）
	actionReload                      // 参数变 / 首次 / 老 manager 兜底 → 整进程 reload
)

// syncState 是 agent 本地缓存的上一次下发的版本快照（决策输入，纯数据便于测试）。
type syncState struct {
	version      string
	userVersion  string
	paramVersion string
}

// snapshotState 取当前 version 快照（调用方持锁）。
func (a *Agent) snapshotState() syncState {
	return syncState{
		version:      a.currentVersion,
		userVersion:  a.currentUserVersion,
		paramVersion: a.currentParamVersion,
	}
}

// decideSyncAction 纯决策：对比本地缓存版本与新响应，决定走 reload / 热更 / 不动。
// 抽成纯函数便于单测（不依赖 sing-box 进程、syncer、缓存）。
//
//   - 双 version 缺失（当前 otun-manager 标准分支只下发顶层 version）→ 退回顶层 version 判定，
//     变则一律 reload（安全侧，行为同今天）。要走热更需 manager 侧补 user_version/param_version。
//   - param_version 变（协议/Reality/端口/证书等）→ reload（inbound 重建无法热更）。
//   - 仅 user_version 变（param_version 不变）→ 热更。
//   - 都不变 → 不动。
func decideSyncAction(prev syncState, resp *config.UsersResponse) syncAction {
	dualVersion := resp.UserVersion != "" && resp.ParamVersion != ""

	if !dualVersion {
		if prev.version == resp.Version {
			return actionNone
		}
		return actionReload
	}

	if prev.paramVersion != resp.ParamVersion {
		return actionReload
	}
	if prev.userVersion != resp.UserVersion {
		return actionHotReload
	}
	return actionNone
}

// diffRemovedUsers 返回"上次在集合、本次不在"的 UUID（删除/禁用的用户），供热更后主动踢连接。
func (a *Agent) diffRemovedUsers(newUUIDs []string) []string {
	next := uuidSet(newUUIDs)
	a.mu.RLock()
	prev := a.currentUserSet
	a.mu.RUnlock()

	var removed []string
	for uuid := range prev {
		if _, ok := next[uuid]; !ok {
			removed = append(removed, uuid)
		}
	}
	return removed
}

// uuidSet 把 UUID 切片转成集合。
func uuidSet(uuids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(uuids))
	for _, u := range uuids {
		set[u] = struct{}{}
	}
	return set
}

// enabledUUIDs 提取启用用户的 UUID 列表（对齐 generator 的 inbound users 过滤：仅 Enabled）。
func enabledUUIDs(users []config.User) []string {
	uuids := make([]string, 0, len(users))
	for _, u := range users {
		if u.Enabled {
			uuids = append(uuids, u.UUID)
		}
	}
	return uuids
}

// applyFromCache 从缓存应用配置
func (a *Agent) applyFromCache() error {
	resp, err := a.cache.LoadUsers()
	if err != nil {
		return err
	}

	a.monitor.UpdateUsers(resp.Users)

	// 根据是否有多协议上下文选择不同的生成器
	if a.multiProto != nil {
		return a.generateMultiProtocolConfig(a.multiProto, resp.Users, false)
	}

	// 标准模式
	singboxCfg := a.generator.Generate(resp.Users, resp.Config.RealitySNI, false)
	return a.generator.WriteToFile(singboxCfg, a.cfg.SingboxConfig)
}

// collectAndReport 收集并上报统计
func (a *Agent) collectAndReport() {
	log.Println("Collecting stats...")
	userStats, err := a.collector.Collect()
	if err != nil {
		log.Printf("Failed to collect stats: %v", err)
		return
	}

	if len(userStats) == 0 {
		log.Println("No stats to report (no active users with traffic)")
		return
	}

	// 检查每个用户的流量限制，超限用户会被踢出
	for uuid, stat := range userStats {
		traffic := stat.Upload + stat.Download
		if traffic > 0 {
			if !a.monitor.CheckUser(uuid, traffic) {
				log.Printf("User %s failed quota check during stats collection", uuid)
			}
		}
	}

	log.Printf("Reporting stats for %d users", len(userStats))

	if err := a.reporter.Report(userStats); err != nil {
		log.Printf("Failed to report stats: %v", err)
	} else {
		a.monitor.ResetSessionTraffic()

		if a.reporter.GetCacheCount() > 0 {
			if err := a.reporter.FlushCache(); err != nil {
				log.Printf("Failed to flush stats cache: %v", err)
			}
		}
	}
}
