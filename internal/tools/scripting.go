// Package tools 脚本（Scripting）工具的实现。
// scripting.go 提供一个基于 JavaScript 的拦截脚本引擎，使用 goja（纯 Go 的 ECMAScript 5.1+ 运行时）。
//
// 架构：
//   - 用户编写 JS 脚本，包含 onRequest(context) 和 onResponse(context) 函数
//   - 脚本编译为 scriptRuntime，缓存 vm 和导出的钩子函数
//   - 请求/响应阶段调用 runScripts()，依次执行匹配的脚本
//   - 脚本通过 context.request/context.response 访问和修改请求/响应
//
// JS API（通过 context 暴露）：
//   - context.request.method / url / path / host / headers / body / hasBody
//   - context.request.setHeader(name, value) / removeHeader(name)
//   - context.request.setBody(str) / setBodyBase64(str) / clearBody()
//   - context.response 同理
//   - 内置工具：base64.encode/decode, hashes.md5/sha1/sha256, uuid()
//   - btoa() / atob() 兼容浏览器习惯
//
// 设计要点：
//   - 脚本错误不会中断代理，错误被记录到脚本日志中
//   - onRequest 返回 false 时可取消请求
//   - 头信息合并：同名请求头合并为 "val1, val2"
package tools

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dop251/goja"

	"wproxyman/internal/models"
)

// ScriptEntry 是用户编写的拦截脚本条目。
// 包含脚本代码、匹配规则和运行时日志。
type ScriptEntry struct {
	ID      string   `json:"id"`            // 脚本唯一标识
	Name    string   `json:"name"`          // 脚本可读名称
	Enabled bool     `json:"enabled"`       // 是否启用
	Match   URLMatch `json:"match"`         // URL 匹配条件
	Code    string   `json:"code"`          // JavaScript 脚本代码
	Log     []string `json:"log,omitempty"` // 最近的控制台输出日志（供脚本编辑器显示）
}

// ScriptingConfig 持有所有脚本和启用标志。
type ScriptingConfig struct {
	Enabled bool          `json:"enabled"` // 是否启用脚本工具
	Scripts []ScriptEntry `json:"scripts"` // 脚本列表
}

// scriptRuntime 是已编译的脚本运行时，包含 goja 虚拟机和导出的钩子函数。
type scriptRuntime struct {
	entry      *ScriptEntry   // 原始脚本条目（用于日志写入）
	vm         *goja.Runtime  // goja JavaScript 运行时
	onRequest  goja.Callable  // 编译后的 onRequest 函数（可能为 nil）
	onResponse goja.Callable  // 编译后的 onResponse 函数（可能为 nil）
}

// scriptEngine 持有所有已编译脚本的运行时列表。
type scriptEngine struct {
	runtimes []*scriptRuntime
}

// rebuildScriptEngine 根据当前脚本配置重新编译所有启用的脚本。
// 在 e.mu 写锁保护下调用（SetConfig 中）。
// 编译失败的脚本会被跳过，错误写入脚本日志。
func (e *Engine) rebuildScriptEngine() {
	e.scriptEngine = nil
	if !e.Scripts.Enabled {
		return
	}
	se := &scriptEngine{}
	for i := range e.Scripts.Scripts {
		entry := &e.Scripts.Scripts[i]
		if !entry.Enabled {
			continue
		}
		rt, err := compileScript(entry)
		if err != nil {
			// 编译失败：记录错误到脚本日志，跳过此脚本
			entry.Log = append(entry.Log, fmt.Sprintf("[%s] %v", time.Now().Format("15:04:05"), err))
			continue
		}
		se.runtimes = append(se.runtimes, rt)
	}
	e.scriptEngine = se
}

