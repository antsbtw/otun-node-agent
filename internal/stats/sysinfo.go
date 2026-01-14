package stats

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SystemLoad 系统负载信息
type SystemLoad struct {
	CPUPercent    float64
	MemoryPercent float64
}

// GetSystemLoad 获取系统负载（简化版，Linux 专用）
func GetSystemLoad() SystemLoad {
	load := SystemLoad{}

	// CPU 使用率（简化：使用 load average）
	load.CPUPercent = getCPUUsage()

	// 内存使用率
	load.MemoryPercent = getMemoryUsage()

	return load
}

func getCPUUsage() float64 {
	// 读取 /proc/loadavg
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}

	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}

	// 转换为百分比（基于 CPU 核心数）
	numCPU := float64(runtime.NumCPU())
	percent := (load1 / numCPU) * 100
	if percent > 100 {
		percent = 100
	}

	return percent
}

func getMemoryUsage() float64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()

	var memTotal, memAvailable int64

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		switch fields[0] {
		case "MemTotal:":
			memTotal = value
		case "MemAvailable:":
			memAvailable = value
		}
	}

	if memTotal == 0 {
		return 0
	}

	used := memTotal - memAvailable
	return float64(used) / float64(memTotal) * 100
}

// 公网 IP 缓存
var (
	cachedPublicIP   string
	publicIPMutex    sync.RWMutex
	publicIPLastTime time.Time
	publicIPCacheTTL = 5 * time.Minute // 缓存 5 分钟
)

// GetPublicIP 获取公网 IP（带缓存）
func GetPublicIP() string {
	publicIPMutex.RLock()
	if cachedPublicIP != "" && time.Since(publicIPLastTime) < publicIPCacheTTL {
		ip := cachedPublicIP
		publicIPMutex.RUnlock()
		return ip
	}
	publicIPMutex.RUnlock()

	// 需要刷新
	ip := fetchPublicIP()
	if ip != "" {
		publicIPMutex.Lock()
		cachedPublicIP = ip
		publicIPLastTime = time.Now()
		publicIPMutex.Unlock()
	}
	return ip
}

// fetchPublicIP 从外部服务获取公网 IP（优先 IPv4）
func fetchPublicIP() string {
	// 优先使用只返回 IPv4 的服务
	ipv4Services := []string{
		"https://api.ipify.org",      // 只返回 IPv4
		"https://ipv4.icanhazip.com", // 强制 IPv4
		"https://v4.ident.me",        // 强制 IPv4
	}

	// 备用服务（可能返回 IPv6）
	fallbackServices := []string{
		"https://ifconfig.me/ip",
		"https://ipinfo.io/ip",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// 先尝试获取 IPv4
	for _, url := range ipv4Services {
		if ip := fetchIPFromURL(client, url); ip != "" && isIPv4(ip) {
			return ip
		}
	}

	// 如果没有 IPv4，尝试备用服务
	for _, url := range fallbackServices {
		if ip := fetchIPFromURL(client, url); ip != "" {
			// 优先返回 IPv4
			if isIPv4(ip) {
				return ip
			}
		}
	}

	return ""
}

func fetchIPFromURL(client *http.Client, url string) string {
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}

	ip := strings.TrimSpace(string(body))
	// 简单验证是否像 IP 地址
	if len(ip) >= 7 && len(ip) <= 45 && (strings.Contains(ip, ".") || strings.Contains(ip, ":")) {
		return ip
	}
	return ""
}

func isIPv4(ip string) bool {
	// IPv4 只包含点，不包含冒号
	return strings.Contains(ip, ".") && !strings.Contains(ip, ":")
}
