package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// 桥接器不做协议逻辑，只转发；这里用真实的 HTTP 端点验证转发是通的。
func TestMCPBridgeForwardsRequests(t *testing.T) {
	app := NewApp()
	if err := app.SetMCPEnabled(true, freePort(t), false, false); err != nil {
		t.Fatalf("启用 MCP 失败：%v", err)
	}
	defer app.stopMCPServer()
	st := app.GetMCPStatus()
	cfg := bridgeConfig{URL: st.URL, Token: st.Token}

	// 两条请求 + 一条通知：响应应只有两行，通知不产生输出。
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_capabilities","arguments":{}}}`,
		"",
	}, "\n")

	var out bytes.Buffer
	if err := bridgeMCPStdio(strings.NewReader(in), &out, cfg); err != nil {
		t.Fatalf("桥接失败：%v", err)
	}

	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("应输出 2 行响应（通知不产生输出），实际 %d 行：%q", len(lines), out.String())
	}
	// 第一行：tools/list，id 必须是 1。
	var first struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("第一行不是合法 JSON-RPC：%v (%s)", err, lines[0])
	}
	if first.ID != 1 || first.Error != nil || len(first.Result.Tools) == 0 {
		t.Fatalf("第一行内容不对：%s", lines[0])
	}
	// 第二行：id 2，且工具调用有结果。
	if !strings.Contains(lines[1], `"id":2`) || !strings.Contains(lines[1], "content") {
		t.Fatalf("第二行内容不对：%s", lines[1])
	}
}

// 令牌错误时，桥要给出可操作的说明，而不是让客户端一直等。
func TestMCPBridgeBadToken(t *testing.T) {
	app := NewApp()
	if err := app.SetMCPEnabled(true, freePort(t), false, false); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	defer app.stopMCPServer()
	st := app.GetMCPStatus()

	var out bytes.Buffer
	if err := bridgeMCPStdio(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"),
		&out, bridgeConfig{URL: st.URL, Token: "wrong"}); err != nil {
		t.Fatalf("桥接失败：%v", err)
	}
	if !strings.Contains(out.String(), "令牌无效") {
		t.Fatalf("应提示令牌无效：%s", out.String())
	}
}

// 应用没在跑时也要回一条 JSON-RPC 错误，而不是静默无响应。
func TestMCPBridgeUnreachable(t *testing.T) {
	cfg := bridgeConfig{URL: "http://127.0.0.1:1/mcp", Token: "x"} // 1 号端口不会有服务
	var out bytes.Buffer
	if err := bridgeMCPStdio(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out, cfg); err != nil {
		t.Fatalf("桥接失败：%v", err)
	}
	if !strings.Contains(out.String(), "无法连接 WProxyman") {
		t.Fatalf("应提示无法连接：%s", out.String())
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
