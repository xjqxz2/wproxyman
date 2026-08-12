//go:build linux

// Package systemproxy Linux 平台的系统代理管理实现。
// linux.go 通过桌面环境配置工具设置系统代理（GNOME 和 KDE Plasma）。
//
// 工作原理：
//   - GNOME：使用 gsettings 设置 org.gnome.system.proxy 模式、主机、端口和忽略主机列表
//   - KDE Plasma：使用 kwriteconfig5 写入 kioslaverc 配置文件
//   - Apply 优先尝试 GNOME，失败时尝试 KDE
//   - Clear 同时尝试清除 GNOME 和 KDE 的代理配置
//
// 已知限制：
//   - 仅支持 GNOME 和 KDE Plasma 桌面环境
//   - 不支持通过环境变量（http_proxy/https_proxy）设置代理
//   - 如果两种桌面环境都不可用，Apply 返回错误提示手动配置
//
// GNOME gsettings 键：
//   org.gnome.system.proxy mode: 'manual' | 'none'
//   org.gnome.system.proxy.http host/port
//   org.gnome.system.proxy.https host/port
//   org.gnome.system.proxy ignore-hosts
//
// KDE kwriteconfig5：
//   kioslaverc → [Proxy Settings] → ProxyType=1
//   kioslaverc → [Proxy Settings] → httpProxy
//   kioslaverc → [Proxy Settings] → httpsProxy
package systemproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

// platformManager 是 Linux 平台的代理管理器实现（空结构体，无状态）。
type platformManager struct{}

// gsettingsSet 通过 gsettings 设置 GNOME 代理配置。
func gsettingsSet(schema, key, value string) error {
	return exec.Command("gsettings", "set", schema, key, value).Run()
}

// Apply 尝试设置系统代理，优先 GNOME，其次 KDE Plasma。
// GNOME 成功时返回 nil；如果两种环境都不可用则返回错误。
func (m platformManager) Apply(p Proxy) error {
	server := p.Host
	port := fmt.Sprint(p.Port)
	// 优先尝试 GNOME（最常见的桌面环境）
	if err := gsettingsSet("org.gnome.system.proxy", "mode", "manual"); err == nil {
		// 设置 HTTP 代理
		_ = gsettingsSet("org.gnome.system.proxy.http", "host", server)
		_ = gsettingsSet("org.gnome.system.proxy.http", "port", port)
		// 设置 HTTPS 代理
		_ = gsettingsSet("org.gnome.system.proxy.https", "host", server)
		_ = gsettingsSet("org.gnome.system.proxy.https", "port", port)
		// 构建绕过主机列表（默认包含 localhost）
		bypass := "'localhost,127.0.0.1,::1"
		for _, b := range p.Bypass {
			bypass += "," + b
		}
		bypass += "'"
		_ = gsettingsSet("org.gnome.system.proxy", "ignore-hosts", bypass)
		return nil
	}
	// GNOME 不可用 → 尝试 KDE Plasma
	_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1").Run()
	_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", fmt.Sprintf("http://%s:%d", server, p.Port)).Run()
	_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpsProxy", fmt.Sprintf("http://%s:%d", server, p.Port)).Run()
	return fmt.Errorf("set proxy via GNOME/KDE settings; if neither is available, configure the proxy manually")
}

// Clear 同时尝试清除 GNOME 和 KDE 的代理配置。
// 两种方案都使用尽力而为策略（忽略错误）。
func (m platformManager) Clear() error {
	// GNOME：设置为 'none' 模式
	_ = gsettingsSet("org.gnome.system.proxy", "mode", "none")
	// KDE：设置 ProxyType=0（无代理）
	_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	return nil
}

// Status 通过 gsettings 检查 GNOME 代理模式是否为 'manual'。
func (m platformManager) Status() bool {
	out, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "'manual'"
}

// Current 返回空字符串（Linux 环境下无统一的代理地址获取方式）。
func (platformManager) Current() string { return "" }
