// Package tools 阻止列表（Block List）和允许列表（Allow List）工具的实现。
// blocklist.go 实现两个功能：
//
// Block List（阻止列表）：
//   - 匹配的请求直接返回 404，阻止其到达上游服务器
//   - 支持 BlockAll 模式：启用时阻止所有请求（用于断网测试）
//   - 规则列表为空 + BlockAll=true → 阻止一切
//   - 规则列表非空 + BlockAll=false → 只阻止匹配规则的请求
//
// Allow List（允许列表）：
//   - 启用时只捕获匹配规则的请求，其余请求透明代理（不记录）
//   - 用于聚焦特定域名的调试，减少干扰流量
//   - 列表为空 + 启用 = 不捕获任何请求
package tools

import (
	"wproxyman/internal/models"
)

// BlockListRule 定义了要阻止的请求匹配规则。
// 继承 Rule 的 Enabled、Match 字段，无额外字段。
type BlockListRule struct {
	Rule
}

// BlockListConfig 持有阻止列表的规则和启用标志。
// BlockAll 选项可在不配置规则的情况下阻止所有请求。
type BlockListConfig struct {
	Enabled  bool            `json:"enabled"`   // 是否启用阻止列表
	BlockAll bool            `json:"blockAll"`  // 为 true 时阻止所有请求（无需配置规则）
	Rules    []BlockListRule `json:"rules"`     // 阻止规则列表
}

// applyBlockList 检查当前 Flow 是否应被阻止，如果是则短路返回 404。
// 返回 true 表示请求已被阻止（短路）。
func (e *Engine) applyBlockList(f *models.Flow) bool {
	cfg := e.BlockList
	if !cfg.Enabled {
		return false
	}
	// BlockAll 模式：阻止所有请求
	blocked := cfg.BlockAll
	// 如果未开启 BlockAll，检查是否有任何规则匹配
	if !blocked {
		for i := range cfg.Rules {
			if cfg.Rules[i].Enabled && cfg.Rules[i].Match.Matches(f) {
				blocked = true
				break
			}
		}
	}
	if !blocked {
		return false
	}
	// 填充 404 响应，跳过上游请求
	f.ToolType = "Block List"
	f.ResponseStatus = 404
	f.ResponseReason = "Not Found"
	f.ResponseHeaders = []models.Header{{Name: "Content-Type", Value: "text/plain"}}
	body := "Blocked by WProxyman Block List"
	f.ResponseBody = []byte(body)
	f.ResponseSize = int64(len(body))
	f.Error = "Blocked by Block List tool"
	return true
}

// AllowListRule 定义了允许捕获的请求匹配规则。
// 继承 Rule 的 Enabled、Match 字段，无额外字段。
type AllowListRule struct {
	Rule
}

// AllowListConfig 持有允许列表的规则和启用标志。
// 启用时，只有匹配规则的请求才会被捕获；其余请求透明代理不记录。
type AllowListConfig struct {
	Enabled bool             `json:"enabled"` // 是否启用允许列表
	Rules   []AllowListRule  `json:"rules"`   // 允许规则列表
}

// allowListBlocks 判断当前 Flow 是否应被跳过捕获（不记录）。
// 当允许列表启用且没有任何规则匹配时，返回 true（跳过捕获）。
// 返回 true 表示"不捕获此 Flow"，false 表示"正常捕获"。
// 注意：跳过捕获 ≠ 阻止请求，请求仍会正常代理到上游。
func (e *Engine) allowListBlocks(f *models.Flow) bool {
	cfg := e.AllowList
	if !cfg.Enabled {
		return false // 未启用允许列表 → 正常捕获
	}
	// 检查是否有任何规则匹配
	for i := range cfg.Rules {
		if cfg.Rules[i].Enabled && cfg.Rules[i].Match.Matches(f) {
			return false // 有规则匹配 → 捕获此请求
		}
	}
	return true // 无规则匹配 → 跳过捕获
}
