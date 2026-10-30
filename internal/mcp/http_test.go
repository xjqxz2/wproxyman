package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "tok-abcdefghijklmnop"

func newHTTPTest(t *testing.T, perms Permissions) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHTTPHandler(newTestService(perms), HTTPConfig{Token: testToken}))
	t.Cleanup(srv.Close)
	return srv
}

// post 发一条请求；token 为空表示不带鉴权头。
func post(t *testing.T, srv *httptest.Server, token, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestHTTPAuth(t *testing.T) {
	srv := newHTTPTest(t, Permissions{})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	// 没有令牌。
	resp, raw := post(t, srv, "", body, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401，实际 %d (%s)", resp.StatusCode, raw)
	}
	if !strings.Contains(raw, "令牌") {
		t.Fatalf("错误信息应说明是令牌问题：%s", raw)
	}

	// 错误令牌。
	resp, _ = post(t, srv, "wrong-token", body, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误令牌应 401，实际 %d", resp.StatusCode)
	}

	// 支持 X-WProxyman-Token 头（有的客户端只方便配自定义头）。
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("X-WProxyman-Token", testToken)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("自定义令牌头应通过，实际 %d", r2.StatusCode)
	}
}

func TestHTTPToolsListWithProtocolHeader(t *testing.T) {
	srv := newHTTPTest(t, Permissions{})
	// 只在 HTTP 头里声明版本（不在 _meta 里）——传输层应补进去，而不是报"缺版本"。
	resp, raw := post(t, srv, testToken,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		map[string]string{"MCP-Protocol-Version": ModernVersion})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d (%s)", resp.StatusCode, raw)
	}
	var out struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("响应解析失败：%v (%s)", err, raw)
	}
	if out.Error != nil {
		t.Fatalf("不应有错误：%+v", out.Error)
	}
	if len(out.Result.Tools) == 0 {
		t.Fatalf("应返回工具列表：%s", raw)
	}
}

func TestHTTPUnsupportedVersion(t *testing.T) {
	srv := newHTTPTest(t, Permissions{})
	resp, raw := post(t, srv, testToken,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		map[string]string{"MCP-Protocol-Version": "1900-01-01"})
	// JSON-RPC 层错误仍是 200（传输成功），错误在 body 里。
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200（错误在 body 里），实际 %d", resp.StatusCode)
	}
	if !strings.Contains(raw, "-32022") {
		t.Fatalf("应回 -32022：%s", raw)
	}
	if !strings.Contains(raw, "2026-07-28") {
		t.Fatalf("应列出支持的版本：%s", raw)
	}
}

func TestHTTPToolsCallAndNotification(t *testing.T) {
	srv := newHTTPTest(t, Permissions{})
	// 只读工具：带 _meta 的现代请求。
	resp, raw := post(t, srv, testToken,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"get_flows_raw","arguments":{}}}`,
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d (%s)", resp.StatusCode, raw)
	}
	if !strings.Contains(raw, `"content"`) {
		t.Fatalf("应返回 content 块：%s", raw)
	}

	// 写档未开：错误里应指引去设置里开启。
	_, raw = post(t, srv, testToken,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"_meta":{"protocolVersion":"`+ModernVersion+`"},"name":"set_port","arguments":{"port":1}}}`,
		nil)
	if !strings.Contains(raw, "设置 → MCP") {
		t.Fatalf("应指引去设置开启档位：%s", raw)
	}

	// 通知：无 id → 202 且没有响应体。
	resp, raw = post(t, srv, testToken, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("通知应回 202，实际 %d (%s)", resp.StatusCode, raw)
	}
	if strings.TrimSpace(raw) != "" {
		t.Fatalf("通知不应有响应体：%s", raw)
	}
}

func TestHTTPMethodAndSizeLimits(t *testing.T) {
	srv := newHTTPTest(t, Permissions{})

	// GET：本服务不提供 SSE 流，应明确 405。
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405，实际 %d", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "POST" {
		t.Fatalf("应告知只支持 POST，实际 Allow=%q", allow)
	}

	// 超大请求体：应在读完之前就被拒（用 limit reader，不会把内存吃满）。
	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"pad":"` +
		strings.Repeat("a", defaultMaxBytes+10) + `"}}}`
	resp, raw := post(t, srv, testToken, huge, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求应 413，实际 %d (%s)", resp.StatusCode, raw)
	}
}

func TestHTTPEmptyTokenRejectsEverything(t *testing.T) {
	// 令牌为空（未初始化）时不能变成"无鉴权模式"。
	srv := httptest.NewServer(NewHTTPHandler(newTestService(Permissions{}), HTTPConfig{}))
	defer srv.Close()
	resp, _ := post(t, srv, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("空令牌配置下必须拒绝一切请求，实际 %d", resp.StatusCode)
	}
}
