package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"wproxyman/internal/models"
)

// 端到端：真正把 MCP 服务挂到回环地址，用 HTTP 客户端走完整链路。
// 单元测试覆盖协议细节，这里验证的是"装配起来能不能用"。
func TestMCPServerEndToEnd(t *testing.T) {
	app := NewApp()
	app.flows = []*models.Flow{
		{ID: "1", Method: "GET", Host: "api.example.com", Path: "/v1/users",
			FullURL: "https://api.example.com/v1/users", ResponseStatus: 200, StartedAt: 1,
			ResponseBody: []byte("BODY-SENTINEL")},
	}
	app.flowIdx[app.flows[0].ID] = app.flows[0]

	// 显式取一个空闲端口：不要依赖 settings 里持久化的 MCPPort，
	// 否则同一进程内多个用例会抢同一个端口（曾经偶发失败）。
	port := freePort(t)
	if err := app.SetMCPEnabled(true, port, false, false); err != nil {
		t.Fatalf("启用 MCP 失败：%v", err)
	}
	defer app.stopMCPServer()

	st := app.GetMCPStatus()
	if !st.Running || st.URL == "" {
		t.Fatalf("服务未运行：%+v", st)
	}
	if st.Token == "" {
		t.Fatalf("应自动生成令牌")
	}
	// 只绑回环：绝不能对局域网暴露。
	addr := strings.TrimPrefix(st.URL, "http://")
	host, _, _ := net.SplitHostPort(strings.TrimSuffix(addr, "/mcp"))
	if host != "127.0.0.1" {
		t.Fatalf("必须只绑回环，实际 %s", host)
	}
	if st.ToolCount == 0 {
		t.Fatalf("工具表为空")
	}

	do := func(token, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, st.URL, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求失败：%v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	// 无令牌 → 401。
	if code, _ := do("", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401，实际 %d", code)
	}

	// 正确令牌 → tools/list 里有 list_flows 与 get_flow_body。
	code, raw := do(st.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if code != http.StatusOK {
		t.Fatalf("tools/list 应 200，实际 %d (%s)", code, raw)
	}
	if !strings.Contains(raw, "list_flows") || !strings.Contains(raw, "get_flow_body") {
		t.Fatalf("工具列表缺内容：%s", raw)
	}

	// 只读工具可用；返回里不能出现正文。
	_, raw = do(st.Token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_flows","arguments":{"host":"api.example.com"}}}`)
	if strings.Contains(raw, "BODY-SENTINEL") {
		t.Fatalf("摘要里不应出现正文：%s", raw)
	}
	var callResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &callResp); err != nil {
		t.Fatalf("解析失败：%v (%s)", err, raw)
	}
	if callResp.Error != nil || len(callResp.Result.Content) == 0 {
		t.Fatalf("调用应成功：%s", raw)
	}
	if !strings.Contains(callResp.Result.Content[0].Text, "api.example.com") {
		t.Fatalf("结果里应有那条流量：%s", callResp.Result.Content[0].Text)
	}

	// 写档默认关闭 → 被拒。
	_, raw = do(st.Token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"start_proxy","arguments":{"port":0}}}`)
	if !strings.Contains(raw, "设置 → MCP") {
		t.Fatalf("写档未开应被拒并指引：%s", raw)
	}

	// 开写档后立即可用（权限每次调用现查）。
	if err := app.SetMCPEnabled(true, port, true, false); err != nil {
		t.Fatalf("开启写档失败：%v", err)
	}
	st2 := app.GetMCPStatus()
	if !st2.AllowWrite {
		t.Fatalf("写档应已开启：%+v", st2)
	}

	// 审计有记录。
	if len(st2.Audit) == 0 {
		t.Fatalf("审计应有记录")
	}
	// 关掉服务。
	if err := app.SetMCPEnabled(false, 0, false, false); err != nil {
		t.Fatalf("关闭 MCP 失败：%v", err)
	}
	if app.IsMCPRunning() {
		t.Fatalf("关闭后不应在监听")
	}
}

// 令牌轮换后旧令牌必须立即失效。
func TestMCPTokenRotation(t *testing.T) {
	app := NewApp()
	if err := app.SetMCPEnabled(true, freePort(t), false, false); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	defer app.stopMCPServer()
	old := app.GetMCPStatus().Token

	newTok, err := app.RotateMCPToken()
	if err != nil {
		t.Fatalf("轮换失败：%v", err)
	}
	if newTok == old || newTok == "" {
		t.Fatalf("轮换后应得到不同的非空令牌")
	}
	url := app.GetMCPStatus().URL

	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+old)
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧令牌应失效（401），实际 %d", resp.StatusCode)
	}
}
