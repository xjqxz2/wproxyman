package mcp

import (
	"encoding/json"
	"testing"
)

// 自定义工具（包装工具）机制：list_flows / get_flow_body 这类需要重新包装的能力
// 靠它实现 —— 反射只能"原样转发到 App 方法"，无法做过滤/分页/裁剪。

func TestAddHandlerAndCall(t *testing.T) {
	r, _, err := NewRegistry(&fakeTarget{}, testSpecs())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	type listArgs struct {
		Host  string `json:"host"`
		Limit int    `json:"limit"`
	}
	var gotArgs listArgs
	err = r.AddHandler(
		ToolSpec{Name: "list_flows", Method: "list_flows", Description: "列出流量（过滤+分页）",
			Group: "flows", Tier: TierRead},
		[]Param{
			{Name: "host", Type: "string", Description: "只看该主机"},
			{Name: "limit", Type: "integer", Description: "返回条数", Default: 50},
			{Name: "status", Type: "string", Description: "状态筛选", Enum: []string{"2xx", "4xx", "5xx", "failed"}},
			{Name: "fields", Type: "array", Items: "string", Description: "额外字段"},
			{Name: "onlyStream", Type: "boolean", Description: "只看流式"},
		},
		func(args json.RawMessage) (any, *Error) {
			if err := json.Unmarshal(args, &gotArgs); err != nil {
				return nil, Errorf(CodeInvalidParams, "参数解析失败：%v", err)
			}
			return map[string]any{"matched": 3, "host": gotArgs.Host}, nil
		},
	)
	if err != nil {
		t.Fatalf("AddHandler: %v", err)
	}

	// 出现在工具列表里，且 Schema 反映了参数细节。
	var tool *Tool
	tools := r.Tools()
	for i := range tools {
		if tools[i].Name == "list_flows" {
			tool = &tools[i]
		}
	}
	if tool == nil {
		t.Fatalf("自定义工具未出现在列表里")
	}
	props := tool.InputSchema["properties"].(map[string]any)
	if props["status"].(map[string]any)["enum"] == nil {
		t.Fatalf("enum 未写进 Schema：%v", props["status"])
	}
	if props["limit"].(map[string]any)["default"] != 50 {
		t.Fatalf("default 未写进 Schema：%v", props["limit"])
	}
	if props["fields"].(map[string]any)["items"].(map[string]any)["type"] != "string" {
		t.Fatalf("array items 未写进 Schema：%v", props["fields"])
	}
	if req, _ := tool.InputSchema["required"].([]string); len(req) != 0 {
		t.Fatalf("没有必填参数时 required 应为空：%v", req)
	}

	// 调用确实进到 handler，参数也被解析了。
	res, errObj := r.Call("list_flows", json.RawMessage(`{"host":"api.example.com","limit":10}`))
	if errObj != nil {
		t.Fatalf("调用失败：%v", errObj)
	}
	if gotArgs.Host != "api.example.com" || gotArgs.Limit != 10 {
		t.Fatalf("参数没传进 handler：%+v", gotArgs)
	}
	m, _ := res.(map[string]any)
	if m["matched"] != 3 {
		t.Fatalf("handler 返回值没透传：%#v", res)
	}

	// 无参调用：handler 收到的是 {}，不会 panic，也不会带上一轮的参数
	// （注意 json.Unmarshal 到已填充的结构体不会清空未出现的字段，所以要先归零）。
	gotArgs = listArgs{}
	if _, errObj = r.Call("list_flows", nil); errObj != nil {
		t.Fatalf("无参调用失败：%v", errObj)
	}
	if gotArgs.Host != "" || gotArgs.Limit != 0 {
		t.Fatalf("无参调用不该带参数：%+v", gotArgs)
	}

	// handler 返回错误时原样透出。
	if _, e := r.Call("list_flows", json.RawMessage(`{"host":123}`)); e == nil || e.Code != CodeInvalidParams {
		t.Fatalf("handler 的错误应透出，实际 %+v", e)
	}
}

func TestAddHandlerValidation(t *testing.T) {
	r, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	ok := func(json.RawMessage) (any, *Error) { return nil, nil }

	// 与反射工具重名（testSpecs 里有 get_flows_raw）。
	if err := r.AddHandler(ToolSpec{Name: "get_flows_raw", Method: "get_flows_raw"}, nil, ok); err == nil {
		t.Fatalf("重名应被拒绝")
	}
	// 缺实现。
	if err := r.AddHandler(ToolSpec{Name: "x", Method: "x"}, nil, nil); err == nil {
		t.Fatalf("缺实现应被拒绝")
	}
	// 缺名字。
	if err := r.AddHandler(ToolSpec{Method: "x"}, nil, ok); err == nil {
		t.Fatalf("缺名字应被拒绝")
	}
	// 参数类型非法。
	if err := r.AddHandler(ToolSpec{Name: "y", Method: "y"}, []Param{{Name: "a", Type: "func"}}, ok); err == nil {
		t.Fatalf("非法参数类型应被拒绝")
	}
	// 无名参数。
	if err := r.AddHandler(ToolSpec{Name: "z", Method: "z"}, []Param{{Type: "string"}}, ok); err == nil {
		t.Fatalf("无名参数应被拒绝")
	}
}

func TestHandlersCoexistAndStaySorted(t *testing.T) {
	r, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	ok := func(json.RawMessage) (any, *Error) { return map[string]any{"ok": true}, nil }
	for _, name := range []string{"zzz_tool", "aaa_tool"} {
		if err := r.AddHandler(ToolSpec{Name: name, Method: name, Group: "custom", Tier: TierRead,
			Description: "测试"}, nil, ok); err != nil {
			t.Fatalf("AddHandler(%s): %v", name, err)
		}
	}
	tools := r.Tools()
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name > tools[i].Name {
			t.Fatalf("加入自定义工具后顺序被打乱：%s > %s", tools[i-1].Name, tools[i].Name)
		}
	}
	// 档位与分组照样可用。
	if tier, ok := r.TierOf("aaa_tool"); !ok || tier != TierRead {
		t.Fatalf("TierOf 对自定义工具失效：%v %v", tier, ok)
	}
	if len(r.Groups()["custom"]) != 2 {
		t.Fatalf("自定义工具未进分组：%v", r.Groups())
	}
	// 反射工具仍在。
	if _, ok := r.TierOf("set_port"); !ok {
		t.Fatalf("反射工具不应受影响")
	}
}
