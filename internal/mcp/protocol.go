// Package mcp 实现 MCP（Model Context Protocol）服务端，让 AI 客户端控制整个应用。
//
// 协议分两代，本包按 **dual-era** 实现（见官方 versioning 规范）：
//
//   - 现代（2026-07-28 起）：没有握手会话。每个请求自带 _meta.protocolVersion
//     （HTTP 上还有 MCP-Protocol-Version 头），服务端必须实现 server/discover；
//     版本不支持时回 -32022 并在 data.supported 里列出支持的版本。
//   - 旧版（2025-11-25 及更早）：以 initialize 建立会话后再通信。
//
// 本文件只放与"代"无关的协议骨架：JSON-RPC 2.0 编解码、错误码、版本协商与
// _meta 解析。工具注册表、传输层（HTTP / stdio）在各自文件里实现。
package mcp

import (
	"encoding/json"
	"fmt"
)

// 支持的协议版本，新的在前。首个为现代版本（无握手），其余为 legacy 版本
// （initialize 握手）。客户端请求到哪个就回哪个，都不支持则回 -32022。
var supportedVersions = []string{
	"2026-07-28", // 现代：per-request _meta，无握手，必须实现 server/discover
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// ModernVersion 是当前的现代协议版本（发送它的客户端不带握手）。
const ModernVersion = "2026-07-28"

// SupportedVersions 返回支持的版本列表副本（新的在前）。
func SupportedVersions() []string {
	out := make([]string, len(supportedVersions))
	copy(out, supportedVersions)
	return out
}

// IsModern 判断某版本是否属于"现代"（无握手）代。
func IsModern(version string) bool { return version >= ModernVersion }

// Negotiate 校验客户端请求的版本：支持则原样返回，否则 ok=false，
// 调用方应回 UnsupportedVersionError（-32022）。
func Negotiate(requested string) (string, bool) {
	for _, v := range supportedVersions {
		if v == requested {
			return v, true
		}
	}
	return "", false
}

// JSON-RPC 2.0 标准错误码 + MCP 自定义错误码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeUnsupportedProtocolVersion 见规范 versioning：必须带 data.supported。
	CodeUnsupportedProtocolVersion = -32022
)

// Request 是一条 JSON-RPC 请求（通知时 ID 为空）。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification 判断是否为通知（无 id，按规范不应回复）。
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response 是一条 JSON-RPC 响应：Result 与 Error 二选一。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error 是 JSON-RPC 错误对象。
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("mcp: %d %s", e.Code, e.Message) }

// Errorf 构造一个错误对象。
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// UnsupportedVersionError 构造 -32022，并按规范列出服务端支持的版本。
func UnsupportedVersionError(requested string) *Error {
	return &Error{
		Code:    CodeUnsupportedProtocolVersion,
		Message: "Unsupported protocol version",
		Data: map[string]any{
			"supported": SupportedVersions(),
			"requested": requested,
		},
	}
}

// ParseRequest 解析一条入站 JSON-RPC 消息。
func ParseRequest(raw []byte) (*Request, *Error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, Errorf(CodeParseError, "invalid JSON: %v", err)
	}
	if req.Method == "" {
		return nil, Errorf(CodeInvalidRequest, "missing method")
	}
	return &req, nil
}

// Meta 是 MCP 的 per-request 元数据（现代代用它携带版本、身份与能力）。
type Meta struct {
	ProtocolVersion string          `json:"protocolVersion,omitempty"`
	ClientInfo      *Implementation `json:"clientInfo,omitempty"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
}

// Implementation 是客户端/服务端的自我描述。
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ParseMeta 从 params 里取出 _meta（没有则返回空 Meta，不报错：
// legacy 请求本来就没有它）。
func ParseMeta(params json.RawMessage) (*Meta, *Error) {
	if len(params) == 0 {
		return &Meta{}, nil
	}
	var wrapper struct {
		Meta *Meta `json:"_meta"`
	}
	if err := json.Unmarshal(params, &wrapper); err != nil {
		return nil, Errorf(CodeInvalidParams, "invalid params: %v", err)
	}
	if wrapper.Meta == nil {
		return &Meta{}, nil
	}
	return wrapper.Meta, nil
}

// Result 构造成功响应（ID 原样回显，可能是字符串/数字/null）。
func Result(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: result}
}

// ErrorResponse 构造错误响应。
func ErrorResponse(id json.RawMessage, err *Error) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: err}
}
