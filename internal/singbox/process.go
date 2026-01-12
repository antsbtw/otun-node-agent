package singbox

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	// sing-box V2Ray API 端口
	apiPort = "127.0.0.1:10085"
	// 最大重启尝试次数
	maxRestartAttempts = 5
	// 端口检查最大等待时间
	maxPortWaitTime = 30 * time.Second
)

// Manager 管理 sing-box 进程
type Manager struct {
	binPath        string
	configPath     string
	cmd            *exec.Cmd
	mu             sync.Mutex
	running        bool
	restartCount   int       // 连续重启计数
	lastRestartAt  time.Time // 上次重启时间
	stopRequested  bool      // 是否正在停止（避免 monitor 重启）
}

// NewManager 创建进程管理器
func NewManager(binPath, configPath string) *Manager {
	return &Manager{
		binPath:    binPath,
		configPath: configPath,
	}
}

// Start 启动 sing-box 进程
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.startLocked()
}

// startLocked 内部启动函数（调用方需持有锁）
func (m *Manager) startLocked() error {
	if m.running {
		return fmt.Errorf("sing-box is already running")
	}

	// 检查配置文件是否存在
	if _, err := os.Stat(m.configPath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found: %s", m.configPath)
	}

	// 检查 sing-box 二进制是否存在
	if _, err := os.Stat(m.binPath); os.IsNotExist(err) {
		return fmt.Errorf("sing-box binary not found: %s", m.binPath)
	}

	// 等待端口可用（替代 pkill 暴力方案）
	if err := m.waitForPortAvailable(); err != nil {
		return fmt.Errorf("port not available: %w", err)
	}

	m.cmd = exec.Command(m.binPath, "run", "-c", m.configPath)
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}

	m.running = true
	m.stopRequested = false
	log.Printf("sing-box started with PID %d", m.cmd.Process.Pid)

	// 监控进程退出
	go m.monitor()

	return nil
}

// waitForPortAvailable 等待 API 端口可用
func (m *Manager) waitForPortAvailable() error {
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxPortWaitTime {
			return fmt.Errorf("timeout waiting for port %s to be available", apiPort)
		}

		// 尝试连接端口
		conn, err := net.DialTimeout("tcp", apiPort, 100*time.Millisecond)
		if err != nil {
			// 连接失败说明端口未被占用，可以启动
			return nil
		}
		conn.Close()

		// 端口被占用，等待后重试
		log.Printf("Port %s is still in use, waiting...", apiPort)
		time.Sleep(time.Second)
	}
}

// Stop 停止 sing-box 进程
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.stopLocked()
}

// stopLocked 内部停止函数（调用方需持有锁）
func (m *Manager) stopLocked() error {
	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		m.running = false
		return nil
	}

	m.stopRequested = true
	log.Println("Stopping sing-box...")

	// 发送 SIGTERM
	if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("SIGTERM failed: %v, trying SIGKILL", err)
		m.cmd.Process.Kill()
	}

	// 等待进程退出
	done := make(chan error, 1)
	go func() {
		done <- m.cmd.Wait()
	}()

	select {
	case <-done:
		log.Println("sing-box stopped gracefully")
	case <-time.After(5 * time.Second):
		m.cmd.Process.Kill()
		// 等待 Kill 生效
		select {
		case <-done:
			log.Println("sing-box force killed")
		case <-time.After(2 * time.Second):
			log.Println("sing-box kill timeout, process may be zombie")
		}
	}

	m.running = false
	m.cmd = nil

	// 等待端口释放
	time.Sleep(500 * time.Millisecond)

	return nil
}

// Reload 重载配置（重启 sing-box 进程）
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return fmt.Errorf("sing-box is not running")
	}

	log.Println("Reloading sing-box config...")

	// 先停止旧进程
	if err := m.stopLocked(); err != nil {
		return fmt.Errorf("stop sing-box for reload: %w", err)
	}

	// 重置重启计数（这是主动 reload，不是崩溃重启）
	m.restartCount = 0

	// 启动新进程
	if err := m.startLocked(); err != nil {
		return fmt.Errorf("start sing-box after reload: %w", err)
	}

	return nil
}

// IsRunning 检查是否运行中
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// monitor 监控进程状态，崩溃时自动重启（带指数退避）
func (m *Manager) monitor() {
	if m.cmd == nil {
		return
	}

	// 保存当前 cmd 的引用，避免竞态
	cmd := m.cmd
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果是主动停止请求，不要重启
	if m.stopRequested {
		m.running = false
		return
	}

	// 关键检查：如果当前 m.cmd 已经不是我们监控的那个，说明已经被 Reload 替换了
	// 这种情况下不应该触发重启逻辑
	if m.cmd != cmd {
		log.Printf("monitor: cmd has been replaced, skipping restart logic")
		return
	}

	wasRunning := m.running
	m.running = false

	if !wasRunning {
		return
	}

	log.Printf("sing-box exited unexpectedly: %v", err)

	// 检查是否在短时间内频繁重启（10秒内）
	if time.Since(m.lastRestartAt) < 10*time.Second {
		m.restartCount++
	} else {
		// 超过10秒，重置计数
		m.restartCount = 1
	}
	m.lastRestartAt = time.Now()

	// 检查重启次数限制
	if m.restartCount > maxRestartAttempts {
		log.Printf("sing-box has crashed %d times in quick succession, giving up auto-restart", m.restartCount)
		log.Println("Manual intervention required. Check logs and restart the service.")
		return
	}

	// 指数退避：2^n 秒，最大 32 秒
	backoffSeconds := 1 << m.restartCount // 2, 4, 8, 16, 32
	if backoffSeconds > 32 {
		backoffSeconds = 32
	}

	log.Printf("Attempting restart %d/%d in %d seconds...", m.restartCount, maxRestartAttempts, backoffSeconds)

	// 释放锁后等待
	m.mu.Unlock()
	time.Sleep(time.Duration(backoffSeconds) * time.Second)
	m.mu.Lock()

	// 再次检查是否有停止请求
	if m.stopRequested {
		return
	}

	// 尝试重启
	if err := m.startLocked(); err != nil {
		log.Printf("Failed to restart sing-box: %v", err)
	} else {
		log.Println("sing-box restarted successfully")
	}
}

// CheckConfig 验证配置文件
func (m *Manager) CheckConfig() error {
	cmd := exec.Command(m.binPath, "check", "-c", m.configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("config check failed: %s", string(output))
	}
	return nil
}

// GetRestartCount 获取当前重启计数（用于监控）
func (m *Manager) GetRestartCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restartCount
}
