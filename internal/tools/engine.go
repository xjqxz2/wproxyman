// Package tools 实现了 WProxyman 的拦截管道（interception pipeline），
// 驱动以下代理工具：
//   - Map Local（本地映射）、Map Remote（远程映射）
//   - Block List（阻止列表）、Allow List（允许列表）
//   - Breakpoints（断点调试）、Scripting（脚本）
//   - Rules（规则引擎）、No Caching（禁用缓存）
//   - Network Conditions（网络条件模拟）、External Proxy（外部代理）
//
// 核心架构：
//   - Engine 是工具管道的中心控制器，持有所有工具的配置状态。
//   - 管道执行顺序固定：AllowList → Breakpoints → Scripting → BlockList →
//     MapLocal → MapRemote → Rules → NoCaching（见 pipeline.go）
//   - URLMatch 提供统一的 URL/方法匹配机制，支持正则和通配符。
//   - Engine 实现 proxy.Interceptor 接口，由 proxy 模块在请求/响应时调用。
//
// 与其他模块的关系：
//   - proxy 模块在拦截请求/响应时调用 Engine.OnRequest/OnResponse
//   - models 模块定义了 Flow 等核心数据结构
//   - storage 模块负责会话持久化（与工具配置无关）
package tools

import (
	"regexp"
	"strings"
	"sync"

	"wproxyman/internal/models"
)

// URLMatch 定义了工具规则如何匹配请求 URL。
// 支持正则表达式和通配符两种模式，可按 HTTP 方法过滤，大小写可选。
type URLMatch struct {
	// Pattern 是匹配表达式（正则或通配符）。为空则匹配所有 URL。
	Pattern string `json:"pattern"`
	// IsRegex 为 true 时 Pattern 视为正则表达式；false 时视为通配符（* 和 ?）。
	IsRegex bool `json:"isRegex"`
	// Method 限制匹配的 HTTP 方法。支持逗号分隔的多个方法，如 "GET,POST"。空则不限。
	Method string `json:"method"`
	// IgnoreCase 为 true 时 URL 匹配忽略大小写。
	IgnoreCase bool `json:"ignoreCase"`
}

// Matches 判断给定的 Flow（请求流）的 URL 和方法是否匹配此规则。
// 匹配逻辑：先检查 HTTP 方法，再检查 Pattern。
// Pattern 为空时匹配一切（配合 Method 过滤使用）。
func (m *URLMatch) Matches(f *models.Flow) bool {
	if m == nil {
		return false
	}
	// 第一步：检查 HTTP 方法（支持逗号分隔的多方法列表）
	if m.Method != "" && !methodListMatches(m.Method, f.Method) {
		return false
	}
	// 第二步：Pattern 为空 → 匹配所有 URL
	if m.Pattern == "" {
		return true
	}
	// 第三步：根据 IsRegex 选择正则或通配符匹配
	url := f.FullURL
	if m.IgnoreCase {
		url = strings.ToLower(url)
	}
	if m.IsRegex {
		re, err := compileRegex(m.Pattern, m.IgnoreCase)
		if err != nil {
			return false // 正则无效 → 不匹配
		}
		return re.MatchString(url)
	}
	return wildcardMatch(m.Pattern, url, m.IgnoreCase)
}

// methodListMatches 检查 HTTP 方法是否在逗号分隔的方法列表中。
// 例如：list="GET,POST", method="post" → true（大小写不敏感）。
func methodListMatches(list, method string) bool {
	for _, m := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(m), method) {
			return true
		}
	}
	return false
}

// regexCache 缓存已编译的正则表达式，避免重复编译。
// sync.Map 适用于读多写少的场景（规则设置后基本不变）。
var regexCache sync.Map // pattern → *regexp.Regexp（或 error 标记）

// compileRegex 编译正则表达式，支持缓存。
// 如果 ignoreCase 为 true 且 pattern 尚未包含 (?i) 前缀，则自动添加。
// 缓存命中时直接返回已编译的对象（包括缓存的错误）。
func compileRegex(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	// 自动添加不区分大小写标志
	if ignoreCase && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	// 检查缓存
	if v, ok := regexCache.Load(pattern); ok {
		if err, isErr := v.(error); isErr {
			return nil, err // 缓存的是上次编译的错误
		}
		return v.(*regexp.Regexp), nil
	}
	// 编译并缓存
	re, err := regexp.Compile(pattern)
	if err != nil {
		regexCache.Store(pattern, err) // 缓存错误，避免重复尝试
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// wildcardMatch 实现带 * 和 ? 通配符的子串匹配。
// 不含通配符的 Pattern 视为子串匹配（如 "example.com" 匹配任何包含它的 URL）。
// 含 * 或 ? 的 Pattern 转换为正则表达式执行匹配，可在 URL 任意位置匹配。
func wildcardMatch(pattern, s string, ignoreCase bool) bool {
	if ignoreCase {
		pattern = strings.ToLower(pattern)
		s = strings.ToLower(s)
	}
	// 不含通配符 → 简单的子串匹配
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(s, pattern)
	}
	// 将通配符转换为可在任意位置匹配的宽松正则表达式
	// * → .*（匹配任意字符序列），? → .（匹配单个字符）
	var b strings.Builder
	b.WriteString(".*") // 前缀：可在 URL 任何位置开始匹配
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r))) // 转义正则特殊字符
		}
	}
	b.WriteString(".*") // 后缀：可在 URL 任何位置结束
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// Rule 是所有工具规则的公共基础结构。
// 包含启用标志和 URL 匹配条件。
type Rule struct {
	ID      string   `json:"id"`      // 规则的唯一标识符
	Name    string   `json:"name"`    // 规则的可读名称
	Enabled bool     `json:"enabled"`  // 是否启用
	Match   URLMatch `json:"match"`    // URL 匹配条件
}