// compileScript 解析 JS 脚本并提取 onRequest / onResponse 钩子函数。
// 使用 goja 的 TagFieldNameMapper 让 JS 对象的属性名与 Go 的 json tag 对应。
// 至少需要一个钩子函数，否则视为无效脚本。
func compileScript(entry *ScriptEntry) (*scriptRuntime, error) {
	vm := goja.New()
	// 设置字段名映射器：Go 的 json tag 自动映射到 JS 属性名（如 "url" 而非 "URL"）
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	// 注入内置工具函数（base64、hashes、uuid 等）
	injectAddons(vm)

	if _, err := vm.RunString(entry.Code); err != nil {
		return nil, fmt.Errorf("script compile error: %w", err)
	}
	rt := &scriptRuntime{entry: entry, vm: vm}
	// 提取 onRequest 钩子（如果定义了的话）
	if v := vm.Get("onRequest"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		if fn, ok := goja.AssertFunction(v); ok {
			rt.onRequest = fn
		}
	}
	// 提取 onResponse 钩子（如果定义了的话）
	if v := vm.Get("onResponse"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		if fn, ok := goja.AssertFunction(v); ok {
			rt.onResponse = fn
		}
	}
	// 至少需要有一个钩子函数
	if rt.onRequest == nil && rt.onResponse == nil {
		return nil, fmt.Errorf("script defines neither onRequest nor onResponse")
	}
	return rt, nil
}

// scriptFlow 是暴露给 JS 脚本调用的可变状态容器。
// 包含请求和响应两个消息对象，脚本通过它们访问和修改数据。
type scriptFlow struct {
	request  *scriptMessage
	response *scriptMessage
}

// scriptMessage 表示一个 HTTP 消息（请求或响应），暴露给 JS 脚本。
// 字段名通过 json tag 映射到 JS 属性名。
type scriptMessage struct {
	Method     string            `json:"method"`     // HTTP 方法
	URL        string            `json:"url"`        // 完整 URL
	Path       string            `json:"path"`       // URL 路径
	Host       string            `json:"host"`       // 主机名
	Query      string            `json:"query"`      // 查询字符串
	HTTPVer    string            `json:"httpVersion"` // HTTP 协议版本
	Status     int               `json:"status"`     // 响应状态码
	StatusText string            `json:"statusText"` // 响应状态文本
	Headers    map[string]string `json:"headers"`    // HTTP 头
	body       []byte            // 消息体（不导出到 JS，通过 body 属性字符串化访问）
	bodyIsSet  bool              // 标记 body 是否被脚本显式修改
}

// newScriptMessageFromFlow 从 Flow 构建脚本消息对象。
// request=true 构建请求消息，false 构建响应消息。
func newScriptMessageFromFlow(f *models.Flow, request bool) *scriptMessage {
	if request {
		m := &scriptMessage{
			Method:  f.Method,
			URL:     f.FullURL,
			Path:    f.Path,
			Host:    f.Host,
			Query:   f.Query,
			HTTPVer: f.HTTPVersion,
			Headers: headersToMap(f.RequestHeaders),
			body:    f.RequestBody,
		}
		return m
	}
	return &scriptMessage{
		Status:     f.ResponseStatus,
		StatusText: f.ResponseReason,
		HTTPVer:    f.HTTPVersion,
		Headers:    headersToMap(f.ResponseHeaders),
		body:       f.ResponseBody,
	}
}

// --- 暴露给 JS 的方法 ---

// SetHeader 添加或替换 HTTP 头（JS 可调用）。
func (m *scriptMessage) SetHeader(name, value string) {
	m.Headers[name] = value
}

// RemoveHeader 删除 HTTP 头（JS 可调用）。
func (m *scriptMessage) RemoveHeader(name string) {
	delete(m.Headers, name)
}

// SetBody 设置消息体为指定字符串（JS 可调用）。
func (m *scriptMessage) SetBody(s string) {
	m.body = []byte(s)
	m.bodyIsSet = true
}

// SetBodyBase64 将 Base64 编码的字符串解码后设置消息体（JS 可调用）。
// 解码失败时不做任何操作。
func (m *scriptMessage) SetBodyBase64(s string) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		m.body = b
		m.bodyIsSet = true
	}
}

// ClearBody 清空消息体（JS 可调用）。
func (m *scriptMessage) ClearBody() {
	m.body = nil
	m.bodyIsSet = true
}

