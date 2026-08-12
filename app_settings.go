package main

// 文件说明： 设置与工具配置：读写持久化 settings.json、应用/获取工具引擎配置、

import (
	"encoding/json"
	"os"
	"path/filepath"

	"wproxyman/internal/tools"
)

// Settings holds persisted application preferences.
type Settings struct {
	ProxyPort          int                  `json:"proxyPort"`
	AutoStartProxy     bool                 `json:"autoStartProxy"`
	SSLEnabledDefault  bool                 `json:"sslEnabledDefault"`
	SSLHosts           map[string]bool      `json:"sslHosts"`
	Theme              string               `json:"theme"` // "dark" | "light"
	ToolConfig         *tools.EngineConfig  `json:"toolConfig"`
	MaxBodyBytes       int64                `json:"maxBodyBytes"`
}

func settingsPath() string {
	return filepath.Join(configDir(), "settings.json")
}

func loadSettings() Settings {
	s := Settings{
		ProxyPort:         0,
		AutoStartProxy:    true, // start capturing immediately, like Proxyman
		SSLEnabledDefault: true,
		SSLHosts:          make(map[string]bool),
		Theme:             "dark",
		MaxBodyBytes:      64 << 20,
	}
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s
	}
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

// SetSettings persists updated settings.
func (a *App) SetSettings(s Settings) {
	a.mu.Lock()
	a.settings.Theme = s.Theme
	if s.MaxBodyBytes > 0 {
		a.settings.MaxBodyBytes = s.MaxBodyBytes
	}
	a.mu.Unlock()
	_ = a.saveSettings()
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
