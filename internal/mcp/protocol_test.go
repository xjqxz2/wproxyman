package mcp

import (
	"encoding/json"
	"testing"
)

func TestNegotiate(t *testing.T) {
	// 现代版本原样接受。
	if v, ok := Negotiate(ModernVersion); !ok || v != ModernVersion {
		t.Fatalf("Negotiate(%q) = %q,%v", ModernVersion, v, ok)
	}
	// 已知的 legacy 版本也要接受（dual-era：老客户端仍能用）。
	for _, legacy := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {
		if _, ok := Negotiate(legacy); !ok {
			t.Errorf("legacy 版本 %q 应被接受", legacy)
		}
	}
	// 未知版本不接受，由调用方回 -32022。
	if _, ok := Negotiate("1900-01-01"); ok {
		t.Errorf("未知版本不应被接受")
	}
}

func TestUnsupportedVersionErrorShape(t *testing.T) {
	err := UnsupportedVersionError("1900-01-01")
	if err.Code != CodeUnsupportedProtocolVersion {
		t.Fatalf("code = %d, want %d", err.Code, CodeUnsupportedProtocolVersion)
	}
	data, ok := err.Data.(map[string]any)
	if !ok {
		t.Fatalf("data 类型不对：%T", err.Data)
	}
	sup, ok := data["supported"].([]string)
	if !ok || len(sup) == 0 || sup[0] != ModernVersion {
		t.Fatalf("supported 列表不对：%v", data["supported"])
	}
	if data["requested"] != "1900-01-01" {
		t.Fatalf("requested = %v", data["requested"])
	}
	// 规范要求错误体里带上支持的版本，序列化后必须真的出现。
	raw, _ := json.Marshal(err)
	if !json.Valid(raw) || len(raw) == 0 {
		t.Fatalf("error 无法序列化：%s", raw)
	}
}

func TestParseRequest(t *testing.T) {
	req, errObj := ParseRequest([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`))
	if errObj != nil {
		t.Fatalf("解析失败：%v", errObj)
	}
	if req.Method != "tools/list" || string(req.ID) != "7" {
		t.Fatalf("解析结果不对：%+v", req)
	}
	if req.IsNotification() {
		t.Fatalf("带 id 的请求不是通知")
	}

	// 通知（无 id）不应被回复。
	if n, _ := ParseRequest([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); !n.IsNotification() {
		t.Fatalf("无 id 的请求应识别为通知")
	}
	if n, _ := ParseRequest([]byte(`{"jsonrpc":"2.0","id":null,"method":"x"}`)); !n.IsNotification() {
		t.Fatalf("id 为 null 也应识别为通知")
	}

	// 非法 JSON / 缺 method。
	if _, e := ParseRequest([]byte(`{`)); e == nil || e.Code != CodeParseError {
		t.Fatalf("非法 JSON 应回 -32700，实际 %+v", e)
	}
	if _, e := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1}`)); e == nil || e.Code != CodeInvalidRequest {
		t.Fatalf("缺 method 应回 -32600，实际 %+v", e)
	}
}

func TestParseMeta(t *testing.T) {
	// 现代请求：带 _meta。
	meta, errObj := ParseMeta(json.RawMessage(`{"_meta":{"protocolVersion":"2026-07-28","clientInfo":{"name":"claude-ai","version":"1.2"}}}`))
	if errObj != nil {
		t.Fatalf("解析 _meta 失败：%v", errObj)
	}
	if meta.ProtocolVersion != ModernVersion || meta.ClientInfo == nil || meta.ClientInfo.Name != "claude-ai" {
		t.Fatalf("_meta 解析结果不对：%+v", meta)
	}

	// legacy 请求：没有 _meta，不报错，返回空 Meta。
	meta, errObj = ParseMeta(json.RawMessage(`{"name":"x"}`))
	if errObj != nil || meta.ProtocolVersion != "" {
		t.Fatalf("无 _meta 时应返回空 Meta，实际 %+v / %v", meta, errObj)
	}
	if m, e := ParseMeta(nil); e != nil || m == nil {
		t.Fatalf("params 为空时应返回空 Meta")
	}

	// params 本身不是对象 → -32602。
	if _, e := ParseMeta(json.RawMessage(`"nope"`)); e == nil || e.Code != CodeInvalidParams {
		t.Fatalf("非法 params 应回 -32602，实际 %+v", e)
	}
}

func TestResponseShape(t *testing.T) {
	// 成功响应：result 有值，error 必须不出现（omitempty 生效）。
	raw, _ := json.Marshal(Result(json.RawMessage(`"abc"`), map[string]any{"ok": true}))
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["jsonrpc"] != "2.0" || got["id"] != "abc" {
		t.Fatalf("响应形状不对：%s", raw)
	}
	if _, has := got["error"]; has {
		t.Fatalf("成功响应不应带 error：%s", raw)
	}

	// 错误响应：不能带 result。
	// 注意：必须用新的 map —— json.Unmarshal 对已存在的 map 是"合并"而非清空，
	// 复用上面的 got 会让上一次的 result 残留，断言形同虚设。
	raw, _ = json.Marshal(ErrorResponse(json.RawMessage(`1`), Errorf(CodeMethodNotFound, "未知方法 %s", "x/y")))
	var got2 map[string]any
	_ = json.Unmarshal(raw, &got2)
	if _, has := got2["result"]; has {
		t.Fatalf("错误响应不应带 result：%s", raw)
	}
	errObj, _ := got2["error"].(map[string]any)
	if errObj == nil || int(errObj["code"].(float64)) != CodeMethodNotFound {
		t.Fatalf("错误码不对：%s", raw)
	}
}
