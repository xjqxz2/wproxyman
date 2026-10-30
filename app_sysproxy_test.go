package main

import (
	"context"
	"sync"
	"testing"

	"wproxyman/internal/systemproxy"
)

// 本文件覆盖"系统代理必须被正确还原"这一行为。此前的 bug：
// macOS/Linux 的 Manager.Current() 永远返回空串，而退出时只凭它判断
// "系统代理是不是我们设的"，导致退出后系统代理残留 → 用户断网。
// 修复后以持久化记录 settings.SystemProxyTarget 为主依据，Current() 仅作参考。
//
// 用假的 Manager 注入（App.sysProxy 是接口），因此用例跨平台可跑，
// 且不会真的修改本机系统代理。

// fakeSystemProxy 是用于测试的系统代理管理器：记录调用并模拟平台行为。
type fakeSystemProxy struct {
	mu       sync.Mutex
	applies  []systemproxy.Proxy
	clears   int
	current  string // 模拟平台"读回"的当前代理；空串模拟 macOS/Linux 读不到
	applyErr error
}

func (f *fakeSystemProxy) Apply(p systemproxy.Proxy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applies = append(f.applies, p)
	f.current = systemProxyAddr(p.Port)
	return nil
}

func (f *fakeSystemProxy) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clears++
	f.current = ""
	return nil
}

func (f *fakeSystemProxy) Status() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current != ""
}

func (f *fakeSystemProxy) Current() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *fakeSystemProxy) applyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applies)
}

func (f *fakeSystemProxy) clearCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clears
}

func (f *fakeSystemProxy) lastAppliedPort() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applies) == 0 {
		return 0
	}
	return f.applies[len(f.applies)-1].Port
}

func (f *fakeSystemProxy) setCurrent(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = v
}

// newSysProxyApp 构造一个使用假系统代理管理器、端口为高位空闲端口的 App。
func newSysProxyApp(t *testing.T) (*App, *fakeSystemProxy, int) {
	t.Helper()
	a := NewApp()
	a.settings = defaultSettings()
	port := freePort(t)
	a.settings.ProxyPort = port
	f := &fakeSystemProxy{}
	a.sysProxy = f
	return a, f, port
}

// TestShutdownRestoresSystemProxyWhenCurrentUnreadable 是本 bug 的核心回归：
// 即使平台读不到当前代理值（macOS/Linux 的真实情况），退出时也必须还原。
func TestShutdownRestoresSystemProxyWhenCurrentUnreadable(t *testing.T) {
	a, f, port := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}
	if got := f.lastAppliedPort(); got != port {
		t.Fatalf("applied port = %d, want %d", got, port)
	}

	// 模拟平台读不到当前值（修复前 darwin/linux 的 Current() 就是这样）。
	f.setCurrent("")

	a.shutdown(context.Background())

	if got := f.clearCount(); got != 1 {
		t.Fatalf("shutdown must restore the system proxy, Clear called %d times", got)
	}
	if got := a.settings.SystemProxyTarget; got != "" {
		t.Fatalf("SystemProxyTarget should be cleared, got %q", got)
	}
	if !a.settings.SystemProxyEnabled {
		t.Fatalf("user intent (SystemProxyEnabled) should be kept for next launch")
	}
}

// TestShutdownKeepsForeignSystemProxy 验证不会把别的程序设置的代理清掉。
func TestShutdownKeepsForeignSystemProxy(t *testing.T) {
	a, f, _ := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}
	// 别的程序接管了系统代理。
	f.setCurrent("127.0.0.1:8080")

	a.shutdown(context.Background())

	if got := f.clearCount(); got != 0 {
		t.Fatalf("must not clobber another tool's proxy, Clear called %d times", got)
	}
}

// TestStopProxyRestoresThenResumeReapplies 验证"暂停抓包还原系统代理、恢复抓包再接管"。
func TestStopProxyRestoresThenResumeReapplies(t *testing.T) {
	a, f, port := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}

	if err := a.StopProxy(); err != nil {
		t.Fatalf("stop proxy: %v", err)
	}
	if got := f.clearCount(); got != 1 {
		t.Fatalf("stopping capture must restore the system proxy, Clear called %d times", got)
	}
	if got := a.settings.SystemProxyTarget; got != "" {
		t.Fatalf("SystemProxyTarget should be cleared after stop, got %q", got)
	}

	// 恢复抓包：应重新接管系统代理（端口不变）。
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("restart proxy: %v", err)
	}
	defer a.StopProxy()
	if got := f.applyCount(); got != 2 {
		t.Fatalf("resuming capture should re-apply the system proxy, Apply called %d times", got)
	}
	if got := a.settings.SystemProxyTarget; got != systemProxyAddr(port) {
		t.Fatalf("SystemProxyTarget = %q, want %q", got, systemProxyAddr(port))
	}
}

