package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func newTestService(perms Permissions) *Service {
	reg, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	return NewService(reg, func() Permissions { return perms }, Implementation{Name: "wproxyman", Version: "test"})
}

// call 发一条现代请求（自带 _meta 版本）。
func call(t *testing.T, s *Service, id, method, params string) *Response {
	t.Helper()
	raw := `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `"`
	if params != "" {
		raw += `,"params":` + params
	} else {
		raw += `,"params":{"_meta":{"protocolVersion":"` + ModernVersion + `"}}`
	}
	raw += `}`
	return s.Handle([]byte(raw))
}

func decode[T any](t *testing.T, r *Response) T {
	t.Helper()
	if r == nil {
		t.Fatalf("响应为空")
	}
	if r.Error != nil {
		t.Fatalf("意外的错误：%+v", r.Error)
	}
	raw, _ := json.Marshal(r.Result)
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析结果失败：%v (%s)", err, raw)
	}
	return out
}

func TestModernDiscoverAndToolsList(t *testing.T) {
	s := newTestService(Permissions{})

	// 现代代必须实现 server/discover。
	d := decode[struct {
		ProtocolVersion   string   `json:"protocolVersion"`
		SupportedVersions []string `json:"supportedVersions"`
		ServerInfo        Implementation
	}](t, call(t, s, "1", "server/discover", ""))
	if d.ProtocolVersion != ModernVersion || len(d.SupportedVersions) == 0 {
		t.Fatalf("discover 结果不对：%+v", d)
	}
	if d.ServerInfo.Name != "wproxyman" {
		t.Fatalf("serverInfo 不对：%+v", d.ServerInfo)
	}

	// tools/list 带上全部工具。
	lst := decode[struct {
		Tools []Tool `json:"tools"`
	}](t, call(t, s, "2", "tools/list", ""))
	if len(lst.Tools) == 0 {
		t.Fatalf("tools/list 为空")
	}
	for _, tool := range lst.Tools {
		if tool.InputSchema == nil || tool.Description == "" {
			t.Fatalf("工具缺少 schema 或描述：%+v", tool)
		}
	}
}

func TestUnsupportedVersionRejected(t *testing.T) {
	s := newTestService(Permissions{AllowWrite: true, AllowDangerous: true})
	r := s.Handle([]byte(`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{"_meta":{"protocolVersion":"1900-01-01"}}}`))
	if r == nil || r.Error == nil || r.Error.Code != CodeUnsupportedProtocolVersion {
		t.Fatalf("应回 -32022：%+v", r)
	}
	data, _ := r.Error.Data.(map[string]any)
	if data == nil || data["supported"] == nil {
		t.Fatalf("错误里应带 supported 列表：%+v", r.Error)
	}
}

