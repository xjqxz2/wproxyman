//go:build windows

// Package systemproxy Windows 平台的系统代理管理实现。
// windows.go 通过修改注册表中的 Internet Settings 来配置 WinINET 代理。
//
// 工作原理：
//   - 读取/写入 HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
//   - 设置 ProxyEnable=1、ProxyServer="host:port"、ProxyOverride="<local>;..."
//   - 通过 wininet.dll 的 InternetSetOptionW 通知系统代理变更
//   - 清除时设置 ProxyEnable=0
//
// 设计决策：
//   - 仅配置 WinINET 代理（覆盖浏览器和大多数应用）
//   - 不配置 WinHTTP（netsh winhttp）——需要管理员权限且会弹出控制台窗口
//
// 技术细节：
//   - 使用 golang.org/x/sys/windows 操作注册表和 DLL 调用
//   - InternetSetOptionW 的两个调用：
//     INTERNET_OPTION_SETTINGS_CHANGED (39)：通知系统代理设置已变更
//     INTERNET_OPTION_REFRESH (37)：刷新代理设置
package systemproxy

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// platformManager 是 Windows 平台的代理管理器实现（空结构体，无状态）。
type platformManager struct{}

// ieSettingsKey 是 Internet Explorer/Edge 代理设置的注册表路径。
const ieSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet                            = windows.NewLazySystemDLL("wininet.dll")
	internetSetOptionW                 = wininet.NewProc("InternetSetOptionW")
	internetOptionSettingsChanged      = 39 // INTERNET_OPTION_SETTINGS_CHANGED
	internetOptionRefresh              = 37 // INTERNET_OPTION_REFRESH
)

// Apply 启用系统代理。
// 设置 ProxyEnable=1、ProxyServer="host:port"、ProxyOverride 包含 bypass 列表。
// 始终在 bypass 列表前添加 "<local>" 以绕过本地地址。
func (platformManager) Apply(p Proxy) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, ieSettingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open IE settings: %w", err)
	}
	defer k.Close()

	server := fmt.Sprintf("%s:%d", p.Host, p.Port)
	// 绕过列表：<local> 前缀表示绕过所有本地地址
	bypass := "<local>"
	if len(p.Bypass) > 0 {
		bypass += ";" + strings.Join(p.Bypass, ";")
	}
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", server); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyOverride", bypass); err != nil {
		return err
	}
	// 通知 WinINET 代理设置已变更（使更改立即生效）
	notifyWinINet()
	// 注意：WinHTTP（netsh winhttp）在此有意未被配置——
	// 它需要管理员权限，且每次启动都会弹出控制台窗口。
	// 上述 WinINET 设置已覆盖所有浏览器和大多数应用程序。
	return nil
}

// Clear 禁用系统代理。
// 设置 ProxyEnable=0，不移除 ProxyServer 值（便于下次快速恢复）。
func (platformManager) Clear() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, ieSettingsKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	notifyWinINet()
	return nil
}

// Status 检查系统代理是否当前已启用。
// 读取注册表中的 ProxyEnable 值。
func (platformManager) Status() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, ieSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("ProxyEnable")
	return err == nil && v == 1
}

// Current 返回当前配置的代理服务器地址。
// 读取注册表中的 ProxyServer 值。
func (platformManager) Current() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, ieSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	s, _, err := k.GetStringValue("ProxyServer")
	if err != nil {
		return ""
	}
	return s
}

// notifyWinINet 通过 wininet.dll 通知系统代理设置已变更。
// INTERNET_OPTION_SETTINGS_CHANGED 告诉系统重新读取代理设置。
// INTERNET_OPTION_REFRESH 强制刷新当前连接。
func notifyWinINet() {
	internetSetOptionW.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	internetSetOptionW.Call(0, uintptr(internetOptionRefresh), 0, 0)
}
