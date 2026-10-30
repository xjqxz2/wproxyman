package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	a, _ := newTestApp(t)

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
	if err := a.SetSettings(next); err != nil {
		t.Fatalf("SetSettings(allowLAN=true): %v", err)
	}

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
	if err := a.SetSettings(next); err != nil {
		t.Fatalf("SetSettings(allowLAN=false): %v", err)
	}

	if a.proxySrv == nil || !a.proxySrv.IsRunning() {
		t.Fatalf("proxy should still be running after second restart")
	}
	if got := a.proxySrv.Config().BindHost; got != "127.0.0.1" {
		t.Fatalf("expected BindHost=127.0.0.1 after AllowLAN off, got %q", got)
	}
	if got := a.proxySrv.Port(); got != port {
		t.Fatalf("expected port %d preserved across second restart, got %d", port, got)
	}
}

// TestNormalizeProxyPort 验证端口归一化：历史配置里的 0（旧版"自动分配"）
// 以及越界值都回落到默认端口，合法端口原样保留。
func TestNormalizeProxyPort(t *testing.T) {
	cases := map[int]int{
		0:     DefaultProxyPort,
		-1:    DefaultProxyPort,
		65536: DefaultProxyPort,
		1:     1,
		8080:  8080,
		10086: 10086,
		65535: 65535,
	}
	for in, want := range cases {
		if got := normalizeProxyPort(in); got != want {
			t.Errorf("normalizeProxyPort(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestDefaultSettingsUseFixedPort 验证默认设置端口固定为 10086 而不是 0
// （0 意味着"随机端口"，正是端口会变化的根源）。
func TestDefaultSettingsUseFixedPort(t *testing.T) {
	if got := defaultSettings().ProxyPort; got != DefaultProxyPort {
		t.Fatalf("defaultSettings().ProxyPort = %d, want %d", got, DefaultProxyPort)
	}
}

// TestLoadSettingsMigratesLegacyZeroPort 验证老配置文件（proxyPort=0）被
// 迁移为固定默认端口，用户不需要手工改 settings.json。
func TestLoadSettingsMigratesLegacyZeroPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 的用户配置目录无法通过环境变量重定向")
	}
	dir := t.TempDir()
	// linux: XDG_CONFIG_HOME；darwin: $HOME/Library/Application Support。
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{"proxyPort":0,"autoStartProxy":true,"allowLAN":false}`
	if err := os.WriteFile(settingsPath(), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	if got := loadSettings().ProxyPort; got != DefaultProxyPort {
		t.Fatalf("loadSettings().ProxyPort = %d, want %d", got, DefaultProxyPort)
	}

	// 保存后应把固定端口写回文件（下次启动直接生效）。
	a := NewApp()
	a.settings = loadSettings()
	if err := a.saveSettings(); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if got := int(raw["proxyPort"].(float64)); got != DefaultProxyPort {
		t.Fatalf("persisted proxyPort = %d, want %d", got, DefaultProxyPort)
	}
}

// TestSetSettingsRejectsInvalidPort 验证非法端口被拒绝且不落盘、不改动运行状态。
func TestSetSettingsRejectsInvalidPort(t *testing.T) {
	a, port := newTestApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()

	for _, bad := range []int{0, -1, 65536, 100000} {
		next := a.settings
		next.ProxyPort = bad
		if err := a.SetSettings(next); err == nil {
			t.Fatalf("SetSettings(port=%d) should fail", bad)
		}
	}
	if a.settings.ProxyPort != port {
		t.Fatalf("invalid input must not change settings: got %d, want %d", a.settings.ProxyPort, port)
	}
	if !a.IsProxyRunning() || a.GetProxyPort() != port {
		t.Fatalf("invalid input must not disturb the running proxy")
	}
}

// TestSetSettingsChangesPortWhileRunning 验证运行中改端口会真正迁移监听端口，
// 并把新端口持久化。
func TestSetSettingsChangesPortWhileRunning(t *testing.T) {
	a, oldPort := newTestApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()

	newPort := freePort(t)
	if newPort == oldPort {
		t.Fatalf("test setup error: ports must differ")
	}
	next := a.settings
	next.ProxyPort = newPort
	if err := a.SetSettings(next); err != nil {
		t.Fatalf("SetSettings(port=%d): %v", newPort, err)
	}
	if !a.IsProxyRunning() {
		t.Fatalf("proxy should keep running after port change")
	}
	if got := a.GetProxyPort(); got != newPort {
		t.Fatalf("expected proxy on new port %d, got %d", newPort, got)
	}
	if a.settings.ProxyPort != newPort {
		t.Fatalf("settings.ProxyPort = %d, want %d", a.settings.ProxyPort, newPort)
	}
}

// TestSetSettingsRollsBackWhenNewPortBusy 验证改到一个被占用的端口时：
// 返回错误、配置回滚、原代理继续在原端口运行（不把用户留在"代理已停止"）。
func TestSetSettingsRollsBackWhenNewPortBusy(t *testing.T) {
	a, oldPort := newTestApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()

	busyPort := freePort(t)
	release := occupyPort(t, busyPort)
	defer release()

	next := a.settings
	next.ProxyPort = busyPort
	err := a.SetSettings(next)
	if err == nil {
		t.Fatalf("SetSettings to busy port %d should fail", busyPort)
	}
	if !a.IsProxyRunning() {
		t.Fatalf("proxy should still be running after failed port change")
	}
	if got := a.GetProxyPort(); got != oldPort {
		t.Fatalf("expected rollback to old port %d, got %d", oldPort, got)
	}
	if a.settings.ProxyPort != oldPort {
		t.Fatalf("settings must roll back to %d, got %d", oldPort, a.settings.ProxyPort)
	}
}

// TestSetSystemProxyEnabledRequiresRunningProxy 验证代理未运行时不会把系统
// 代理指向一个无人监听的端口。
func TestSetSystemProxyEnabledRequiresRunningProxy(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.SetSystemProxyEnabled(true); err == nil {
		t.Fatalf("enabling system proxy without a running proxy should fail")
	}
}