func TestLegacyInitialize(t *testing.T) {
	s := newTestService(Permissions{})
	// 旧版客户端：没有 _meta，先 initialize。
	r := s.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"claude-desktop"}}}`))
	got := decode[struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      Implementation
	}](t, r)
	if got.ProtocolVersion != "2025-06-18" {
		t.Fatalf("legacy 应回客户端请求的受支持版本，实际 %q", got.ProtocolVersion)
	}
	// 握手后的通知不需要回复。
	if n := s.Handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); n != nil {
		t.Fatalf("通知不应产生响应：%+v", n)
	}
}

func TestCallToolPermissionTiers(t *testing.T) {
	// 默认只开 read：只读工具可用。
	s := newTestService(Permissions{})
	okRes := call(t, s, "1", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"get_flows_raw","arguments":{}}`)
	decode[map[string]any](t, okRes)

	// 写档未开 → 拒绝，并说明去哪里开。
	r := call(t, s, "2", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"set_port","arguments":{"port":1}}`)
	if r.Error == nil || !strings.Contains(r.Error.Message, "设置 → MCP") {
		t.Fatalf("写档未开应被拒并给出指引：+%v", r)
	}
	// 危险档即使开了写档也仍被拒。
	s2 := newTestService(Permissions{AllowWrite: true})
	r = call(t, s2, "3", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"clear_flows","arguments":{}}`)
	if r.Error == nil || !strings.Contains(r.Error.Message, "dangerous") {
		t.Fatalf("危险档未开应被拒：%+v", r)
	}
	// 全开时危险工具可用。
	s3 := newTestService(Permissions{AllowWrite: true, AllowDangerous: true})
	decode[map[string]any](t, call(t, s3, "4", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"clear_flows","arguments":{}}`))
}

func TestCallToolResultsAndErrors(t *testing.T) {
	s := newTestService(Permissions{AllowWrite: true})

	// 成功：content 里是 JSON 文本，isError=false。
	r := call(t, s, "1", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"get_flows_raw","arguments":{}}`)
	out := decode[struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}](t, r)
	if len(out.Content) != 1 || out.Content[0].Type != "text" || out.IsError {
		t.Fatalf("成功结果形状不对：%+v", out)
	}
	if !strings.Contains(out.Content[0].Text, "[") {
		t.Fatalf("文本块里应是 JSON：%s", out.Content[0].Text)
	}

	// 未知工具 → 协议级错误。
	if r := call(t, s, "2", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"nope","arguments":{}}`); r.Error == nil || r.Error.Code != CodeMethodNotFound {
		t.Fatalf("未知工具应回 -32601：%+v", r)
	}
	// 参数错误 → 协议级错误（模型写错了参数）。
	if r := call(t, s, "3", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"set_port","arguments":{}}`); r.Error == nil || r.Error.Code != CodeInvalidParams {
		t.Fatalf("缺参数应回 -32602：%+v", r)
	}
	// 未知方法 → -32601。
	if r := call(t, s, "4", "no/such/method", ""); r.Error == nil || r.Error.Code != CodeMethodNotFound {
		t.Fatalf("未知方法应回 -32601：%+v", r)
	}
	// resources/prompts 至少要有合法形状。
	decode[map[string]any](t, call(t, s, "5", "resources/list", ""))
	decode[map[string]any](t, call(t, s, "6", "prompts/list", ""))
}

func TestAuditRecordsTierAndArgs(t *testing.T) {
	s := newTestService(Permissions{})
	// 用夹具里真实存在的工具名（get_flows_raw 是 testSpecs 里的只读工具）。
	call(t, s, "1", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"get_flows_raw","arguments":{}}`)
	call(t, s, "2", "tools/call", `{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"set_port","arguments":{"port":10086}}`)

	entries := s.Audit().Entries() // 新的在前
	if len(entries) != 2 {
		t.Fatalf("应有 2 条审计，实际 %d", len(entries))
	}
	if entries[0].Tool != "set_port" || entries[0].Tier != TierWrite || entries[0].OK {
		t.Fatalf("被拒绝的调用也应记审计：%+v", entries[0])
	}
	if entries[0].ArgsHash == "" || entries[0].ArgsLen == 0 {
		t.Fatalf("审计应记录参数指纹与长度：%+v", entries[0])
	}
	if entries[1].Tool != "get_flows_raw" || entries[1].Tier != TierRead || !entries[1].OK {
		t.Fatalf("成功调用审计不对：%+v", entries[1])
	}
	// 参数内容不能落进审计（可能含敏感数据）。
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "10086") {
		t.Fatalf("审计不应包含参数内容：%s", raw)
	}
}

func TestAuditRingBuffer(t *testing.T) {
	log := NewAuditLog(3)
	for i := 0; i < 5; i++ {
		log.add(AuditEntry{Tool: string(rune('a' + i))})
	}
	got := log.Entries()
	if len(got) != 3 {
		t.Fatalf("环形缓冲应只留 3 条，实际 %d", len(got))
	}
	if got[0].Tool != "e" || got[2].Tool != "c" {
		t.Fatalf("应保留最新的三条且新的在前：%+v", got)
	}
}