// TestPortChangeReappliesSystemProxy 验证改端口后系统代理跟随到新端口。
func TestPortChangeReappliesSystemProxy(t *testing.T) {
	a, f, oldPort := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
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

	if got := f.lastAppliedPort(); got != newPort {
		t.Fatalf("system proxy should follow to port %d, got %d", newPort, got)
	}
	if got := a.settings.SystemProxyTarget; got != systemProxyAddr(newPort) {
		t.Fatalf("SystemProxyTarget = %q, want %q", got, systemProxyAddr(newPort))
	}
}

// TestHealStaleSystemProxy 验证崩溃/强杀残留的自愈：系统代理指向我们但代理没运行。
func TestHealStaleSystemProxy(t *testing.T) {
	a, f, port := newSysProxyApp(t)
	// 模拟上次崩溃留下的状态：记录在，系统代理也确实是我们设的。
	if err := f.Apply(systemproxy.Proxy{Host: "127.0.0.1", Port: port}); err != nil {
		t.Fatalf("seed system proxy: %v", err)
	}
	a.settings.SystemProxyTarget = systemProxyAddr(port)

	if !a.healStaleSystemProxy() {
		t.Fatalf("stale system proxy should be healed")
	}
	if got := f.clearCount(); got != 1 {
		t.Fatalf("heal should clear the system proxy, Clear called %d times", got)
	}
	if got := a.settings.SystemProxyTarget; got != "" {
		t.Fatalf("SystemProxyTarget should be reset, got %q", got)
	}
}

// TestHealStaleSystemProxyNoopWhenRunning 代理正常运行时不应误清系统代理。
func TestHealStaleSystemProxyNoopWhenRunning(t *testing.T) {
	a, f, _ := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}
	if a.healStaleSystemProxy() {
		t.Fatalf("heal must be a no-op while the proxy is running")
	}
	if got := f.clearCount(); got != 0 {
		t.Fatalf("Clear called %d times, want 0", got)
	}
}

// TestSetSystemProxyEnabledFalseClearsRecord 关闭系统代理要同时清掉记录与用户意图。
func TestSetSystemProxyEnabledFalseClearsRecord(t *testing.T) {
	a, f, _ := newSysProxyApp(t)
	if _, err := a.startProxy(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer a.StopProxy()
	if err := a.SetSystemProxyEnabled(true); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}
	if err := a.SetSystemProxyEnabled(false); err != nil {
		t.Fatalf("disable system proxy: %v", err)
	}
	if got := f.clearCount(); got != 1 {
		t.Fatalf("Clear called %d times, want 1", got)
	}
	if a.settings.SystemProxyTarget != "" || a.settings.SystemProxyEnabled {
		t.Fatalf("record and intent should both be reset: target=%q enabled=%v",
			a.settings.SystemProxyTarget, a.settings.SystemProxyEnabled)
	}
}

// TestSystemProxyIsOurs 覆盖所有权判断的三条路径。
func TestSystemProxyIsOurs(t *testing.T) {
	a, f, port := newSysProxyApp(t)

	// 无记录 + 平台读不到 → 不敢动（宁可不还原，也不误清别人的代理）。
	f.setCurrent("")
	if a.systemProxyIsOurs() {
		t.Fatalf("without a record and an unreadable current value, must not claim ownership")
	}

	// 无记录（旧版本遗留）+ 平台读到正是我们的端口 → 认定是我们的。
	f.setCurrent(systemProxyAddr(port))
	if !a.systemProxyIsOurs() {
		t.Fatalf("legacy state pointing at our port should be recognized")
	}

	// 无记录 + 平台读到别的地址 → 不是我们的。
	f.setCurrent("127.0.0.1:9999")
	if a.systemProxyIsOurs() {
		t.Fatalf("foreign proxy must not be claimed")
	}

	// 有记录 + 平台读不到 → 信任记录。
	a.settings.SystemProxyTarget = systemProxyAddr(port)
	f.setCurrent("")
	if !a.systemProxyIsOurs() {
		t.Fatalf("with a record, ownership must be trusted even if Current() is empty")
	}

	// 有记录 + 平台读到别的地址 → 已被改写，不再认领。
	f.setCurrent("127.0.0.1:9999")
	if a.systemProxyIsOurs() {
		t.Fatalf("when another tool took over, must not claim ownership")
	}
}
