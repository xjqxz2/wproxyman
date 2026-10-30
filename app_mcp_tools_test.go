package main

import (
	"encoding/json"
	"testing"

	"wproxyman/internal/flowquery"
	"wproxyman/internal/mcp"
	"wproxyman/internal/models"
)

// 通过注册表调用包装工具，验证"AI 视角"的查询行为确实按设计工作。
func TestMCPListFlowsTool(t *testing.T) {
	app := NewApp()
	app.flows = []*models.Flow{
		{ID: "1", Method: "GET", Host: "api.example.com", Path: "/v1/users", FullURL: "https://api.example.com/v1/users",
			ResponseStatus: 200, StartedAt: 100, Duration: 5, ResponseBody: []byte("SECRET-BODY")},
		{ID: "2", Method: "POST", Host: "api.example.com", Path: "/v1/orders", FullURL: "https://api.example.com/v1/orders",
			ResponseStatus: 500, StartedAt: 200, Duration: 8, Error: "boom"},
		{ID: "3", Method: "POST", Host: "api.openai.com", Path: "/v1/chat/completions",
			FullURL: "https://api.openai.com/v1/chat/completions", ResponseStatus: 200, StartedAt: 300,
			IsStream: true, StreamKind: "sse", SSEMsgs: []models.SSEMessage{{Seq: 1, Data: "x"}}},
	}

	reg, warns, err := app.buildMCPRegistry()
	if err != nil {
		t.Fatalf("buildMCPRegistry: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("元数据与签名不一致：%v", warns)
	}

	call := func(args string) map[string]any {
		res, errObj := reg.Call("list_flows", json.RawMessage(args))
		if errObj != nil {
			t.Fatalf("list_flows(%s) 失败：%v", args, errObj)
		}
		raw, _ := json.Marshal(res)
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("结果无法解析：%v", err)
		}
		return out
	}

	// 全量：3 条。
	all := call(`{}`)
	if int(all["matched"].(float64)) != 3 {
		t.Fatalf("matched = %v，期望 3", all["matched"])
	}
	if int(all["limit"].(float64)) != flowquery.DefaultLimit {
		t.Fatalf("默认 limit 应为 %d", flowquery.DefaultLimit)
	}

	// 按方法 + 状态分类筛选。
	post := call(`{"method":"POST"}`)
	if int(post["matched"].(float64)) != 2 {
		t.Fatalf("POST 应命中 2 条：%v", post["matched"])
	}
	failed := call(`{"status":"5xx"}`)
	if int(failed["matched"].(float64)) != 1 {
		t.Fatalf("5xx 应命中 1 条：%v", failed["matched"])
	}
	stream := call(`{"onlyStream":true}`)
	if int(stream["matched"].(float64)) != 1 {
		t.Fatalf("流式应命中 1 条：%v", stream["matched"])
	}

	// 分页：limit=1 时 hasMore=true，且能翻到第二页。
	page1 := call(`{"limit":1}`)
	if page1["hasMore"] != true || int(page1["returned"].(float64)) != 1 {
		t.Fatalf("第一页不对：%+v", page1)
	}
	page2 := call(`{"limit":1,"offset":1}`)
	first := page1["flows"].([]any)[0].(map[string]any)
	second := page2["flows"].([]any)[0].(map[string]any)
	if first["id"] == second["id"] {
		t.Fatalf("翻页返回了同一条：%v / %v", first["id"], second["id"])
	}

	// 投影：请求的字段有，未请求的没有，正文永远没有。
	proj := call(`{"search":"openai","fields":["fullUrl","streamKind","eventCount"]}`)
	raw, _ := json.Marshal(proj)
	if !containsStr(string(raw), "fullUrl") || !containsStr(string(raw), "eventCount") {
		t.Fatalf("请求的字段缺失：%s", raw)
	}
	if containsStr(string(raw), "SECRET-BODY") || containsStr(string(raw), "responseBody") {
		t.Fatalf("摘要里出现了正文：%s", raw)
	}

	// 拼错的字段必须报错（而不是静默少给数据）。
	if _, errObj := reg.Call("list_flows", json.RawMessage(`{"fields":["responseBody"]}`)); errObj == nil {
		t.Fatalf("请求正文字段应被拒绝")
	}
}

func TestMCPListCapabilitiesTool(t *testing.T) {
	app := NewApp()
	reg, _, err := app.buildMCPRegistry()
	if err != nil {
		t.Fatalf("buildMCPRegistry: %v", err)
	}
	res, errObj := reg.Call("list_capabilities", nil)
	if errObj != nil {
		t.Fatalf("list_capabilities 失败：%v", errObj)
	}
	raw, _ := json.Marshal(res)
	var out struct {
		Groups map[string][]string `json:"groups"`
		Tiers  map[string][]string `json:"tiers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 分组里要有 flows / proxy / ssl 等；list_flows 自己也要出现。
	if len(out.Groups["flows"]) == 0 || len(out.Groups["proxy"]) == 0 {
		t.Fatalf("分组缺失：%v", out.Groups)
	}
	found := false
	for _, n := range out.Groups["flows"] {
		if n == "list_flows" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list_flows 应出现在 flows 分组：%v", out.Groups["flows"])
	}
	// 三档都要有内容（read 一定有；write/dangerous 也应存在，只是默认不允许调用）。
	for _, tier := range []string{"read", "write", "dangerous"} {
		if len(out.Tiers[tier]) == 0 {
			t.Errorf("档位 %s 不应为空", tier)
		}
	}
}

// 撤下 get_flows_raw 之后，GetFlows 必须登记在排除名单里（漂移守卫会查），
// 且工具列表里不能再出现那个"上下文炸弹"。
func TestMCPRawFlowsToolIsGone(t *testing.T) {
	app := NewApp()
	reg, _, err := app.buildMCPRegistry()
	if err != nil {
		t.Fatalf("buildMCPRegistry: %v", err)
	}
	for _, tool := range reg.Tools() {
		if tool.Name == "get_flows_raw" {
			t.Fatalf("get_flows_raw 已被 list_flows 取代，不应再暴露")
		}
	}
	if _, errObj := reg.Call("get_flows_raw", nil); errObj == nil {
		t.Fatalf("调用已撤下的工具应当报错")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = mcp.TierRead
