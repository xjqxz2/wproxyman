//go:build darwin

// Package systemproxy macOS 平台的系统代理管理实现。
// darwin.go 通过 networksetup 命令行工具配置每个网络服务的代理设置。
//
// 工作原理：
//   - networkServices() 列出所有 macOS 网络服务（Wi-Fi、以太网等）
//   - Apply：对每个网络服务设置 HTTP 和 HTTPS 代理、启用状态、绕过域名
//   - Clear：对每个网络服务关闭 HTTP 和 HTTPS 代理
//   - Status：检查首个网络服务的代理是否已启用
//
// 设计决策：
//   - 对所有网络服务应用代理（而非仅当前活动服务），
//     避免当用户切换网络时代理丢失
//   - 需要管理员权限才能修改系统代理设置
//
// 使用的命令：
//   - networksetup -listallnetworkservices：列出所有网络服务
//   - networksetup -setwebproxy <service> <host> <port>：设置 HTTP 代理
//   - networksetup -setsecurewebproxy <service> <host> <port>：设置 HTTPS 代理
//   - networksetup -setwebproxystate <service> on/off：启用/禁用 HTTP 代理
//   - networksetup -setsecurewebproxystate <service> on/off：启用/禁用 HTTPS 代理
//   - networksetup -setproxybypassdomains <service> <domains>：设置绕过域名
package systemproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

// platformManager 是 macOS 平台的代理管理器实现（空结构体，无状态）。
type platformManager struct{}

// networkServices 通过 networksetup 获取所有 macOS 网络服务名称。
// 跳过注释行（以 "An asterisk" 开头的行）。
func networkServices() []string {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var services []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// 跳过空行和说明行（"An asterisk (*) denotes..."）
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		services = append(services, line)
	}
	return services
}

// Apply 对所有 macOS 网络服务设置 HTTP 和 HTTPS 代理。
// 使用逗号分隔的绕过域名列表（macOS 用逗号，与 Windows 用的分号不同）。
// 需要管理员权限。
func (m platformManager) Apply(p Proxy) error {
	server := p.Host
	port := fmt.Sprint(p.Port)
	// macOS 使用逗号作为分隔符（与 Windows 的分号不同）
	bypass := "<local>"
	if len(p.Bypass) > 0 {
		bypass += "," + strings.Join(p.Bypass, ",")
	}
	services := networkServices()
	if len(services) == 0 {
		return fmt.Errorf("no network services found")
	}
	var lastErr error
	// 对所有网络服务应用代理设置
	for _, svc := range services {
		// 设置 HTTP 代理
		if err := exec.Command("networksetup", "-setwebproxy", svc, server, port).Run(); err != nil {
			lastErr = err
			continue
		}
		// 设置 HTTPS 代理
		if err := exec.Command("networksetup", "-setsecurewebproxy", svc, server, port).Run(); err != nil {
			lastErr = err
			continue
		}
		// 启用 HTTP 代理（状态设为 on）
		_ = exec.Command("networksetup", "-setwebproxystate", svc, "on").Run()
		// 启用 HTTPS 代理
		_ = exec.Command("networksetup", "-setsecurewebproxystate", svc, "on").Run()
		// 设置绕过域名列表
		_ = exec.Command("networksetup", "-setproxybypassdomains", svc, bypass).Run()
	}
	if lastErr != nil {
		return fmt.Errorf("applying proxy requires administrator privileges: %v", lastErr)
	}
	return nil
}

// Clear 对所有网络服务关闭代理设置。
func (m platformManager) Clear() error {
	services := networkServices()
	for _, svc := range services {
		// 关闭 HTTP 代理
		_ = exec.Command("networksetup", "-setwebproxystate", svc, "off").Run()
		// 关闭 HTTPS 代理
		_ = exec.Command("networksetup", "-setsecurewebproxystate", svc, "off").Run()
	}
	return nil
}

// Status 检查首个网络服务的 HTTP 代理是否已启用。
func (m platformManager) Status() bool {
	services := networkServices()
	if len(services) == 0 {
		return false
	}
	// 检查第一个网络服务（通常是 Wi-Fi）
	out, err := exec.Command("networksetup", "-getwebproxy", services[0]).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Enabled: Yes")
}

// Current 返回空字符串（macOS 的代理按网络服务分别配置，无统一值）。
func (platformManager) Current() string { return "" }
