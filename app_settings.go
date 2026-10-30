package main

// 文件说明： 设置与工具配置：读写持久化 settings.json、应用/获取工具引擎配置、

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"wproxyman/internal/models"
	"wproxyman/internal/tools"
)

// DefaultProxyPort 是代理的默认监听端口。
//
// 端口是"用户偏好"而不是"运行时随机值"：只有固定端口，手机/模拟器上填写的
// 代理地址（电脑IP:端口）才会长期有效。若该端口被其他程序占用，代理启动会
// 直接失败并提示用户，由用户在设置里改填其他端口——不做自动顺延，
// 避免端口在用户不知情的情况下漂移。
const DefaultProxyPort = 10086

// 端口合法区间。
const (
	MinProxyPort = 1
	MaxProxyPort = 65535
)

// validProxyPort 判断端口是否可以用于监听。
func validProxyPort(port int) bool {
	return port >= MinProxyPort && port <= MaxProxyPort
}

// normalizeProxyPort 把历史（<=0，早期版本用 0 表示"自动分配端口"）或非法
// 的端口值归一化为默认端口。
func normalizeProxyPort(port int) int {
	if !validProxyPort(port) {
		return DefaultProxyPort
	}
	return port
}

// Settings holds persisted application preferences.
type Settings struct {
	ProxyPort         int                 `json:"proxyPort"`
	AutoStartProxy    bool                `json:"autoStartProxy"`
	AllowLAN          bool                `json:"allowLAN"`
	SSLEnabledDefault bool                `json:"sslEnabledDefault"`
	SSLHosts          map[string]bool     `json:"sslHosts"`
	Theme             string              `json:"theme"` // "dark" | "light"
	ToolConfig        *tools.EngineConfig `json:"toolConfig"`
	MaxBodyBytes      int64               `json:"maxBodyBytes"`

	// SystemProxyEnabled 是"用户希望抓包时接管系统代理"的意图。关掉抓包时会
	// 还原系统代理，但保留这个意图，下次启动/恢复抓包时自动重新接管。
	SystemProxyEnabled bool `json:"systemProxyEnabled"`

	// SystemProxyTarget 记录我们最后写进系统代理的地址（"host:port"，空表示没设过）。
	// 退出/停止/崩溃自愈时靠它判断"这个系统代理是不是我们设的"——
	// 不能依赖 Manager.Current()，因为部分平台读不到当前值（见 systemproxy 包说明）。
	SystemProxyTarget string `json:"systemProxyTarget"`

	// StreamRules 是用户为**非标**流式协议自定义的解析规则（SSE/NDJSON）。
	// OpenAI / Anthropic 由前端内置预设处理，这里只放私有协议的补充规则。
	StreamRules []models.StreamRule `json:"streamRules,omitempty"`
}

func settingsPath() string {
	return filepath.Join(configDir(), "settings.json")
}

// defaultSettings 返回内置默认设置（无配置文件时使用）。
func defaultSettings() Settings {
	return Settings{
		ProxyPort:         DefaultProxyPort, // 固定端口，不再是 0（自动分配）
		AutoStartProxy:    true,             // start capturing immediately, like Proxyman
		AllowLAN:          false,
		SSLEnabledDefault: true,
		SSLHosts:          make(map[string]bool),
		Theme:             "dark",
		MaxBodyBytes:      64 << 20,
	}
}

func loadSettings() Settings {
	s := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings()
	}
	// 迁移历史配置：早期版本用 0 表示"自动选择空闲端口"，导致每次启动端口
	// 都会变化；现在统一固定为偏好端口。
	s.ProxyPort = normalizeProxyPort(s.ProxyPort)
	if s.SSLHosts == nil {
		s.SSLHosts = make(map[string]bool)
	}
	return s
}

func (a *App) saveSettings() error {
	data, err := json.MarshalIndent(&a.settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0o644)
}

// GetSettings returns the current settings.
func (a *App) GetSettings() Settings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := a.settings
	s.ToolConfig = a.engine.Config()
	return s
}

