// Package tools 远程映射（Map Remote）工具的实现。
// mapremote.go 实现将匹配的请求重定向到另一个远程 URL。
//
// 功能：
//   - 将请求的完整 URL 替换为目标 URL
//   - 支持正则捕获组的反向引用（$1..$9）
//   - 例如：Pattern = "(.*)/api/(.*)"，TargetURL = "https://other.com/$2" →
//           https://example.com/api/users → https://other.com/users
package tools

import (
	"strings"

	"wproxyman/internal/models"
)

// MapRemoteRule 定义了将匹配的请求重定向到另一个 URL 的规则。
// TargetURL 支持对正则捕获组的反向引用。
type MapRemoteRule struct {
	Rule
	// TargetURL 是目标 URL。支持 $1..$9 反向引用正则匹配 Pattern 中的捕获组。
	TargetURL string `json:"targetUrl"`
}

// MapRemoteConfig 持有所有远程映射规则和启用标志。
type MapRemoteConfig struct {
	Enabled bool             `json:"enabled"` // 是否启用远程映射工具
	Rules   []MapRemoteRule `json:"rules"`   // 规则列表（按顺序匹配，优先命中优先使用）
}

// applyMapRemote 在规则匹配时返回上游 URL 覆盖值。
// 返回值：(目标URL, 是否匹配)
// 当规则匹配且为正则模式时，执行反向引用替换。
func (e *Engine) applyMapRemote(f *models.Flow) (string, bool) {
	cfg := e.MapRemote
	if !cfg.Enabled {
		return "", false
	}
	// 遍历规则列表，使用第一个匹配的规则
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if !r.Enabled || !r.Match.Matches(f) {
			continue
		}
		target := r.TargetURL
		// 正则匹配模式下，支持 $1..$9 反向引用
		if r.Match.IsRegex && strings.Contains(target, "$") {
			re, err := compileRegex(r.Match.Pattern, r.Match.IgnoreCase)
			if err == nil {
				// regexp.ReplaceAllString 自动处理 $1..$9 反向引用
				target = re.ReplaceAllString(f.FullURL, target)
			}
		}
		f.ToolType = "Map Remote"
		return target, true
	}
	return "", false
}