// run 对 Flow 执行单个脚本的一个阶段。
// phase 为 "request" 或 "response"。
// 返回值：(是否继续处理, 错误)
// onRequest 返回 false 表示取消请求。
func (rt *scriptRuntime) run(f *models.Flow, phase string) (bool, error) {
	ctx := &scriptFlow{}
	var fn goja.Callable
	if phase == "request" {
		if rt.onRequest == nil {
			return true, nil // 无 onRequest 钩子 → 继续
		}
		fn = rt.onRequest
		ctx.request = newScriptMessageFromFlow(f, true)
	} else {
		if rt.onResponse == nil {
			return true, nil // 无 onResponse 钩子 → 继续
		}
		fn = rt.onResponse
		ctx.response = newScriptMessageFromFlow(f, false)
	}
	// 构建 JS context 对象（包含 request/response 子对象）
	ctxObj := rt.vm.ToValue(buildJsContext(rt.vm, ctx))
	// 调用 JS 钩子函数
	res, err := fn(goja.Undefined(), ctxObj)
	if err != nil {
		return true, err
	}
	// 将 JS 修改同步回 Flow。
	// JS 中对 context.request.body 的直接赋值会被读取回传；
	// setHeader/setBody 等方法直接修改 Go 结构体。
	if ctx.request != nil {
		// 从 JS 对象回读 body 属性（支持直接赋值 context.request.body = "xxx"）
		if jsReq := ctxObj.ToObject(rt.vm).Get("request"); jsReq != nil && !goja.IsUndefined(jsReq) && !goja.IsNull(jsReq) {
			if b := jsReq.ToObject(rt.vm).Get("body"); b != nil && !goja.IsUndefined(b) {
				ctx.request.body = []byte(b.String())
				ctx.request.bodyIsSet = true
			}
		}
		f.RequestHeaders = mapToHeaders(ctx.request.Headers)
		if ctx.request.bodyIsSet {
			f.RequestBody = ctx.request.body
		}
	}
	if ctx.response != nil {
		// 从 JS 对象回读 body 属性
		if jsRes := ctxObj.ToObject(rt.vm).Get("response"); jsRes != nil && !goja.IsUndefined(jsRes) && !goja.IsNull(jsRes) {
			if b := jsRes.ToObject(rt.vm).Get("body"); b != nil && !goja.IsUndefined(b) {
				ctx.response.body = []byte(b.String())
				ctx.response.bodyIsSet = true
			}
		}
		f.ResponseHeaders = mapToHeaders(ctx.response.Headers)
		if ctx.response.bodyIsSet {
			f.ResponseBody = ctx.response.body
		}
	}
	// onRequest 返回 false → 取消请求
	if phase == "request" && !goja.IsUndefined(res) && !goja.IsNull(res) {
		if b, ok := res.Export().(bool); ok && !b {
			return false, nil
		}
	}
	return true, nil
}

// buildJsContext 为 JS 脚本创建 context 对象，包含 request 和/或 response 子对象。
func buildJsContext(vm *goja.Runtime, ctx *scriptFlow) map[string]interface{} {
	obj := map[string]interface{}{}
	if ctx.request != nil {
		obj["request"] = jsMessage(vm, ctx.request, true)
	}
	if ctx.response != nil {
		obj["response"] = jsMessage(vm, ctx.response, false)
	}
	return obj
}

