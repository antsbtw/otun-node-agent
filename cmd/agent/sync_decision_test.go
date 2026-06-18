package main

import (
	"testing"

	"otun-node-agent/internal/config"
)

// WP-C 决策表单测：双 version 下 reload / 热更 / 不动 的分流，外加老 manager 兼容路径。
// 标准节点 manager 当前只下发顶层 version（无 user_version/param_version）→ 走"version 变即 reload"。
func TestDecideSyncAction(t *testing.T) {
	tests := []struct {
		name string
		prev syncState
		resp *config.UsersResponse
		want syncAction
	}{
		{
			name: "仅 user_version 变（param 不变）→ 热更",
			prev: syncState{version: "v1", userVersion: "u1", paramVersion: "p1"},
			resp: &config.UsersResponse{Version: "v2", UserVersion: "u2", ParamVersion: "p1"},
			want: actionHotReload,
		},
		{
			name: "param_version 变（user 也变）→ reload 优先",
			prev: syncState{version: "v1", userVersion: "u1", paramVersion: "p1"},
			resp: &config.UsersResponse{Version: "v3", UserVersion: "u2", ParamVersion: "p2"},
			want: actionReload,
		},
		{
			name: "param_version 变（user 不变，如协议/SNI/端口）→ reload",
			prev: syncState{version: "v1", userVersion: "u1", paramVersion: "p1"},
			resp: &config.UsersResponse{Version: "v4", UserVersion: "u1", ParamVersion: "p2"},
			want: actionReload,
		},
		{
			name: "双 version 都不变 → 不动",
			prev: syncState{version: "v1", userVersion: "u1", paramVersion: "p1"},
			resp: &config.UsersResponse{Version: "v1", UserVersion: "u1", ParamVersion: "p1"},
			want: actionNone,
		},
		{
			name: "首次激活：本地空 + 下发带双 version 且 user 变 → 热更（param 也空vs空相等不触发reload）",
			prev: syncState{},
			resp: &config.UsersResponse{Version: "v1", UserVersion: "u1", ParamVersion: "p1"},
			want: actionReload, // prev.paramVersion("")!=resp.ParamVersion("p1") → reload
		},
		{
			name: "老 manager（无双 version），顶层 version 不变 → 不动",
			prev: syncState{version: "v1"},
			resp: &config.UsersResponse{Version: "v1"},
			want: actionNone,
		},
		{
			name: "老 manager（无双 version），顶层 version 变 → reload（安全侧，不冒险热更）",
			prev: syncState{version: "v1"},
			resp: &config.UsersResponse{Version: "v2"},
			want: actionReload,
		},
		{
			name: "半残响应：只有 user_version 缺 param_version → 当老 manager，version 变则 reload",
			prev: syncState{version: "v1", userVersion: "u1", paramVersion: "p1"},
			resp: &config.UsersResponse{Version: "v2", UserVersion: "u2"},
			want: actionReload,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideSyncAction(tc.prev, tc.resp)
			if got != tc.want {
				t.Fatalf("decideSyncAction = %v, want %v", got, tc.want)
			}
		})
	}
}

// diffRemovedUsers 必须只报"上次在、本次不在"的 UUID，供热更后主动踢连接。
func TestDiffRemovedUsers(t *testing.T) {
	a := &Agent{currentUserSet: uuidSet([]string{"a", "b", "c"})}

	removed := a.diffRemovedUsers([]string{"a", "c", "d"}) // b 掉出，d 新增
	if len(removed) != 1 || removed[0] != "b" {
		t.Fatalf("removed = %v, want [b]", removed)
	}

	// 纯新增不报 removed。
	if r := a.diffRemovedUsers([]string{"a", "b", "c", "e"}); len(r) != 0 {
		t.Fatalf("pure-add removed = %v, want empty", r)
	}
}

// enabledUUIDs 只取 Enabled 用户（对齐 generator 的 inbound users 过滤）。
func TestEnabledUUIDs(t *testing.T) {
	users := []config.User{
		{UUID: "a", Enabled: true},
		{UUID: "b", Enabled: false},
		{UUID: "c", Enabled: true},
	}
	got := enabledUUIDs(users)
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("enabledUUIDs = %v, want [a c]", got)
	}
}
