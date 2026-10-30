package mcp

import (
	"encoding/json"
	"testing"
)

// fakeTarget 模拟 App：覆盖有参/无参、多返回值、error 返回、结构体参数、
// 以及必须被排除的生命周期方法。
type fakeTarget struct {
	lastPort   int
	lastPinned bool
	lastID     string
	settings   fakeSettings
	cleanupRun bool
}

type fakeSettings struct {
	ProxyPort int    `json:"proxyPort"`
	Theme     string `json:"theme"`
	inner     int    // 未导出字段不应出现在 Schema 里
}

func (f *fakeTarget) GetFlows() []string { return []string{"a", "b"} }
func (f *fakeTarget) SetPort(port int) (int, error) {
	f.lastPort = port
	return port, nil
}
func (f *fakeTarget) SetPinned(id string, pinned bool) error {
	f.lastID, f.lastPinned = id, pinned
	return nil
}
func (f *fakeTarget) SaveSettings(s fakeSettings) error {
	f.settings = s
	return nil
}
func (f *fakeTarget) ClearFlows() { f.lastID = "cleared" }
func (f *fakeTarget) Cleanup()    { f.cleanupRun = true }

func testSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "list_flows", Method: "GetFlows", Description: "列出流量", Group: "flows", Tier: TierRead},
		{Name: "set_port", Method: "SetPort", Description: "改端口", Group: "proxy", Tier: TierWrite, Params: []string{"port"}},
		{Name: "set_pinned", Method: "SetPinned", Description: "置顶", Group: "flows", Tier: TierWrite, Params: []string{"id", "pinned"}},
		{Name: "save_settings", Method: "SaveSettings", Description: "存设置", Group: "settings", Tier: TierWrite, Params: []string{"settings"}},
		{Name: "clear_flows", Method: "ClearFlows", Description: "清空流量", Group: "flows", Tier: TierDangerous},
		// 生命周期方法：元数据里写了也必须被硬性拒绝（见 deniedMethods）
		{Name: "cleanup", Method: "Cleanup", Description: "内部清理", Group: "internal", Tier: TierDangerous},
		// 元数据写错：参数个数不符 → 应只产生 warning，不进工具列表
		{Name: "set_port_bad", Method: "SetPort", Description: "参数写错", Group: "proxy", Tier: TierWrite, Params: []string{"port", "extra"}},
		// 元数据写了不存在的方法 → warning
		{Name: "ghost", Method: "NoSuchMethod", Description: "不存在", Group: "proxy", Tier: TierRead},
	}
}

func TestRegistryBuildAndWarnings(t *testing.T) {
	target := &fakeTarget{}
	r, warns, err := NewRegistry(target, testSpecs())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(warns) != 3 {
		t.Fatalf("应产生 3 条 warning（参数不符 + 方法不存在 + 生命周期方法被拒），实际 %d：%v", len(warns), warns)
	}
	names := map[string]bool{}
	for _, tool := range r.Tools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"list_flows", "set_port", "set_pinned", "save_settings", "clear_flows"} {
		if !names[want] {
			t.Errorf("工具 %s 应在列表里", want)
		}
	}
	if names["set_port_bad"] || names["ghost"] {
		t.Errorf("签名不符/方法不存在的条目不应进列表：%v", names)
	}
	// 生命周期方法是硬性拒绝：即使元数据里写了、目标上也有，也不能暴露。
	if names["cleanup"] {
		t.Errorf("Cleanup 属于生命周期方法，绝不应暴露给 MCP")
	}
	if target.cleanupRun {
		t.Errorf("构建注册表只做反射，不应触发任何方法")
	}
	// 顺序必须稳定（按名字排序），便于客户端与测试比对。
	tools := r.Tools()
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name > tools[i].Name {
			t.Fatalf("工具列表未按名字排序：%s > %s", tools[i-1].Name, tools[i].Name)
		}
	}
}

func TestRegistrySchema(t *testing.T) {
	r, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	byName := map[string]Tool{}
	for _, tool := range r.Tools() {
		byName[tool.Name] = tool
	}

	// 无参工具：properties 为空对象，required 为空数组。
	lf := byName["list_flows"].InputSchema
	if lf["type"] != "object" {
		t.Fatalf("list_flows schema type = %v", lf["type"])
	}
	if req, _ := lf["required"].([]string); len(req) != 0 {
		t.Fatalf("无参工具 required 应为空：%v", req)
	}

	// int → integer，bool → boolean，且 required 覆盖全部参数。
	sp := byName["set_port"].InputSchema
	props, _ := sp["properties"].(map[string]any)
	if props["port"].(map[string]any)["type"] != "integer" {
		t.Fatalf("set_port.port 应为 integer：%v", props["port"])
	}
	pin := byName["set_pinned"].InputSchema
	pprops, _ := pin["properties"].(map[string]any)
	if pprops["pinned"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("set_pinned.pinned 应为 boolean：%v", pprops["pinned"])
	}
	if req, _ := pin["required"].([]string); len(req) != 2 {
		t.Fatalf("set_pinned 应有两个必填参数：%v", req)
	}

	// 结构体参数：展开 JSON 标签，且不带未导出字段。
	ss := byName["save_settings"].InputSchema
	sprops, _ := ss["properties"].(map[string]any)
	settingsSchema, _ := sprops["settings"].(map[string]any)
	nested, _ := settingsSchema["properties"].(map[string]any)
	if nested["proxyPort"].(map[string]any)["type"] != "integer" {
		t.Fatalf("settings.proxyPort 应为 integer：%v", nested["proxyPort"])
	}
	if nested["theme"].(map[string]any)["type"] != "string" {
		t.Fatalf("settings.theme 应为 string：%v", nested["theme"])
	}
	if _, has := nested["inner"]; has {
		t.Fatalf("未导出字段不应出现在 Schema：%v", nested)
	}
	if settingsSchema["additionalProperties"] != true {
		t.Fatalf("结构体应允许额外字段：%v", settingsSchema)
	}
}

