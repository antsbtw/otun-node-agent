package singbox

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 成功 200 {"ok":true,...} → 返回 resp 且无 error（调用方据此走热更成功路径）。
func TestHotReloadClient_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/hotreload/users") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"user_count":3,"billing_sync":true}`))
	}))
	defer srv.Close()

	c := NewHotReloadClient(strings.TrimPrefix(srv.URL, "http://"))
	resp, err := c.UpdateUsers([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.UserCount != 3 || !resp.BillingSync {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

// 非 200（如 404 inbound_tag 找不到 / 老二进制无端点路由）→ 返回 error，调用方降级回 reload。
func TestHotReloadClient_Non200_DegradesToReload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"ok":false,"error":"inbound_tag not found"}`))
	}))
	defer srv.Close()

	c := NewHotReloadClient(strings.TrimPrefix(srv.URL, "http://"))
	if _, err := c.UpdateUsers([]string{"a"}); err == nil {
		t.Fatal("expected error on non-200 (so caller degrades to reload), got nil")
	}
}

// 连接被拒（sing-box 没起 / 老二进制 v1.10.7 完全无端点）→ 返回 error，调用方降级回 reload。
func TestHotReloadClient_ConnRefused_DegradesToReload(t *testing.T) {
	// 127.0.0.1:1 几乎必然无监听 → 连接被拒。
	c := NewHotReloadClient("127.0.0.1:1")
	if _, err := c.UpdateUsers([]string{"a"}); err == nil {
		t.Fatal("expected error on connection refused (so caller degrades to reload), got nil")
	}
}
