// mcp_bridge.go —— `wproxyman mcp` 子命令：把 stdio 上的 JSON-RPC 转发到
// 正在运行的 WProxyman 内置 MCP 端点。
//
// 为什么需要它：Claude Desktop 这类客户端只会**启动一个子进程**并用 stdio 通信，
// 不能直接填 HTTP URL。桥本身不实现任何协议逻辑，只是一个转发器——
// 权限、审计、工具表都在应用进程里，客户端连的还是同一份能力。
//
// 协议细节：stdio 传输按行分隔 JSON-RPC 消息（每行一条）。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wproxyman/internal/mcp"
)

// bridgeConfig 是桥接目标。
type bridgeConfig struct {
	URL   string
	Token string
}

// bridgeFromSettings 从持久化设置里读出 MCP 端点的地址与令牌。
func bridgeFromSettings() (bridgeConfig, error) {
	s := loadSettings()
	if !s.MCPEnabled {
		return bridgeConfig{}, fmt.Errorf("MCP 未启用：请在 WProxyman 的 设置 → MCP 中打开")
	}
	port := s.MCPPort
	if port == 0 {
		port = DefaultMCPPort
	}
	if s.MCPToken == "" {
		return bridgeConfig{}, fmt.Errorf("MCP 令牌为空：请在 设置 → MCP 中重新生成")
	}
	return bridgeConfig{
		URL:   fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
		Token: s.MCPToken,
	}, nil
}

// bridgeMCPStdio 逐行读取 stdin 上的 JSON-RPC 消息，转发给 HTTP 端点，
// 并把响应逐行写回 stdout。返回的 error 表示桥本身无法工作（例如应用没在跑）。
func bridgeMCPStdio(in io.Reader, out io.Writer, cfg bridgeConfig) error {
	client := &http.Client{Timeout: 60 * time.Second}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20) // 单条消息上限 8 MiB
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		respBody, status, err := postJSON(client, cfg, line)
		if err != nil {
			// 连接层失败：用 JSON-RPC 错误回应，让客户端知道发生了什么
			// （而不是静默卡住）。写不出响应就结束。
			if werr := writeLine(out, errorLine(nil, mcp.CodeInternalError,
				fmt.Sprintf("无法连接 WProxyman（%s）：请确认应用正在运行且 MCP 已启用：%v", cfg.URL, err))); werr != nil {
				return werr
			}
			continue
		}
		if status == http.StatusAccepted || len(bytes.TrimSpace(respBody)) == 0 {
			// 通知：按规范没有响应。
			continue
		}
		if status == http.StatusUnauthorized {
			if werr := writeLine(out, errorLine(nil, mcp.CodeInvalidRequest,
				"MCP 令牌无效：请在 WProxyman 的 设置 → MCP 中重新复制令牌")); werr != nil {
				return werr
			}
			continue
		}
		if werr := writeLine(out, bytes.TrimSpace(respBody)); werr != nil {
			return werr
		}
	}
	return scanner.Err()
}

func postJSON(client *http.Client, cfg bridgeConfig, body []byte) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	// 让服务端按现代语义处理（stdio 桥不做握手）。
	req.Header.Set("MCP-Protocol-Version", mcp.ModernVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return raw, resp.StatusCode, err
}

// errorLine 构造一条 JSON-RPC 错误响应。
func errorLine(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(mcp.ErrorResponse(id, &mcp.Error{Code: code, Message: msg}))
	return b
}

// writeLine 写一行并以换行结尾（stdio 传输按行分隔）。
func writeLine(w io.Writer, b []byte) error {
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// runMCPBridge 是 `wproxyman mcp` 的入口。
func runMCPBridge() {
	cfg, err := bridgeFromSettings()
	if err != nil {
		// 这是"给用户看的"错误：写到 stderr，同时用一条 JSON-RPC 错误回给客户端，
		// 免得客户端只显示"服务器无响应"。
		println("WProxyman MCP 桥:", err.Error())
		_ = writeLine(stdout(), errorLine(nil, mcp.CodeInternalError, err.Error()))
		return
	}
	if err := bridgeMCPStdio(stdin(), stdout(), cfg); err != nil {
		println("WProxyman MCP 桥已结束:", err.Error())
	}
}

var _ = strings.TrimSpace
