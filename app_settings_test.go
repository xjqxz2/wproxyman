package main

import (
	"testing"
)

// TestSetSettingsAllowLANRestartsProxy 复现并验证一个关键 bug 的修复：
//
// 用户在代理运行中切换 "Allow LAN connections" 开关时，如果只更新了
// settings.AllowLAN 而没有重启代理，监听地址仍是 127.0.0.1，局域网设备
// （手机/模拟器）将无法连接（"找不到服务器"）。
//
// 预期行为：SetSettings 检测到 AllowLAN 变化且代理正在运行时，自动重启
// 代理，使其按新的 AllowLAN 值重新绑定（true → 0.0.0.0，false → 127.0.0.1），
// 且端口保持不变。
func TestSetSettingsAllowLANRestartsProxy(t *testing.T) {
	a := NewApp()
	a.settings.AllowLAN = false

	port, err := a.startProxy(0)
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()

	if got := a.proxySrv.Config().BindHost; got != "127.0.0.1" {
		t.Fatalf("expected initial BindHost=127.0.0.1, got %q", got)
	}

	// 模拟用户在 Devices 页打开 Allow LAN 开关（代理仍在运行）。
	next := a.settings
	next.AllowLAN = true
	a.SetSettings(next)

	if a.proxySrv == nil || !a.proxySrv.IsRunning() {
		t.Fatalf("proxy should have been restarted, got nil or stopped")
	}
	if got := a.proxySrv.Config().BindHost; got != "0.0.0.0" {
		t.Fatalf("expected BindHost=0.0.0.0 after AllowLAN, got %q", got)
	}
	if got := a.proxySrv.Port(); got != port {
		t.Fatalf("expected port %d preserved across restart, got %d", port, got)
	}

	// 再关掉 AllowLAN：同样应重启回回环。
	a.settings.AllowLAN = true
	next = a.settings
	next.AllowLAN = false
	a.SetSettings(next)

	if a.proxySrv == nil || !a.proxySrv.IsRunning() {
		t.Fatalf("proxy should still be running after second restart")
	}
	if got := a.proxySrv.Config().BindHost; got != "127.0.0.1" {
		t.Fatalf("expected BindHost=127.0.0.1 after AllowLAN off, got %q", got)
	}
}
