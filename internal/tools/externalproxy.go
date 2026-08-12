// Package tools 外部代理（External Proxy）工具的实现。
// externalproxy.go 实现将捕获的流量通过上游代理服务器转发。
//
// 功能：
//   - 支持 HTTP 和 HTTPS 代理转发
//   - 支持代理认证（用户名/密码）
//   - 支持绕过域名列表：匹配的域名不经过外部代理，直接连接
//
// 使用场景：
//   - 将流量转发到 Burp Suite / Fiddler 进行进一步分析
//   - 通过公司代理转发流量
package tools

import (
	"fmt"
	"net/url"
)

// ExternalProxyConfig 配置外部代理参数。
// 支持 HTTP/HTTPS 代理和绕过域名列表。
type ExternalProxyConfig struct {
	Enabled  bool   `json:"enabled"`  // 是否启用外部代理
	Host     string `json:"host"`     // 代理服务器主机名
	Port     int    `json:"port"`     // 代理服务器端口
	Username string `json:"username"` // 代理认证用户名
	Password string `json:"password"` // 代理认证密码
	// BypassDomains 是绕过外部代理的主机名列表（直接连接，不经代理）
	BypassDomains []string `json:"bypassDomains"`
	// Type 指定代理类型："http" | "https"（socks5 暂未支持）
	Type string `json:"type"`
}

// UpstreamURL 返回配置的外部代理 URL。
// 如果外部代理未启用或配置不完整，则返回 nil。
func (e *Engine) UpstreamURL() *url.URL {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cfg := e.ExternalProxy
	if !cfg.Enabled || cfg.Host == "" || cfg.Port == 0 {
		return nil
	}
	scheme := cfg.Type
	if scheme == "" {
		scheme = "http" // 默认 HTTP 代理
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
	}
	// 如果有用户名，设置认证信息
	if cfg.Username != "" {
		u.User = url.UserPassword(cfg.Username, cfg.Password)
	}
	return u
}

// bypassesExternalProxy 判断指定主机是否应绕过外部代理。
// 匹配逻辑：精确匹配或子域名匹配（如 ".example.com" 匹配 "api.example.com"）。
func (e *Engine) bypassesExternalProxy(host string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, d := range e.ExternalProxy.BypassDomains {
		if d == "" {
			continue
		}
		// 精确匹配：host == d
		// 子域名匹配：host 以 ".d" 结尾（如 api.example.com 匹配 example.com）
		if host == d || len(host) > len(d) && host[len(host)-len(d)-1] == '.' && host[len(host)-len(d):] == d {
			return true
		}
	}
	return false
}

// ShouldBypassExternalProxy 暴露给服务层使用，判断主机是否应绕过外部代理。
// 这是在 engine.go 的 bypassesExternalProxy 方法上的公共包装。
func (e *Engine) ShouldBypassExternalProxy(host string) bool {
	return e.bypassesExternalProxy(host)
}
