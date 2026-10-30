// Package systemproxy 管理操作系统的 HTTP 代理设置。
// 支持 Windows、macOS 和 Linux 三个平台，通过 build tag 选择平台实现。
//
// 功能：
//   - Apply：将系统代理设置为指定的主机和端口（含绕过列表）
//   - Clear：移除系统代理配置，恢复直连
//   - Status：检查系统代理当前是否已启用
//   - Current：获取当前生效的代理地址（host:port；未启用或平台读不到时返回空串）
//
// 平台实现：
//   - Windows：通过注册表配置 WinINET 代理设置（HKCU\Internet Settings）
//   - macOS：通过 networksetup 命令配置每个网络服务的代理
//   - Linux：通过 gsettings（GNOME）和 kwriteconfig5（KDE）配置桌面环境代理
//
// 重要：Current() 在部分桌面环境（如各种非 GNOME/KDE 的 Linux）无法读取，
// 因此"系统代理是不是我们设的"不能只靠它判断 —— 调用方需要持久化记录
// 自己写入的地址（见 App.settings.SystemProxyTarget）。
//
// 与其他模块的关系：
//   - 前端 UI 通过此包启用/禁用系统代理，将流量引导至 WProxyman 的本地代理端口
//   - 由于代理必须监听后才能设置系统代理，通常在代理服务器就绪后调用 Apply
package systemproxy

import (
	"net"
	"strings"
)

// Normalize 把各平台读回来的代理地址归一化成 "host:port"，便于字符串比较：
// 去掉空白与方案前缀（http://）、去掉路径，并把 localhost 统一成 127.0.0.1。
func Normalize(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimSuffix(s, "/")
	if host, port, err := net.SplitHostPort(s); err == nil {
		if strings.EqualFold(host, "localhost") {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, port)
	}
	return s
}

// Proxy 是应用到操作系统的代理配置。
// 包含代理服务器地址、端口和绕过域名列表。
type Proxy struct {
	Host   string   `json:"host"`   // 代理服务器主机名或 IP 地址
	Port   int      `json:"port"`   // 代理服务器端口
	Bypass []string `json:"bypass"` // 绕过代理的主机名列表
}

// Manager 接口定义了设置和清除操作系统代理的操作。
// 各平台通过 platformManager 结构体实现此接口。
type Manager interface {
	// Apply 将系统代理设置为 host:port（含绕过列表）。
	Apply(p Proxy) error
	// Clear 移除系统代理配置。
	Clear() error
	// Status 返回系统代理当前是否已启用（任一网络服务/协议启用即为 true）。
	Status() bool
	// Current 返回当前生效的代理地址（"host:port"）；未启用或平台读不到时返回空串。
	Current() string
}

// NewManager 返回当前平台的代理管理器实例。
// 通过 platformManager 结构体（在各平台的 .go 文件中定义）实现多态。
func NewManager() Manager {
	return platformManager{}
}
