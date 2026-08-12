// Package tools 规则引擎（Rules）工具的实现。
// rules.go 提供灵活的请求/响应转换规则，支持修改 HTTP 头、替换请求体、重定向。
//
// 支持的 Action 类型：
//   - addHeader：添加或替换 HTTP 头
//   - removeHeader：删除 HTTP 头
//   - replaceHeader：替换 HTTP 头（等同于 addHeader）
//   - replaceBody：使用正则表达式替换请求体/响应体
//   - redirect：重定向请求到另一个 URL
//
// 设计要点：
//   - 每个规则可以包含多个 Action，按顺序执行
//   - Action 可以指定 phase（"request" 或 "response"），空表示两者都执行
//   - 规则在 SetConfig 时预编译正则/通配符以加速匹配
//   - 规则匹配使用 URLMatch 统一机制
package tools

import (
	"regexp"

	"wproxyman/internal/models"
)

// RuleAction 定义对请求或响应执行的单个转换操作。
// Type 字段决定具体行为。
type RuleAction struct {
	// Type 指定操作类型："addHeader" | "removeHeader" | "replaceHeader" | "replaceBody" | "redirect"
	Type string `json:"type"`
	// HeaderName 是头操作的目标头名称。
	HeaderName string `json:"headerName"`
	// HeaderValue 是添加/替换时使用的头值。
	HeaderValue string `json:"headerValue"`
	// From 是 replaceBody 操作中的正则表达式匹配模式。
	From string `json:"from"`
	// To 是 replaceBody 中的替换文本，或 redirect 中的目标 URL。
	To string `json:"to"`
	// Phase 指定 action 在哪个阶段执行："request" | "response"（"" 表示两者都执行）
	Phase string `json:"phase"`
}

// RuleToolEntry 是规则引擎中的单条规则定义。
// 包含匹配条件和一组转换操作。
type RuleToolEntry struct {
	Rule
	Actions []RuleAction `json:"actions"` // 转换操作列表（按顺序执行）
}

// RuleToolConfig 持有所有规则引擎条目和启用标志。
type RuleToolConfig struct {
	Enabled bool            `json:"enabled"` // 是否启用规则引擎
	Rules   []RuleToolEntry `json:"rules"`   // 规则条目列表
}

// RuleItem 是预编译的规则条目，用于快速匹配。
// 在 SetConfig 时构建，缓存正则或通配符信息。
type RuleItem struct {
	RuleToolEntry
	re   *regexp.Regexp // 预编译的正则（IsRegex 为 true 时）
	glob *string        // 通配符 Pattern 引用（IsRegex 为 false 时）
}

// matches 使用预编译的模式检查 Flow 是否匹配此规则。
func (it *RuleItem) matches(f *models.Flow) bool {
	if it.re != nil {
		return it.re.MatchString(f.FullURL)
	}
	if it.glob != nil {
		return wildcardMatch(*it.glob, f.FullURL, it.Match.IgnoreCase)
	}
	// Pattern 为空 → 匹配所有
	return true
}

// applyRules 对指定阶段执行所有匹配的规则。
// 每个匹配规则的每个 Action 都会被依次执行。
func (e *Engine) applyRules(f *models.Flow, phase string) {
	e.mu.RLock()
	items := e.ruleItems
	e.mu.RUnlock()
	if len(items) == 0 {
		return
	}
	for _, it := range items {
		if !it.matches(f) {
			continue
		}
		for _, a := range it.Actions {
			// 检查 Action 的阶段过滤
			if a.Phase != "" && a.Phase != phase {
				continue
			}
			e.applyAction(f, a, phase)
		}
	}
}

// applyAction 执行单个 Action 对 Flow 的转换。
func (e *Engine) applyAction(f *models.Flow, a RuleAction, phase string) {
	// 根据阶段选择修改请求头还是响应头
	headers := &f.RequestHeaders
	if phase == "response" {
		headers = &f.ResponseHeaders
	}
	switch a.Type {
	case "addHeader":
		// 添加或覆盖 HTTP 头
		models.SetHeader(headers, a.HeaderName, a.HeaderValue)
		f.ToolType = "Rules"
	case "removeHeader":
		// 删除 HTTP 头
		models.DeleteHeader(headers, a.HeaderName)
		f.ToolType = "Rules"
	case "replaceHeader":
		// 替换 HTTP 头（与 addHeader 行为相同）
		models.SetHeader(headers, a.HeaderName, a.HeaderValue)
		f.ToolType = "Rules"
	case "replaceBody":
		// 正则替换请求体或响应体
		body := &f.RequestBody
		if phase == "response" {
			body = &f.ResponseBody
		}
		if re, err := regexp.Compile(a.From); err == nil {
			*body = re.ReplaceAll(*body, []byte(a.To))
			f.ToolType = "Rules"
		}
	case "redirect":
		// 重定向请求 URL（仅请求阶段有效）
		if phase == "request" {
			f.FullURL = a.To
			f.ToolType = "Rules"
		}
	}
}
