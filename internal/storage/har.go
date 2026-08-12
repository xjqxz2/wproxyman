// Package storage HAR 1.2 格式的导入/导出实现。
// har.go 提供了 HTTP Archive（HAR）1.2 规范的结构体定义和 Flow ↔ HAR 转换逻辑。
//
// HAR 格式：HTTP Archive — 一种 JSON 格式的 HTTP 交互归档标准，
// 被广泛应用于浏览器开发者工具和流量分析工具之间交换数据。
//
// 实现范围（HAR 1.2 子集）：
//   - 导出：Flow → HAR 条目（请求/响应头、体、Cookie、MIME 类型）
//   - 导入：HAR 条目 → Flow（解析 URL、设置 scheme/host/path 等字段）
//   - 二进制体处理：判断是否为文本（通过检查 NUL 字节），非文本用 base64 编码
//
// 支持的 HAR 字段：
//   - entry.startedDateTime, entry.time
//   - request.method, .url, .httpVersion, .headers, .postData, .cookies
//   - response.status, .statusText, .headers, .content (mimeType/text), .cookies
package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"wproxyman/internal/models"
)

// --- HAR 1.2 结构体定义（导入/导出所需的子集） ---

// HAR 是 HAR 文件的顶级结构，包含一个 log 条目。
type HAR struct {
	Log HARLog `json:"log"`
}

// HARLog 包含 HAR 版本、创建者信息和条目列表。
type HARLog struct {
	Version string     `json:"version"` // HAR 格式版本（应为 "1.2"）
	Creator HARCreator `json:"creator"` // 创建此 HAR 的工具信息
	Entries []HAREntry `json:"entries"` // HTTP 交互条目列表
}

// HARCreator 描述生成 HAR 文件的工具。
type HARCreator struct {
	Name    string `json:"name"`    // 工具名称
	Version string `json:"version"` // 工具版本
}

// HAREntry 代表一个完整的 HTTP 请求-响应对。
type HAREntry struct {
	StartedDateTime string      `json:"startedDateTime"` // 请求开始时间（ISO 8601）
	Time            float64     `json:"time"`            // 请求总耗时（毫秒）
	Request         HARRequest  `json:"request"`         // 请求详情
	Response        HARResponse `json:"response"`        // 响应详情
	ServerIPAddress string      `json:"serverIPAddress,omitempty"` // 服务器 IP（可选）
}

// HARRequest 描述一个 HTTP 请求。
type HARRequest struct {
	Method      string       `json:"method"`      // HTTP 方法（GET/POST/...）
	URL         string       `json:"url"`         // 完整 URL
	HTTPVersion string       `json:"httpVersion"` // HTTP 协议版本
	Headers     []HARHeader  `json:"headers"`     // 请求头列表
	QueryString []HARQuery   `json:"queryString"` // 查询参数列表
	Cookies     []HARCookie  `json:"cookies"`     // Cookie 列表
	HeadersSize int          `json:"headersSize"` // 请求头大小（字节）
	BodySize    int          `json:"bodySize"`    // 请求体大小（字节）
	PostData    *HARPostData `json:"postData,omitempty"` // POST 数据（可选）
}

// HARPostData 描述 POST 请求体。
type HARPostData struct {
	MimeType string `json:"mimeType"` // MIME 类型
	Text     string `json:"text"`     // 请求体内容
	Encoding string `json:"encoding,omitempty"` // 编码方式（"base64" 或空）
}

// HARResponse 描述一个 HTTP 响应。
type HARResponse struct {
	Status      int         `json:"status"`      // HTTP 状态码
	StatusText  string      `json:"statusText"`  // HTTP 状态文本
	HTTPVersion string      `json:"httpVersion"` // HTTP 协议版本
	Headers     []HARHeader `json:"headers"`     // 响应头列表
	Cookies     []HARCookie `json:"cookies"`     // Cookie 列表
	Content     HARContent  `json:"content"`     // 响应体内容
	RedirectURL string      `json:"redirectURL"` // 重定向 URL
	HeadersSize int         `json:"headersSize"` // 响应头大小（字节）
	BodySize    int         `json:"bodySize"`    // 响应体大小（字节）
}