// SetSettings persists updated settings and applies the parts that require a
// running-proxy restart. 返回错误以便前端把具体原因（如端口被占用）提示给用户。
//
// 两种改动需要重启代理：
//   - AllowLAN 变化：监听地址需在 127.0.0.1 ↔ 0.0.0.0 之间切换，否则局域网
//     设备会因代理仍只监听回环而连不上（"找不到服务器"）；
//   - 端口变化：切换到用户填写的端口。
//
// 重启失败（新端口被占用）时回滚到原端口并返回错误——绝不把用户留在
// "代理已停止"或"端口指向一个绑不上的值"的状态。
func (a *App) SetSettings(s Settings) error {
	if !validProxyPort(s.ProxyPort) {
		return fmt.Errorf("端口 %d 非法：请填写 %d-%d 之间的整数", s.ProxyPort, MinProxyPort, MaxProxyPort)
	}

	a.mu.Lock()
	lanChanged := a.settings.AllowLAN != s.AllowLAN
	portChanged := a.settings.ProxyPort != s.ProxyPort
	var runningPort int
	var running bool
	if a.proxySrv != nil && a.proxySrv.IsRunning() {
		running = true
		runningPort = a.proxySrv.Port()
	}
	a.settings.Theme = s.Theme
	a.settings.AllowLAN = s.AllowLAN
	a.settings.ProxyPort = s.ProxyPort
	a.settings.StreamRules = s.StreamRules
	if s.MaxBodyBytes > 0 {
		a.settings.MaxBodyBytes = s.MaxBodyBytes
	}
	a.mu.Unlock()
	_ = a.saveSettings()

	if !running || (!lanChanged && !portChanged) {
		// 代理未运行，或无需重启：仅同步一次状态（端口显示随之更新）。
		a.emitProxyStatus()
		return nil
	}

	// 目标端口：改端口时用新端口；仅 AllowLAN 变化时沿用当前实际端口，
	// 保持端口不变。
	target := runningPort
	if portChanged {
		target = s.ProxyPort
	}

	a.StopProxy()
	if _, err := a.startProxy(target); err != nil {
		if portChanged {
			// 回滚：恢复原端口并重新运行，同时把配置改回原端口，
			// 否则下次启动仍会用一个绑不上的端口。
			a.mu.Lock()
			a.settings.ProxyPort = runningPort
			a.mu.Unlock()
			_ = a.saveSettings()
			if _, rerr := a.startProxy(runningPort); rerr == nil {
				return fmt.Errorf("%v（已恢复原端口 %d）", err, runningPort)
			}
		}
		return err
	}
	return nil
}

// ApplyToolConfig updates the tools engine configuration.
func (a *App) ApplyToolConfig(cfg *tools.EngineConfig) {
	if cfg == nil {
		return
	}
	a.engine.SetConfig(cfg)
	a.mu.Lock()
	a.settings.ToolConfig = cfg
	a.mu.Unlock()
	_ = a.saveSettings()
	a.applyExternalProxy()
}

// GetToolConfig returns the full tool configuration.
func (a *App) GetToolConfig() *tools.EngineConfig {
	cfg := a.engine.Config()
	// Ensure script templates + network profiles are available.
	if len(cfg.Scripts.Scripts) == 0 {
		cfg.Scripts.Scripts = tools.DefaultScriptTemplates()
	}
	return cfg
}

// GetScriptTemplates returns default script examples.
func (a *App) GetScriptTemplates() []tools.ScriptEntry {
	return tools.DefaultScriptTemplates()
}

// GetNetworkProfiles returns preset network condition profiles.
func (a *App) GetNetworkProfiles() []tools.NetworkProfile {
	return tools.DefaultNetworkProfiles()
}

// applyExternalProxy wires the configured upstream proxy into the proxy server.
func (a *App) applyExternalProxy() {
	u := a.engine.UpstreamURL()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proxySrv != nil {
		a.proxySrv.SetUpstreamProxy(u)
	}
}

// SetUpstreamInsecure toggles upstream TLS certificate validation
// (Proxyman's "Disable SSL Certificate Validation" option).
func (a *App) SetUpstreamInsecure(insecure bool) {
	a.mu.Lock()
	if a.proxySrv != nil {
		a.proxySrv.SetUpstreamInsecure(insecure)
	}
	a.mu.Unlock()
}
