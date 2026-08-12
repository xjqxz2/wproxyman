// Package tools 禁用缓存（No Caching）工具的实现。
// nocache.go 从请求和响应中移除所有与缓存相关的 HTTP 头，
// 确保每次都从服务器拉取最新内容。
//
// 移除的请求头：
//   - If-None-Match、If-Modified-Since（条件请求：服务端可能返回 304）
//   - If-Match、If-Unmodified-Since（条件请求）
//   - If-Range、Cache-Control、Pragma
//
// 移除的响应头：
//   - Cache-Control、Pragma、Expires（缓存策略）
//   - ETag、Last-Modified（缓存验证）
//   - Age、Vary、Date（缓存元数据）
package tools

import (
	"wproxyman/internal/models"
)

// applyNoCaching 从请求和响应中移除缓存相关的头。
// 只在 Engine.NoCaching 为 true 时执行。
func (e *Engine) applyNoCaching(f *models.Flow) {
	if !e.NoCaching {
		return
	}
	// 请求头：移除条件请求头（防止服务端返回 304 Not Modified）
	reqCacheHeaders := []string{
		"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since",
		"If-Range", "Cache-Control", "Pragma",
	}
	for _, h := range reqCacheHeaders {
		models.DeleteHeader(&f.RequestHeaders, h)
	}
	// 响应头：移除缓存策略和验证头（防止客户端缓存）
	respCacheHeaders := []string{
		"Cache-Control", "Pragma", "Expires", "ETag", "Age",
		"Last-Modified", "Vary", "Date",
	}
	for _, h := range respCacheHeaders {
		models.DeleteHeader(&f.ResponseHeaders, h)
	}
	f.ToolType = "No Caching"
}
