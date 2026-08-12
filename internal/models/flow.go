// Package models 定义了 WProxyman 的核心数据模型。
//
// 这里描述了一条"流量"（Flow）的完整结构：一次 HTTP 事务 = 请求 + 响应 +
// 连接元信息 + 计时。这个模型贯穿整个应用：
//   - 代理引擎（internal/proxy）在拦截流量时构造 Flow；
//   - 工具管线（internal/tools）读取/修改 Flow 以实现 Map Local、断点等；
//   - 服务层（app*.go）将 Flow 通过 JSON 序列化推送到前端展示。
//
// 注意：Flow 中的 []byte 字段（请求/响应体）在 JSON 传输时自动编码为
// base64 字符串，前端在 types.ts 中做对应的解码。
package models

import (
	"encoding/base64"
	"time"
)

// Header 表示单个 HTTP 头（键值对）。
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// WSMessage 表示一条被捕获的 WebSocket 帧消息。
// Direction 指明方向（request=客户端→服务端，response=服务端→客户端）。
type WSMessage struct {
	Direction string `json:"direction"` // "request" | "response"
	Opcode    string `json:"opcode"`    // "text" | "binary" | "ping" | "pong" | "close"
	Data      []byte `json:"data"`      // 帧载荷（JSON 中为 base64）
	Timestamp int64  `json:"timestamp"` // 接收时间（unix 毫秒）
}

// MarshalBody 将字节体编码为 base64 字符串，用于 JSON 传输。
func MarshalBody(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// UnmarshalBody 从 base64 字符串解码字节体（与 MarshalBody 互为逆操作）。
func UnmarshalBody(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// Flow 表示一条被捕获的 HTTP 事务（请求 + 响应）。
//
// 生命周期：请求到达代理 → 构造 Flow（StartedAt）→ 上游转发 → 响应返回
// → 补全响应字段（CompletedAt）→ 通过事件推送前端。期间工具管线可以
// 修改 Flow 的任意字段。
type Flow struct {
	ID string `json:"id"`

	// Source / origin information（来源信息）
	SourceID   string `json:"sourceId"`   // 来源列表条目 id（"local" 或设备 id）
	SourceName string `json:"sourceName"` // 捕获来源的显示名
	ClientAddr string `json:"clientAddr"` // 客户端地址，如 "127.0.0.1:54321"

	// Request line（请求行）
	Scheme      string `json:"scheme"` // "http" | "https"
	Method      string `json:"method"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	Query       string `json:"query"` // 不含前导 '?'
	FullURL     string `json:"fullUrl"`
	HTTPVersion string `json:"httpVersion"` // "HTTP/1.1" | "HTTP/2.0"
	TLS         bool   `json:"tls"`         // 是否经 MITM 解密

	// Request（请求部分）
	RequestHeaders    []Header `json:"requestHeaders"`
	RequestBody       []byte   `json:"requestBody"`
	RequestSize       int64    `json:"requestSize"`
	RequestMimeType   string   `json:"requestMimeType"`
	RequestCookies    []Cookie `json:"requestCookies,omitempty"`
	RequestTruncated  bool     `json:"requestTruncated"`

	// Response（响应部分；未完成/出错时 ResponseStatus 为 0）
	ResponseStatus    int      `json:"responseStatus"`
	ResponseReason    string   `json:"responseReason"`
	ResponseHeaders   []Header `json:"responseHeaders"`
	ResponseBody      []byte   `json:"responseBody"`
	ResponseSize      int64    `json:"responseSize"`
	ResponseMimeType  string   `json:"responseMimeType"`
	ResponseCookies   []Cookie `json:"responseCookies,omitempty"`
	ResponseTruncated bool     `json:"responseTruncated"`

	// Timing（unix 毫秒时间戳）
	StartedAt   int64 `json:"startedAt"`   // 请求到达代理的时间
	CompletedAt int64 `json:"completedAt"` // 响应写回客户端的时间
	Duration    int64 `json:"duration"`    // 总耗时（毫秒）

	// WebSocket
	IsWebSocket     bool        `json:"isWebSocket"`
	WebSocketClosed bool        `json:"webSocketClosed"`
	WebSocketMsgs   []WSMessage `json:"webSocketMessages,omitempty"`

	// State（状态）
	Error    string `json:"error,omitempty"` // 非空表示请求失败（如超时、连接拒绝）
	ToolType string `json:"toolType,omitempty"` // 生效的工具标识
	IsPinned bool   `json:"isPinned"`
	IsSaved  bool   `json:"isSaved"` // 已持久化到保存的会话

	// Internal（内部字段）：断点暂停等待用户决策时置位。
	WaitingForDecision bool `json:"-"`
}

// Cookie 是解析后的 HTTP Cookie。
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  string `json:"expires,omitempty"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
	SameSite string `json:"sameSite,omitempty"`
}

// NewFlow 创建一个带唯一 ID 的 Flow，并记录开始时间。
func NewFlow() *Flow {
	return &Flow{
		ID:        GenID(),
		StartedAt: time.Now().UnixMilli(),
	}
}

// RequestContentEncoding 返回请求体的 Content-Encoding 头值。
func (f *Flow) RequestContentEncoding() string {
	for _, h := range f.RequestHeaders {
		if equalFold(h.Name, "Content-Encoding") {
			return h.Value
		}
	}
	return ""
}

// ResponseContentEncoding 返回响应体的 Content-Encoding 头值。
func (f *Flow) ResponseContentEncoding() string {
	for _, h := range f.ResponseHeaders {
		if equalFold(h.Name, "Content-Encoding") {
			return h.Value
		}
	}
	return ""
}

// HeaderValue 返回 headers 中第一个匹配 name（忽略大小写）的头值。
func HeaderValue(headers []Header, name string) string {
	for _, h := range headers {
		if equalFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// SetHeader 替换已存在的同名头（忽略大小写），不存在则追加。
func SetHeader(headers *[]Header, name, value string) {
	for i := range *headers {
		if equalFold((*headers)[i].Name, name) {
			(*headers)[i].Value = value
			return
		}
	}
	*headers = append(*headers, Header{Name: name, Value: value})
}

// DeleteHeader 删除所有匹配 name（忽略大小写）的头。
func DeleteHeader(headers *[]Header, name string) {
	out := (*headers)[:0]
	for _, h := range *headers {
		if !equalFold(h.Name, name) {
			out = append(out, h)
		}
	}
	*headers = out
}

// equalFold 是大小写不敏感的字节比较（仅 ASCII，避免依赖 strings 包开销）。
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
