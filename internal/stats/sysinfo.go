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

// IPv4 缓存
var (
	cachedIPv4     string
	ipv4Mutex      sync.RWMutex
	ipv4LastUpdate time.Time
	ipv4CacheTTL   = 5 * time.Minute
)

// GetPublicIPv4 获取公网 IPv4 地址（带缓存，只返回 IPv4）
func GetPublicIPv4() string {
	ipv4Mutex.RLock()
	if cachedIPv4 != "" && time.Since(ipv4LastUpdate) < ipv4CacheTTL {
		ip := cachedIPv4
		ipv4Mutex.RUnlock()
		return ip
	}
	ipv4Mutex.RUnlock()

	ip := fetchPublicIPv4()
	if ip != "" {
		ipv4Mutex.Lock()
		cachedIPv4 = ip
		ipv4LastUpdate = time.Now()
		ipv4Mutex.Unlock()
	}
	return ip
}

// fetchPublicIPv4 从外部服务获取 IPv4 地址
func fetchPublicIPv4() string {
	// 只使用返回 IPv4 的服务
	services := []string{
		"https://api.ipify.org",      // 只返回 IPv4
		"https://ipv4.icanhazip.com", // 强制 IPv4
		"https://v4.ident.me",        // 强制 IPv4
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, url := range services {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil {
			continue
		}

		ip := strings.TrimSpace(string(body))
		// 验证是 IPv4（包含点，不包含冒号）
		if isIPv4(ip) {
			return ip
		}
	}

	return ""
}

// isIPv4 检查是否为 IPv4 地址
func isIPv4(ip string) bool {
	return len(ip) >= 7 && len(ip) <= 15 && strings.Contains(ip, ".") && !strings.Contains(ip, ":")
}
