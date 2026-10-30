package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// 服务层：把注册表、权限档、审计日志与 JSON-RPC 分发串在一起。
// 传输层（HTTP / stdio）只负责把字节喂给 Handle 并把响应写回去。

// Permissions 是当前允许调用的档位（由设置动态提供，默认只允许 read）。
type Permissions struct {
	AllowWrite     bool
	AllowDangerous bool
}

// Allowed 判断某档位当前是否放行。
func (p Permissions) Allowed(t Tier) bool {
	switch t {
	case TierRead:
		return true
	case TierWrite:
		return p.AllowWrite
	case TierDangerous:
		return p.AllowDangerous
	default:
		return false
	}
}

// AuditEntry 是一条调用记录（只记参数摘要，不记内容本身）。
type AuditEntry struct {
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`
	Tier     Tier      `json:"tier"`
	ArgsHash string    `json:"argsHash"` // 参数指纹：便于比对，不泄露内容
	ArgsLen  int       `json:"argsLen"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
}

// AuditLog 是固定容量的环形缓冲：满了丢最旧的，不会无限增长。
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	cap     int
}

// NewAuditLog 创建审计日志（cap<=0 时用默认 500）。
func NewAuditLog(capacity int) *AuditLog {
	if capacity <= 0 {
		capacity = 500
	}
	return &AuditLog{cap: capacity}
}

func (l *AuditLog) add(e AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if len(l.entries) > l.cap {
		l.entries = l.entries[len(l.entries)-l.cap:]
	}
}

// Entries 返回审计记录副本（新的在前）。
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, 0, len(l.entries))
	for i := len(l.entries) - 1; i >= 0; i-- {
		out = append(out, l.entries[i])
	}
	return out
}

// Service 处理单条 JSON-RPC 请求。
type Service struct {
	registry *Registry
	perms    func() Permissions
	audit    *AuditLog
	info     Implementation
}

// NewService 组装服务。perms 每次调用都会重新求值，这样用户在设置里开关档位能立即生效。
func NewService(reg *Registry, perms func() Permissions, info Implementation) *Service {
	if perms == nil {
		perms = func() Permissions { return Permissions{} }
	}
	return &Service{registry: reg, perms: perms, audit: NewAuditLog(0), info: info}
}

// Audit 暴露审计日志（UI 要展示）。
func (s *Service) Audit() *AuditLog { return s.audit }

// Registry 暴露工具表（UI 需要展示工具数量）。
func (s *Service) Registry() *Registry { return s.registry }

// ServerInfo 返回服务自述。
func (s *Service) ServerInfo() Implementation { return s.info }

// Handle 处理一条入站消息；返回 nil 表示这是通知，不应回复。
func (s *Service) Handle(raw []byte) *Response {
	req, errObj := ParseRequest(raw)
	if errObj != nil {
		return ErrorResponse(nil, errObj)
	}
	meta, errObj := ParseMeta(req.Params)
	if errObj != nil {
		return ErrorResponse(req.ID, errObj)
	}

	// 现代代：请求自带版本。不支持就按规范回 -32022 并列出支持的版本。
	// 旧版（无 _meta）：走 initialize 握手。
	if meta.ProtocolVersion != "" {
		if _, ok := Negotiate(meta.ProtocolVersion); !ok {
			if req.IsNotification() {
				return nil
			}
			return ErrorResponse(req.ID, UnsupportedVersionError(meta.ProtocolVersion))
		}
	} else if req.Method != "initialize" && req.Method != "notifications/initialized" {
		// 无版本信息的现代方法调用：按规范应由传输层拒绝（缺 MCP-Protocol-Version）。
		// 这里给出明确错误，而不是含糊地当 legacy 处理。
		if req.IsNotification() {
			return nil
		}
		return ErrorResponse(req.ID, Errorf(CodeInvalidRequest,
			"缺少协议版本：请在 params._meta.protocolVersion 里声明（HTTP 上还应有 MCP-Protocol-Version 头）"))
	}

	resp := s.dispatch(req, meta)
	if req.IsNotification() {
		return nil
	}
	return resp
}