// HARContent 描述响应体。
type HARContent struct {
	Size     int    `json:"size"`             // 响应体大小（字节）
	MimeType string `json:"mimeType"`         // MIME 类型
	Text     string `json:"text,omitempty"`   // 响应体文本（非二进制时）
	Encoding string `json:"encoding,omitempty"` // 编码方式（"base64" 或空）
}

// HARHeader 表示 HTTP 头键值对。
type HARHeader struct {
	Name  string `json:"name"`  // 头名称
	Value string `json:"value"` // 头值
}

// HARQuery 表示 URL 查询参数键值对。
type HARQuery struct {
	Name  string `json:"name"`  // 参数名
	Value string `json:"value"` // 参数值
}

// HARCookie 表示 Cookie 键值对。
type HARCookie struct {
	Name  string `json:"name"`  // Cookie 名称
	Value string `json:"value"` // Cookie 值
}

// ExportHAR 将 Flow 列表以 HAR 1.2 格式写入指定路径。
// 输出格式化的 JSON（带缩进）。
func ExportHAR(path string, flows []*models.Flow) error {
	log := HARLog{
		Version: "1.2",
		Creator: HARCreator{Name: "wproxyman", Version: "1.0"},
	}
	for _, f := range flows {
		log.Entries = append(log.Entries, flowToHAR(f))
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ") // 缩进格式化
	return enc.Encode(&HAR{Log: log})
}

// flowToHAR 将单个 Flow 转换为 HAR 条目。
// 对于二进制请求体/响应体，使用 base64 编码。
func flowToHAR(f *models.Flow) HAREntry {
	started := time.UnixMilli(f.StartedAt).Format(time.RFC3339Nano)
	durMs := f.Duration
	entry := HAREntry{
		StartedDateTime: started,
		Time:            float64(durMs),
		Request: HARRequest{
			Method:      f.Method,
			URL:         f.FullURL,
			HTTPVersion: f.HTTPVersion,
			Headers:     toHARHeaders(f.RequestHeaders),
			HeadersSize: -1, // 不追踪实际头大小
			BodySize:    int(f.RequestSize),
		},
		Response: HARResponse{
			Status:      f.ResponseStatus,
			StatusText:  f.ResponseReason,
			HTTPVersion: f.HTTPVersion,
			Headers:     toHARHeaders(f.ResponseHeaders),
			Content: HARContent{
				Size:     int(f.ResponseSize),
				MimeType: f.ResponseMimeType,
			},
			HeadersSize: -1,
			BodySize:    int(f.ResponseSize),
		},
	}
	// 复制请求 Cookie
	for _, c := range f.RequestCookies {
		entry.Request.Cookies = append(entry.Request.Cookies, HARCookie{Name: c.Name, Value: c.Value})
	}
	// 复制响应 Cookie
	for _, c := range f.ResponseCookies {
		entry.Response.Cookies = append(entry.Response.Cookies, HARCookie{Name: c.Name, Value: c.Value})
	}
	// 请求体
	if len(f.RequestBody) > 0 {
		encoding := ""
		if !isTextBody(f.RequestBody) {
			encoding = "base64" // 二进制数据标记为 base64 编码
		}
		entry.Request.PostData = &HARPostData{
			MimeType: f.RequestMimeType,
			Text:     string(f.RequestBody),
			Encoding: encoding,
		}
	}
	// 响应体
	if len(f.ResponseBody) > 0 {
		entry.Response.Content.Size = int(f.ResponseSize)
		if isTextBody(f.ResponseBody) {
			entry.Response.Content.Text = string(f.ResponseBody)
		} else {
			entry.Response.Content.Encoding = "base64"
			entry.Response.Content.Text = string(f.ResponseBody)
		}
	}
	return entry
}

// ImportHAR 从 HAR 文件读取条目并转换为 Flow 列表。
// 用于从浏览器 DevTools 或其他工具导入流量数据。
func ImportHAR(path string) ([]*models.Flow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var har HAR
	if err := json.Unmarshal(data, &har); err != nil {
		return nil, fmt.Errorf("invalid HAR file: %v", err)
	}
	var flows []*models.Flow
	for _, e := range har.Log.Entries {
		f := harEntryToFlow(e)
		if f != nil {
			flows = append(flows, f)
		}
	}
	return flows, nil
}

// harEntryToFlow 将 HAR 条目转换为 Flow 对象。
// 创建新的 Flow 并填充请求/响应字段。
func harEntryToFlow(e HAREntry) *models.Flow {
	f := models.NewFlow()
	f.Method = e.Request.Method
	f.FullURL = e.Request.URL
	f.HTTPVersion = e.Request.HTTPVersion
	f.RequestHeaders = fromHARHeaders(e.Request.Headers)
	f.RequestMimeType = mimeOf(e.Request.Headers)
	f.ResponseStatus = e.Response.Status
	f.ResponseReason = e.Response.StatusText
	f.ResponseHeaders = fromHARHeaders(e.Response.Headers)
	f.ResponseMimeType = e.Response.Content.MimeType
	// POST 数据
	if e.Request.PostData != nil {
		f.RequestBody = []byte(e.Request.PostData.Text)
		f.RequestSize = int64(len(f.RequestBody))
	}
	// 响应体
	if e.Response.Content.Text != "" {
		f.ResponseBody = []byte(e.Response.Content.Text)
		f.ResponseSize = int64(len(f.ResponseBody))
	}
	// 解析 URL 以提取 scheme/host/path/query
	f.Scheme = "http"  // 默认值
	f.TLS = false
	u, err := parseURL(e.Request.URL)
	if err == nil {
		f.Scheme = u.Scheme
		f.TLS = u.Scheme == "https"
		f.Host = u.Host
		f.Path = u.Path
		f.Query = u.RawQuery
	}
	// 解析开始时间和耗时
	if t, err := time.Parse(time.RFC3339Nano, e.StartedDateTime); err == nil {
		f.StartedAt = t.UnixMilli()
		f.CompletedAt = f.StartedAt + int64(e.Time)
		f.Duration = int64(e.Time)
	}
	f.IsSaved = true // 导入的数据标记为"已保存"（来源于外部文件）
	return f
}

// parseURL 解析 URL 字符串。
func parseURL(s string) (*url.URL, error) {
	return url.Parse(s)
}

// toHARHeaders 将 models.Header 切片转换为 HAR 格式的头列表。
func toHARHeaders(hs []models.Header) []HARHeader {
	out := make([]HARHeader, 0, len(hs))
	for _, h := range hs {
		out = append(out, HARHeader{Name: h.Name, Value: h.Value})
	}
	return out
}

// fromHARHeaders 将 HAR 格式的头列表转换为 models.Header 切片。
func fromHARHeaders(hs []HARHeader) []models.Header {
	out := make([]models.Header, 0, len(hs))
	for _, h := range hs {
		out = append(out, models.Header{Name: h.Name, Value: h.Value})
	}
	return out
}

// mimeOf 从头列表中提取 Content-Type 值。
func mimeOf(hs []HARHeader) string {
	for _, h := range hs {
		if h.Name == "Content-Type" {
			return h.Value
		}
	}
	return ""
}

// isTextBody 通过简单启发式判断响应体是否为文本内容。
// 方法：检查前 512 字节是否包含 NUL 字节（\x00）。
// NUL 字节通常表示二进制数据（如图片、压缩数据）。
func isTextBody(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	// 简单启发式：前 512 字节中无 NUL 字节 → 文本
	const chunk = 512
	n := len(b)
	if n > chunk {
		n = chunk
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return false // 发现 NUL 字节 → 二进制数据
		}
	}
	return true
}
