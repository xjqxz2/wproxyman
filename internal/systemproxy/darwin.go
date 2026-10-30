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

// parseProxyInfo 解析 networksetup -getwebproxy / -getsecurewebproxy 的输出：
//
//	Enabled: Yes
//	Server: 127.0.0.1
//	Port: 10086
//	Authenticated Proxy Enabled: 0
func parseProxyInfo(out string) (enabled bool, server, port string) {
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Enabled":
			enabled = strings.EqualFold(value, "Yes")
		case "Server":
			server = value
		case "Port":
			port = value
		}
	}
	return enabled, server, port
}

// Status 检查是否有任一网络服务启用了 HTTP/HTTPS 代理。
// （早期实现只看第一个网络服务，用户在 Wi-Fi + 以太网并存时会误报。）
func (m platformManager) Status() bool {
	for _, svc := range networkServices() {
		for _, cmd := range []string{"-getwebproxy", "-getsecurewebproxy"} {
			out, err := exec.Command("networksetup", cmd, svc).Output()
			if err != nil {
				continue
			}
			if enabled, _, _ := parseProxyInfo(string(out)); enabled {
				return true
			}
		}
	}
	return false
}

// Current 返回当前生效的系统代理地址（"host:port"）；没有启用代理时返回空串。
//
// macOS 的代理按网络服务分别配置，这里返回第一个启用代理的服务上的地址。
// 该值用于判断"系统代理是不是我们设的"，因此必须真实可读——早期版本直接
// 返回空串，导致 App.shutdown 的所有权判断永远不成立，退出后系统代理残留。
func (platformManager) Current() string {
	for _, svc := range networkServices() {
		for _, cmd := range []string{"-getwebproxy", "-getsecurewebproxy"} {
			out, err := exec.Command("networksetup", cmd, svc).Output()
			if err != nil {
				continue
			}
			enabled, server, port := parseProxyInfo(string(out))
			if enabled && server != "" && port != "" && port != "0" {
				return Normalize(server + ":" + port)
			}
		}
	}
	return ""
}
