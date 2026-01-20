package stats

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Collector 从 sing-box API 收集流量统计
type Collector struct {
	apiAddr    string
	httpClient *http.Client
}

// UserStats 用户流量统计
type UserStats struct {
	Upload   int64
	Download int64
}

// NewCollector 创建统计收集器
func NewCollector(apiAddr string) *Collector {
	return &Collector{
		apiAddr: apiAddr,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// ActiveConnection sing-box 返回的连接信息
type ActiveConnection struct {
	ID       string `json:"id"`
	Metadata struct {
		User        string `json:"user"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	} `json:"metadata"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Start    string `json:"start"`
}

// ConnectionsResponse sing-box connections API 响应
type ConnectionsResponse struct {
	Connections []ActiveConnection `json:"connections"`
}

// Collect 收集所有用户的流量统计
// 通过 /connections API 获取当前活跃连接的流量数据
func (c *Collector) Collect() (map[string]*UserStats, error) {
	url := fmt.Sprintf("http://%s/connections", c.apiAddr)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request connections: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("connections API returned %d", resp.StatusCode)
	}

	var result ConnectionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode connections: %w", err)
	}

	// 按用户聚合流量
	stats := make(map[string]*UserStats)

	for _, conn := range result.Connections {
		userUUID := conn.Metadata.User
		if userUUID == "" {
			continue
		}

		if _, ok := stats[userUUID]; !ok {
			stats[userUUID] = &UserStats{}
		}

		stats[userUUID].Upload += conn.Upload
		stats[userUUID].Download += conn.Download
	}

	return stats, nil
}