func (s *Service) dispatch(req *Request, meta *Meta) *Response {
	switch req.Method {
	case "initialize": // legacy 握手
		return Result(req.ID, s.initializeResult(req.Params))
	case "server/discover": // 现代代必须实现
		return Result(req.ID, map[string]any{
			"protocolVersion":   ModernVersion,
			"supportedVersions": SupportedVersions(),
			"serverInfo":        s.info,
			"capabilities":      s.capabilities(),
		})
	case "tools/list":
		return Result(req.ID, map[string]any{"tools": s.registry.Tools()})
	case "tools/call":
		return s.callTool(req)
	case "resources/list":
		// 目前只暴露一个资源：CA 证书（避免把 PEM 塞进工具返回值）。
		return Result(req.ID, map[string]any{"resources": []map[string]any{
			{"uri": "wproxyman://ca-cert", "name": "WProxyman CA certificate",
				"mimeType":    "application/x-pem-file",
				"description": "本地抓包 CA 证书（PEM），用于在设备上安装并信任"},
		}})
	case "prompts/list":
		return Result(req.ID, map[string]any{"prompts": []any{}})
	case "notifications/initialized", "notifications/cancelled":
		return nil
	default:
		return ErrorResponse(req.ID, Errorf(CodeMethodNotFound, "不支持的方法 %s", req.Method))
	}
}

// tools/call：权限校验 → 调用 → 审计。
func (s *Service) callTool(req *Request) *Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return ErrorResponse(req.ID, Errorf(CodeInvalidParams, "tools/call 参数解析失败：%v", err))
	}
	if params.Name == "" {
		return ErrorResponse(req.ID, Errorf(CodeInvalidParams, "缺少工具名 name"))
	}

	tier, ok := s.registry.TierOf(params.Name)
	if !ok {
		return ErrorResponse(req.ID, Errorf(CodeMethodNotFound, "未知工具 %s", params.Name))
	}
	if !s.perms().Allowed(tier) {
		s.record(params.Name, tier, params.Arguments, false, "权限不足")
		return ErrorResponse(req.ID, Errorf(CodeInvalidRequest,
			"工具 %s 属于 %s 档，当前未允许调用。请在 WProxyman 设置 → MCP 中开启对应档位。", params.Name, tier))
	}

	result, errObj := s.registry.Call(params.Name, params.Arguments)
	if errObj != nil {
		// 工具自身执行失败（而非协议/权限问题）按结果返回 isError，
		// 让模型能读到原因并自行调整；协议级错误才走 JSON-RPC error。
		s.record(params.Name, tier, params.Arguments, false, errObj.Message)
		if errObj.Code == CodeInternalError {
			return Result(req.ID, toolErrorResult(errObj.Message))
		}
		return ErrorResponse(req.ID, errObj)
	}
	s.record(params.Name, tier, params.Arguments, true, "")
	return Result(req.ID, toolResult(result))
}

// toolResult 把工具返回值包成 MCP 的 content 数组（文本块里放 JSON）。
func toolResult(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", v))
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(raw)}},
		"isError": false,
	}
}

func toolErrorResult(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

func (s *Service) capabilities() map[string]any {
	return map[string]any{
		"tools":     map[string]any{"listChanged": false},
		"resources": map[string]any{"listChanged": false},
	}
}

// initializeResult 处理 legacy 握手：版本取客户端请求值（支持的话），否则给最新的现代版本。
func (s *Service) initializeResult(params json.RawMessage) map[string]any {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &in)
	version := ModernVersion
	if in.ProtocolVersion != "" {
		if v, ok := Negotiate(in.ProtocolVersion); ok {
			version = v
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    s.capabilities(),
		"serverInfo":      s.info,
	}
}

// record 写一条审计；参数只留指纹与长度，不落内容（可能含敏感数据）。
func (s *Service) record(tool string, tier Tier, args json.RawMessage, ok bool, errMsg string) {
	sum := sha256.Sum256(args)
	s.audit.add(AuditEntry{
		At:       time.Now(),
		Tool:     tool,
		Tier:     tier,
		ArgsHash: hex.EncodeToString(sum[:8]),
		ArgsLen:  len(args),
		OK:       ok,
		Error:    errMsg,
	})
}
