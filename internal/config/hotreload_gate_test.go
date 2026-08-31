package config_test

import (
	"testing"

	"otun-node-agent/internal/config"
)

// 2026-08-31 生产事故回归：agent 无条件下发 experimental.hot_reload，而节点上的
// sing-box 钉死在 v1.10.7（不认该字段 → FATAL 退出），导致全部 OBox 节点陷入
// 崩溃重启循环：8080 还活着、DB 里仍是 active，但 443/SS 全拒，付费用户连不上 23 天。
//
// 契约：默认（未设 SINGBOX_HOT_RELOAD）**绝不**下发 hot_reload；显式开启才发。
func TestHotReloadBlockGatedByEnv(t *testing.T) {
	users := []config.User{{
		UUID:       "u1",
		Protocols:  []string{"vless", "shadowsocks"},
		SSPassword: "p1",
		Enabled:    true,
	}}

	experimentalOf := func(t *testing.T) map[string]any {
		t.Helper()
		gen := config.NewGenerator(443, 8388, "k", []string{"sid"})
		cfg := gen.Generate(users, "www.apple.com", false)
		exp, ok := cfg["experimental"].(map[string]any)
		if !ok {
			t.Fatal("experimental block missing")
		}
		return exp
	}

	t.Run("默认不发 hot_reload（老二进制装上去必须能启动）", func(t *testing.T) {
		exp := experimentalOf(t)
		if _, present := exp["hot_reload"]; present {
			t.Fatal("hot_reload 出现在默认配置里：旧 sing-box 会 FATAL 退出，正是 08-31 事故的成因")
		}
		// 计费不能被一起关掉。
		if _, ok := exp["v2ray_api"]; !ok {
			t.Fatal("v2ray_api 丢失：流量统计会失效")
		}
	})

	t.Run("显式开启才发", func(t *testing.T) {
		t.Setenv("SINGBOX_HOT_RELOAD", "true")
		if _, present := experimentalOf(t)["hot_reload"]; !present {
			t.Fatal("SINGBOX_HOT_RELOAD=true 时应下发 hot_reload")
		}
	})

	t.Run("非 true 的值一律当关（避免误开）", func(t *testing.T) {
		for _, v := range []string{"1", "yes", "TRUE", ""} {
			t.Setenv("SINGBOX_HOT_RELOAD", v)
			if _, present := experimentalOf(t)["hot_reload"]; present {
				t.Fatalf("SINGBOX_HOT_RELOAD=%q 不应开启热更", v)
			}
		}
	})
}