// jsMessage 将 Go 的 scriptMessage 转换为暴露给 JS 的 map。
// 包含数据字段和操作方法（setHeader、setBody 等），
// 这些方法通过闭包调用 Go 端的 scriptMessage 方法。
func jsMessage(vm *goja.Runtime, m *scriptMessage, isRequest bool) map[string]interface{} {
	msg := map[string]interface{}{
		"method":      m.Method,
		"url":         m.URL,
		"path":        m.Path,
		"host":        m.Host,
		"query":       m.Query,
		"httpVersion": m.HTTPVer,
		"status":      m.Status,
		"statusText":  m.StatusText,
		"headers":     m.Headers,
		"body":        string(m.body),
		"bodyBase64":  base64.StdEncoding.EncodeToString(m.body),
		"hasBody":     len(m.body) > 0,
	}
	// 辅助函数：将 Go 函数包装为 goja 可调用值
	setFn := func(name string, fn func(goja.FunctionCall) goja.Value) {
		msg[name] = vm.ToValue(fn)
	}
	_ = isRequest
	// 注入操作方法（JS 端调用 → Go 端执行）
	setFn("setHeader", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 2 {
			m.SetHeader(call.Arguments[0].String(), call.Arguments[1].String())
		}
		return goja.Undefined()
	})
	setFn("removeHeader", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 1 {
			m.RemoveHeader(call.Arguments[0].String())
		}
		return goja.Undefined()
	})
	setFn("setBody", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 1 {
			m.SetBody(call.Arguments[0].String())
		}
		return goja.Undefined()
	})
	setFn("setBodyBase64", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 1 {
			m.SetBodyBase64(call.Arguments[0].String())
		}
		return goja.Undefined()
	})
	setFn("clearBody", func(call goja.FunctionCall) goja.Value {
		m.ClearBody()
		return goja.Undefined()
	})
	return msg
}

// runScripts 对指定阶段执行所有匹配的脚本。
// 返回值：(是否继续, 错误)
// 每个脚本的错误被记录到其日志中，不会中断管道。
func (e *Engine) runScripts(f *models.Flow, phase string) (bool, error) {
	e.mu.RLock()
	se := e.scriptEngine
	e.mu.RUnlock()
	if se == nil {
		return true, nil
	}
	for _, rt := range se.runtimes {
		// 检查 URL 匹配
		if !rt.entry.Match.Matches(f) {
			continue
		}
		ok, err := rt.run(f, phase)
		if err != nil {
			// 脚本错误：记录到日志，继续处理下一个脚本
			e.mu.Lock()
			rt.entry.Log = append(rt.entry.Log, fmt.Sprintf("[%s] %v", time.Now().Format("15:04:05"), err))
			e.mu.Unlock()
			continue
		}
		if !ok {
			return false, nil // onRequest 返回 false → 取消请求
		}
	}
	return true, nil
}

// headersToMap 将 models.Header 切片转换为 map，同名头的值合并为 "val1, val2"。
func headersToMap(hs []models.Header) map[string]string {
	m := make(map[string]string, len(hs))
	for _, h := range hs {
		if _, exists := m[h.Name]; exists {
			// 同名头合并
			m[h.Name] = m[h.Name] + ", " + h.Value
			continue
		}
		m[h.Name] = h.Value
	}
	return m
}

// mapToHeaders 将 map 转换回 models.Header 切片。
func mapToHeaders(m map[string]string) []models.Header {
	out := make([]models.Header, 0, len(m))
	for k, v := range m {
		out = append(out, models.Header{Name: k, Value: v})
	}
	return out
}

// injectAddons 向 goja 运行时注入内置工具函数。
// 包括：base64 编解码、哈希函数（MD5/SHA1/SHA256）、UUID 生成、
// 以及浏览器兼容的 btoa/atob 函数。
func injectAddons(vm *goja.Runtime) {
	// base64 工具
	b64 := map[string]interface{}{
		"encode": func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"decode": func(s string) string {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return ""
			}
			return string(b)
		},
	}
	// 哈希工具
	hashes := map[string]interface{}{
		"md5":    func(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) },
		"sha1":   func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) },
		"sha256": func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) },
	}
	// UUID v4 生成器
	uuid := func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		b[6] = (b[6] & 0x0f) | 0x40 // UUID 版本 4
		b[8] = (b[8] & 0x3f) | 0x80 // UUID 变体 1
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	// 全局函数（浏览器兼容）
	_ = vm.Set("btoa", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) })
	_ = vm.Set("atob", func(s string) string {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return ""
		}
		return string(b)
	})
	// 命名空间对象
	_ = vm.Set("base64", b64)
	_ = vm.Set("hashes", hashes)
	_ = vm.Set("uuid", uuid)
	// 统一入口（proxymanAddons）
	_ = vm.Set("proxymanAddons", map[string]interface{}{
		"base64": b64, "hashes": hashes, "uuid": uuid,
	})
}