func TestRegistryAnnotations(t *testing.T) {
	r, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	byName := map[string]Tool{}
	for _, tool := range r.Tools() {
		byName[tool.Name] = tool
	}
	if byName["list_flows"].Annotations["readOnlyHint"] != true {
		t.Fatalf("只读工具应带 readOnlyHint：%v", byName["list_flows"].Annotations)
	}
	if byName["clear_flows"].Annotations["destructiveHint"] != true {
		t.Fatalf("危险工具应带 destructiveHint：%v", byName["clear_flows"].Annotations)
	}
}

func TestRegistryCall(t *testing.T) {
	target := &fakeTarget{}
	r, _, _ := NewRegistry(target, testSpecs())

	// 无参调用 → 返回被调用方法的返回值。
	res, errObj := r.Call("list_flows", nil)
	if errObj != nil {
		t.Fatalf("list_flows 调用失败：%v", errObj)
	}
	if got, ok := res.([]string); !ok || len(got) != 2 {
		t.Fatalf("返回值不对：%#v", res)
	}

	// 有参调用：参数确实传到了方法里。
	if _, errObj = r.Call("set_port", json.RawMessage(`{"port":10086}`)); errObj != nil {
		t.Fatalf("set_port 调用失败：%v", errObj)
	}
	if target.lastPort != 10086 {
		t.Fatalf("端口没传进方法：%d", target.lastPort)
	}

	// 多参数 + bool。
	if _, errObj = r.Call("set_pinned", json.RawMessage(`{"id":"f1","pinned":true}`)); errObj != nil {
		t.Fatalf("set_pinned 调用失败：%v", errObj)
	}
	if target.lastID != "f1" || !target.lastPinned {
		t.Fatalf("参数没传对：%s %v", target.lastID, target.lastPinned)
	}

	// 结构体参数：JSON 反序列化进结构体。
	if _, errObj = r.Call("save_settings", json.RawMessage(`{"settings":{"proxyPort":10087,"theme":"light"}}`)); errObj != nil {
		t.Fatalf("save_settings 调用失败：%v", errObj)
	}
	if target.settings.ProxyPort != 10087 || target.settings.Theme != "light" {
		t.Fatalf("结构体参数没传对：%+v", target.settings)
	}

	// 无返回值的方法返回 ok:true。
	if res, errObj = r.Call("clear_flows", nil); errObj != nil {
		t.Fatalf("clear_flows 调用失败：%v", errObj)
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("无返回值方法应回 ok:true：%#v", res)
	}
}

func TestRegistryCallErrors(t *testing.T) {
	target := &fakeTarget{}
	r, _, _ := NewRegistry(target, testSpecs())

	// 未知工具。
	if _, e := r.Call("nope", nil); e == nil || e.Code != CodeMethodNotFound {
		t.Fatalf("未知工具应回 -32601：%+v", e)
	}
	// 缺参数。
	if _, e := r.Call("set_port", json.RawMessage(`{}`)); e == nil || e.Code != CodeInvalidParams {
		t.Fatalf("缺参数应回 -32602：%+v", e)
	}
	// 类型不符（字符串给 int）。
	if _, e := r.Call("set_port", json.RawMessage(`{"port":"abc"}`)); e == nil || e.Code != CodeInvalidParams {
		t.Fatalf("类型不符应回 -32602：%+v", e)
	}
	// arguments 不是对象。
	if _, e := r.Call("set_port", json.RawMessage(`[1,2]`)); e == nil || e.Code != CodeInvalidParams {
		t.Fatalf("非对象 arguments 应回 -32602：%+v", e)
	}
	// 参数没传进方法（失败时不应有副作用）。
	if target.lastPort != 0 {
		t.Fatalf("失败的调用不应产生副作用：%d", target.lastPort)
	}
}

func TestRegistryTierAndGroups(t *testing.T) {
	r, _, _ := NewRegistry(&fakeTarget{}, testSpecs())
	if tier, ok := r.TierOf("clear_flows"); !ok || tier != TierDangerous {
		t.Fatalf("TierOf 结果不对：%v %v", tier, ok)
	}
	if _, ok := r.TierOf("nope"); ok {
		t.Fatalf("不存在的工具不应有档位")
	}
	groups := r.Groups()
	if len(groups["flows"]) != 3 {
		t.Fatalf("flows 分组应含 3 个工具：%v", groups)
	}
	if _, has := groups["internal"]; has {
		t.Fatalf("被拒绝的生命周期方法不应出现在任何分组：%v", groups)
	}
}
