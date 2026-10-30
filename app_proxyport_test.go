package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// 本文件聚焦"代理端口固定"这一行为：
//
//   - 默认端口是 10086，且只有 10086 空闲它就永远是 10086；
//   - 停止后再启动（端口参数 0）不改变端口；
//   - 端口被占用时启动失败并给出可操作提示，绝不自动顺延/随机换端口；
//   - 运行中换端口会真正迁移，失败则保持原代理继续运行。

// freePort 申请一个当前空闲的端口并立即释放，供测试使用。
// 选高位随机端口是为了避免与开发机/CI 上真实服务占用 10086 冲突。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// occupyPort 占住指定端口，返回释放函数。
// 端口已被别的进程占用时跳过用例（避免测试环境导致的假失败）。
func occupyPort(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skipf("port %d already in use, skip: %v", port, err)
	}
	return func() { _ = ln.Close() }
}

// newTestApp 返回一个"已加载默认设置、但端口换成高位空闲端口"的 App。
// 端口换成空闲高位端口，避免测试去抢真实的 10086。
func newTestApp(t *testing.T) (*App, int) {
	t.Helper()
	a := NewApp()
	a.settings = defaultSettings()
	port := freePort(t)
	a.settings.ProxyPort = port
	return a, port
}

// TestStartProxyDefaultPortIs10086 验证默认设置下代理绑定 10086。
// 若本机 10086 已被占用则跳过（该场景由 TestStartProxyFailsWhenPortBusy 覆盖）。
func TestStartProxyDefaultPortIs10086(t *testing.T) {
	a := NewApp()
	a.settings = defaultSettings()

	if err := canBind("127.0.0.1", DefaultProxyPort); err != nil {
		t.Skipf("port %d busy on this machine, skip: %v", DefaultProxyPort, err)
	}

	got, err := a.startProxy(0)
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()
	if got != DefaultProxyPort {
		t.Fatalf("startProxy(0) bound port %d, want default %d", got, DefaultProxyPort)
	}
}

// TestStartProxyKeepsPortAcrossRestart 是用户报的场景的回归测试：
// 停止后再启动（前端工具栏按钮传 0），端口必须还是原来那个。
func TestStartProxyKeepsPortAcrossRestart(t *testing.T) {
	a, port := newTestApp(t)

	first, err := a.startProxy(0)
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	if first != port {
		t.Fatalf("first start bound %d, want %d", first, port)
	}
	if err := a.StopProxy(); err != nil {
		t.Fatalf("stop proxy: %v", err)
	}

	second, err := a.startProxy(0)
	if err != nil {
		t.Fatalf("restart proxy: %v", err)
	}
	defer a.StopProxy()
	if second != port {
		t.Fatalf("port changed across restart: %d -> %d", port, second)
	}
	if settings := a.settings.ProxyPort; settings != port {
		t.Fatalf("settings.ProxyPort = %d, want %d", settings, port)
	}
}

// TestStartProxyFailsWhenPortBusy 验证端口被占用时：启动失败、返回可读错误、
// 不自动顺延到其他端口、配置保持不变。
func TestStartProxyFailsWhenPortBusy(t *testing.T) {
	a := NewApp()
	a.settings = defaultSettings()
	busyPort := freePort(t)
	a.settings.ProxyPort = busyPort
	release := occupyPort(t, busyPort)
	defer release()

	got, err := a.startProxy(0)
	if err == nil {
		a.StopProxy()
		t.Fatalf("start on busy port %d should fail, got port %d", busyPort, got)
	}
	if a.IsProxyRunning() {
		t.Fatalf("proxy must not be running after a failed start")
	}
	if a.settings.ProxyPort != busyPort {
		t.Fatalf("failed start must not change the configured port: got %d, want %d", a.settings.ProxyPort, busyPort)
	}
	// 错误信息要能指导用户操作（提到端口号且建议改端口）。
	if a.GetProxyError() == "" {
		t.Fatalf("GetProxyError() should expose the failure reason")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(busyPort)) {
		t.Fatalf("error should mention the port number, got: %v", err)
	}
}

// TestStartProxyMigratesRunningPort 验证运行中请求另一个端口时会迁移过去。
func TestStartProxyMigratesRunningPort(t *testing.T) {
	a, oldPort := newTestApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()

	newPort := freePort(t)
	if newPort == oldPort {
		t.Fatalf("test setup error: ports must differ")
	}
	got, err := a.startProxy(newPort)
	if err != nil {
		t.Fatalf("migrate to port %d: %v", newPort, err)
	}
	if got != newPort || a.GetProxyPort() != newPort {
		t.Fatalf("expected migration to %d, got %d (status %d)", newPort, got, a.GetProxyPort())
	}
	if a.settings.ProxyPort != newPort {
		t.Fatalf("settings.ProxyPort = %d, want %d", a.settings.ProxyPort, newPort)
	}
	// 原端口应已释放，可以重新绑定。
	if err := canBind("127.0.0.1", oldPort); err != nil {
		t.Fatalf("old port %d should be released after migration: %v", oldPort, err)
	}
}

// TestStartProxyRejectsInvalidPort 验证非法端口参数被直接拒绝。
func TestStartProxyRejectsInvalidPort(t *testing.T) {
	a, _ := newTestApp(t)
	for _, bad := range []int{-1, 65536, 100000} {
		if _, err := a.startProxy(bad); err == nil {
			t.Fatalf("startProxy(%d) should fail", bad)
		}
	}
	if a.IsProxyRunning() {
		t.Fatalf("proxy must not run after invalid port requests")
	}
}
