package stats

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	statsService "github.com/v2fly/v2ray-core/v5/app/stats/command"
)

// Collector 从 sing-box V2Ray API (gRPC) 收集流量统计
type Collector struct {
	apiAddr string
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
	}
}

// Collect 收集所有用户的流量统计
// 通过 gRPC 访问 V2Ray Stats API
func (c *Collector) Collect() (map[string]*UserStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, c.apiAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to grpc: %w", err)
	}
	defer conn.Close()

	client := statsService.NewStatsServiceClient(conn)

	// 查询所有统计数据
	resp, err := client.QueryStats(ctx, &statsService.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  true, // 重置统计，避免重复计算
	})
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}

	// 解析统计数据
	// V2Ray stats 格式: user>>>uuid>>>traffic>>>uplink 或 user>>>uuid>>>traffic>>>downlink
	stats := make(map[string]*UserStats)

	for _, stat := range resp.Stat {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}

		uuid := parts[1]
		direction := parts[3]

		if _, ok := stats[uuid]; !ok {
			stats[uuid] = &UserStats{}
		}

		if direction == "uplink" {
			stats[uuid].Upload = stat.Value
		} else if direction == "downlink" {
			stats[uuid].Download = stat.Value
		}
	}

	return stats, nil
}
