package mcp

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// HTTP 传输层：MCP 的 Streamable HTTP 绑定（这里只实现 JSON 响应，
// 不实现服务端主动推送的 SSE —— 本服务的工具调用都是请求-响应式，够用）。
//
// 安全基线（这些工具能控制整个应用，必须守住）：
//   - 只应绑定回环地址（由上层决定，本层不感知）；
//   - 每个请求都要 Bearer token，比较用常数时间；
//   - 请求体有大小上限，避免一次调用打爆内存；
//   - 只接受 POST。

// HTTPConfig 配置 HTTP 传输层。
type HTTPConfig struct {
	Token string // 访问令牌；空串表示拒绝所有请求（不提供"无鉴权"模式）
	// MaxBytes 限制请求体大小，默认 1 MiB（工具参数不该有这么大）。
	MaxBytes int64
}

const defaultMaxBytes = 1 << 20

// NewHTTPHandler 返回 MCP 的 HTTP 处理器。
func NewHTTPHandler(svc *Service, cfg HTTPConfig) http.Handler {
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// Streamable HTTP 里 GET 用于建立 SSE 流；本服务不支持，明确告知。
			w.Header().Set("Allow", "POST")
			writeJSONError(w, http.StatusMethodNotAllowed,
				Errorf(CodeInvalidRequest, "只支持 POST（本服务不提供 SSE 推送流）"))
			return
		}
		if !tokenOK(r, cfg.Token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="wproxyman"`)
			writeJSONError(w, http.StatusUnauthorized,
				Errorf(CodeInvalidRequest, "缺少或错误的访问令牌：请在 WProxyman 设置 → MCP 中复制令牌"))
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, Errorf(CodeParseError, "读取请求体失败：%v", err))
			return
		}
		if int64(len(body)) > maxBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge,
				Errorf(CodeInvalidParams, "请求体过大（上限 %d 字节）", maxBytes))
			return
		}

		// 规范要求 HTTP 上同时用 MCP-Protocol-Version 头携带版本。
		// 若头里有而 _meta 里没有，就补进去，免得客户端因为"两处只写一处"被拒。
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" {
			body = injectVersion(body, v)
		}

		resp := svc.Handle(body)
		if resp == nil {
			// 通知：按规范回 202 且没有响应体。
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

// tokenOK 用常数时间比较令牌，避免时序侧信道。
// 令牌为空时一律拒绝：绝不提供"无鉴权"后门。
func tokenOK(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got := ""
	if h := r.Header.Get("Authorization"); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			got = strings.TrimSpace(h[7:])
		}
	}
	if got == "" {
		got = strings.TrimSpace(r.Header.Get("X-WProxyman-Token"))
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// injectVersion 把协议头里的版本补进 params._meta.protocolVersion（已有则不覆盖）。
// 解析失败时原样返回，交给服务层按正常流程报错。
func injectVersion(body []byte, version string) []byte {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return body
	}
	var params map[string]json.RawMessage
	if raw, ok := msg["params"]; ok {
		if err := json.Unmarshal(raw, &params); err != nil {
			params = map[string]json.RawMessage{}
		}
	} else {
		params = map[string]json.RawMessage{}
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			meta = map[string]json.RawMessage{}
		}
	}
	if _, has := meta["protocolVersion"]; !has {
		v, _ := json.Marshal(version)
		meta["protocolVersion"] = v
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return body
	}
	params["_meta"] = metaRaw
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return body
	}
	msg["params"] = paramsRaw
	out, err := json.Marshal(msg)
	if err != nil {
		return body
	}
	return out
}

func writeJSONError(w http.ResponseWriter, status int, errObj *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse(nil, errObj))
}

var _ = bytes.MinRead
