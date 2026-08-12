// Package tools 本地映射（Map Local）工具的实现。
// maplocal.go 实现将匹配的请求映射到本地文件或内联内容。
//
// 功能：
//   - 文件模式：将请求响应替换为本地磁盘文件的内容
//   - 内联模式：将请求响应替换为用户指定的内联文本
//   - 自动推断 Content-Type（基于文件扩展名或内容嗅探）
//   - 支持自定义响应状态码和响应头
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wproxyman/internal/models"
)

// MapLocalRule 定义了将匹配的请求映射到本地文件或内联内容的规则。
// Type 字段决定规则是文件映射还是内联内容映射。
type MapLocalRule struct {
	Rule
	// LocalFile 是本地文件路径（Type == "file" 时使用，Type == "inline" 时为空）。
	LocalFile string `json:"localFile"`
	// Body 是内联响应体内容（Type == "inline" 时使用）。
	Body string `json:"body"`
	// Status 覆盖响应状态码（为 0 时默认 200）。
	Status int `json:"status"`
	// Headers 是要包含在映射响应中的额外响应头。
	Headers []models.Header `json:"headers"`
	// Type 指定映射类型："file"（本地文件）或 "inline"（内联内容）。
	Type string `json:"type"`
}

// MapLocalConfig 持有所有本地映射规则和启用标志。
type MapLocalConfig struct {
	Enabled bool           `json:"enabled"` // 是否启用本地映射工具
	Rules   []MapLocalRule `json:"rules"`   // 规则列表（按顺序匹配，优先命中优先使用）
}

// applyMapLocal 在规则匹配时将 Flow 标记为短路响应并填充本地内容。
// 返回 true 表示请求已被映射（短路），false 表示继续正常处理。
func (e *Engine) applyMapLocal(f *models.Flow) bool {
	cfg := e.MapLocal
	if !cfg.Enabled {
		return false
	}
	// 遍历规则列表，使用第一个匹配的规则
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if !r.Enabled || !r.Match.Matches(f) {
			continue
		}
		f.ToolType = "Map Local"
		f.ResponseStatus = r.Status
		if f.ResponseStatus == 0 {
			f.ResponseStatus = 200 // 默认状态码
		}
		f.ResponseReason = "OK"
		// 复制用户指定的响应头（避免共享底层数组）
		f.ResponseHeaders = append([]models.Header(nil), r.Headers...)

		// 获取响应体：优先内联内容，其次本地文件
		var body []byte
		if r.Type == "inline" {
			body = []byte(r.Body)
		} else if r.LocalFile != "" {
			data, err := os.ReadFile(r.LocalFile)
			if err != nil {
				// 文件读取失败：返回 404 并记录错误
				f.Error = fmt.Sprintf("map local: cannot read file: %v", err)
				f.ResponseStatus = 404
				f.ResponseReason = "Not Found"
				body = []byte("Map Local: " + err.Error())
			} else {
				body = data
			}
		}

		f.ResponseBody = body
		f.ResponseSize = int64(len(body))
		// 如果用户未指定 Content-Type，则自动推断
		if models.HeaderValue(f.ResponseHeaders, "Content-Type") == "" {
			models.SetHeader(&f.ResponseHeaders, "Content-Type", guessContentType(r.LocalFile, body))
		}
		models.SetHeader(&f.ResponseHeaders, "Content-Length", fmt.Sprint(len(body)))
		return true
	}
	return false
}

// guessContentType 根据文件扩展名或内容自动推断 MIME 类型。
// 优先使用文件扩展名判断，其次对内容进行简单嗅探（JSON/HTML）。
func guessContentType(filePath string, body []byte) string {
	// 优先根据文件扩展名判断
	if filePath != "" {
		switch strings.ToLower(filepath.Ext(filePath)) {
		case ".json":
			return "application/json"
		case ".html", ".htm":
			return "text/html"
		case ".xml":
			return "application/xml"
		case ".txt", ".md":
			return "text/plain"
		case ".js":
			return "application/javascript"
		case ".css":
			return "text/css"
		case ".png":
			return "image/png"
		case ".jpg", ".jpeg":
			return "image/jpeg"
		case ".gif":
			return "image/gif"
		case ".svg":
			return "image/svg+xml"
		case ".pdf":
			return "application/pdf"
		case ".zip":
			return "application/zip"
		}
	}
	// 内容嗅探：检测常见 JSON/HTML 格式
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "application/json"
	}
	if strings.HasPrefix(trimmed, "<") {
		return "text/html"
	}
	return "text/plain" // 默认为纯文本
}