// Engine 实现 proxy.Interceptor 接口，管理所有工具的状态和管道执行。
// 是所有工具功能的中央控制器。
type Engine struct {
	mu sync.RWMutex // 保护并发访问的读写锁

	// 各工具的配置（由 UI 通过 SetConfig 设置）
	MapLocal          MapLocalConfig          `json:"mapLocal"`
	MapRemote         MapRemoteConfig         `json:"mapRemote"`
	BlockList         BlockListConfig         `json:"blockList"`
	AllowList         AllowListConfig         `json:"allowList"`
	Breakpoints       BreakpointConfig        `json:"breakpoints"`
	Scripts           ScriptingConfig         `json:"scripts"`
	NoCaching         bool                    `json:"noCaching"`
	NetworkConditions NetworkConditionsConfig `json:"networkConditions"`
	ExternalProxy     ExternalProxyConfig     `json:"externalProxy"`
	RuleConfig        RuleToolConfig          `json:"rules"`

	// 断点等待器：flowID → 暂停条目
	waiters map[string]*waitEntry

	// 脚本引擎（惰性初始化，SetConfig 时编译）
	scriptEngine *scriptEngine

	// 预编译的规则项（SetConfig 时重建），加速规则匹配
	ruleItems []RuleItem
}

// NewEngine 创建一个空的工具引擎实例。
// 初始化 waiters map，其他字段使用零值。
func NewEngine() *Engine {
	return &Engine{
		waiters: make(map[string]*waitEntry),
	}
}

// Config 返回当前引擎配置的深拷贝，供前端 UI 获取。
func (e *Engine) Config() *EngineConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return &EngineConfig{
		MapLocal:          e.MapLocal,
		MapRemote:         e.MapRemote,
		BlockList:         e.BlockList,
		AllowList:         e.AllowList,
		Breakpoints:       e.Breakpoints,
		Scripts:           e.Scripts,
		NoCaching:         e.NoCaching,
		NetworkConditions: e.NetworkConditions,
		ExternalProxy:     e.ExternalProxy,
		Rules:             e.RuleConfig,
	}
}

// EngineConfig 是暴露给前端的完整工具配置结构。
// 包含所有工具的启用状态和规则列表。
type EngineConfig struct {
	MapLocal          MapLocalConfig          `json:"mapLocal"`
	MapRemote         MapRemoteConfig         `json:"mapRemote"`
	BlockList         BlockListConfig         `json:"blockList"`
	AllowList         AllowListConfig         `json:"allowList"`
	Breakpoints       BreakpointConfig        `json:"breakpoints"`
	Scripts           ScriptingConfig         `json:"scripts"`
	NoCaching         bool                    `json:"noCaching"`
	NetworkConditions NetworkConditionsConfig `json:"networkConditions"`
	ExternalProxy     ExternalProxyConfig     `json:"externalProxy"`
	Rules             RuleToolConfig          `json:"rules"`
}

// SetConfig 替换引擎的工具配置（由前端 UI 调用）。
// 同时重建预编译的规则项和脚本引擎，确保配置变更立即生效。
func (e *Engine) SetConfig(cfg *EngineConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cfg == nil {
		return
	}
	e.MapLocal = cfg.MapLocal
	e.MapRemote = cfg.MapRemote
	e.BlockList = cfg.BlockList
	e.AllowList = cfg.AllowList
	e.Breakpoints = cfg.Breakpoints
	e.Scripts = cfg.Scripts
	e.NoCaching = cfg.NoCaching
	e.NetworkConditions = cfg.NetworkConditions
	e.ExternalProxy = cfg.ExternalProxy
	e.RuleConfig = cfg.Rules
	e.rebuildRuleItems()   // 重建规则项：编译正则/通配符，过滤禁用项
	e.rebuildScriptEngine() // 重建脚本引擎：编译 JS 脚本
}

// rebuildRuleItems 根据当前 Rules 配置重建预编译的规则项切片。
// 过滤掉禁用的规则，预编译 Pattern 加速后续匹配。
// 在 e.mu 写锁保护下调用。
func (e *Engine) rebuildRuleItems() {
	e.ruleItems = e.ruleItems[:0] // 清空但保留底层数组容量
	for _, r := range e.RuleConfig.Rules {
		if !r.Enabled {
			continue // 跳过禁用的规则
		}
		item := RuleItem{RuleToolEntry: r}
		if r.Match.Pattern != "" {
			url := r.Match.Pattern
			if r.Match.IgnoreCase {
				url = "(?i)" + url
			}
			if !r.Match.IsRegex {
				// 通配符模式：保存原始 pattern 用于后续运行时的 wildcardMatch
				item.glob = &url
			} else if re, err := compileRegex(url, false); err == nil {
				// 正则模式：预编译并缓存
				item.re = re
			}
		}
		e.ruleItems = append(e.ruleItems, item)
	}
}
