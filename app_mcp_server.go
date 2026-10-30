// app_mcp_server.go —— 把 MCP 服务挂到本机回环地址，并把权限档接到设置上。
//
// 安全前提（与 internal/mcp 的分工）：
//   - 这里只绑 127.0.0.1，绝不绑 0.0.0.0；
//   - 令牌为空时拒绝一切请求（internal/mcp 保证），绝不提供无鉴权模式；
//   - 默认只放行 read 档，write/dangerous 必须在设置里显式打开。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"wproxyman/internal/mcp"
)

// ensureMCPToken 返回现有令牌；没有就生成一个并持久化。
func (a *App) ensureMCPToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings.MCPToken == "" {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			// 生成失败就返回空串：服务层会拒绝所有请求（宁可用不了，也不放开）。
			return ""
		}
		a.settings.MCPToken = hex.EncodeToString(buf)
	}
	token := a.settings.MCPToken
	_ = a.saveSettings()
	return token
}

// mcpPermissions 每次调用都重新读设置，这样用户在界面上一开档位就立即生效。
func (a *App) mcpPermissions() mcp.Permissions {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return mcp.Permissions{
		AllowWrite:     a.settings.MCPAllowWrite,
		AllowDangerous: a.settings.MCPAllowDangerous,
	}
}

// startMCPServer 在 127.0.0.1:MCPPort 上启动 MCP 端点；已在运行则直接返回。
func (a *App) startMCPServer() error {
	a.mu.Lock()
	if a.mcpListener != nil {
		a.mu.Unlock()
		return nil
	}
	port := normalizeProxyPort(a.settings.MCPPort) // 复用端口校验（1..65535）
	if a.settings.MCPPort == 0 {
		port = DefaultMCPPort
	}
	a.mu.Unlock()

	reg, warns, err := a.buildMCPRegistry()
	if err != nil {
		return fmt.Errorf("构建 MCP 工具表失败：%w", err)
	}
	for _, w := range warns {
		println("WProxyman: MCP 工具表警告:", w)
	}
	svc := mcp.NewService(reg, a.mcpPermissions, mcp.Implementation{Name: "wproxyman", Version: "1.1.0"})

	// 只绑回环：这是"能控制整个应用"的接口，绝不对外网暴露。
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("MCP 端口 %d 无法使用，可能已被占用：%w", port, err)
	}
	_ = a.ensureMCPToken() // 确保已有令牌（空令牌会让所有请求被拒）
	srv := &http.Server{
		// 令牌动态读取：轮换时立即生效，不需要重启服务。
		Handler:           mcp.NewHTTPHandler(svc, mcp.HTTPConfig{Token: a.mcpTokenValue}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	a.mu.Lock()
	a.mcpListener = ln
	a.mcpServer = srv
	a.mcpService = svc
	a.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			println("WProxyman: MCP server stopped:", err.Error())
		}
	}()
	return nil
}

// stopMCPServer 关闭 MCP 端点（幂等）。
func (a *App) stopMCPServer() {
	a.mu.Lock()
	srv, ln := a.mcpServer, a.mcpListener
	a.mcpServer, a.mcpListener = nil, nil
	a.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	} else if ln != nil {
		_ = ln.Close()
	}
}

// MCPStatus 返回 MCP 区块所需的全部状态（设置面板直接渲染）。
type MCPStatus struct {
	Enabled        bool             `json:"enabled"`
	Running        bool             `json:"running"`
	URL            string           `json:"url"`
	Port           int              `json:"port"`
	Token          string           `json:"token"`
	AllowWrite     bool             `json:"allowWrite"`
	AllowDangerous bool             `json:"allowDangerous"`
	ToolCount      int              `json:"toolCount"`
	Audit          []mcp.AuditEntry `json:"audit"`
}

// GetMCPStatus 供前端展示：URL/令牌/档位/工具数/最近审计。
func (a *App) GetMCPStatus() MCPStatus {
	a.mu.RLock()
	enabled := a.settings.MCPEnabled
	port := a.settings.MCPPort
	token := a.settings.MCPToken
	allowWrite := a.settings.MCPAllowWrite
	allowDangerous := a.settings.MCPAllowDangerous
	running := a.mcpListener != nil
	var realPort int
	if a.mcpListener != nil {
		if addr, ok := a.mcpListener.Addr().(*net.TCPAddr); ok {
			realPort = addr.Port
		}
	}
	svc := a.mcpService
	a.mu.RUnlock()

	st := MCPStatus{
		Enabled: enabled, Running: running, Port: port,
		Token: token, AllowWrite: allowWrite, AllowDangerous: allowDangerous,
	}
	if running && realPort > 0 {
		st.URL = fmt.Sprintf("http://127.0.0.1:%d/mcp", realPort)
	}
	if svc != nil {
		st.ToolCount = len(svc.Registry().Tools())
		st.Audit = svc.Audit().Entries()
	}
	return st
}

// SetMCPEnabled 开关 MCP 服务并保存档位设置。
func (a *App) SetMCPEnabled(enabled bool, port int, allowWrite, allowDangerous bool) error {
	if port != 0 && !validProxyPort(port) {
		return fmt.Errorf("MCP 端口 %d 非法：请填写 %d-%d 之间的整数", port, MinProxyPort, MaxProxyPort)
	}
	a.mu.Lock()
	a.settings.MCPEnabled = enabled
	if port != 0 {
		a.settings.MCPPort = port
	}
	a.settings.MCPAllowWrite = allowWrite
	a.settings.MCPAllowDangerous = allowDangerous
	// 每次开启都确保有令牌。
	needToken := enabled && a.settings.MCPToken == ""
	a.mu.Unlock()
	if needToken {
		a.ensureMCPToken()
	}
	_ = a.saveSettings()

	if !enabled {
		a.stopMCPServer()
		return nil
	}
	return a.startMCPServer()
}

// RotateMCPToken 轮换令牌（旧令牌立即失效）。
func (a *App) RotateMCPToken() (string, error) {
	a.mu.Lock()
	a.settings.MCPToken = ""
	a.mu.Unlock()
	// handler 每次请求都重新读令牌，所以这里不需要重启服务。
	return a.ensureMCPToken(), nil
}

// mcpTokenValue 返回当前令牌（供 handler 每次请求调用）。
func (a *App) mcpTokenValue() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settings.MCPToken
}

// IsMCPRunning 报告 MCP 端点是否在监听。
func (a *App) IsMCPRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mcpListener != nil
}
