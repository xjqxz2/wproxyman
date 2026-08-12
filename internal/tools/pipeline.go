// Package tools 拦截管道的请求/响应处理。
// pipeline.go 实现了 Engine 的 OnRequest 和 OnResponse 方法，
// 定义了工具管道的固定执行顺序。
//
// 请求阶段管道（OnRequest）：
//   AllowList → Breakpoints(request) → Scripting(onRequest) → BlockList →
//   MapLocal → MapRemote → Rules(request) → NoCaching(request)
//
// 响应阶段管道（OnResponse）：
//   Rules(response) → NoCaching(response) → Scripting(onResponse) → Breakpoints(response)
//
// 管道设计原则：
//   - 每个工具可以独立决定"放行"、"短路响应"、"修改放行"或"暂停等待"
//   - 一旦某个工具决定短路（返回 ShortCircuit），后续工具不再执行
//   - 断点暂停后，用户恢复时会重新进入 continueRequest 继续剩余管道
package tools

import (
	"wproxyman/internal/models"
	"wproxyman/internal/proxy"
)

// OnRequest 实现 proxy.Interceptor 接口的请求阶段。
// 工具按固定顺序执行，确保管道行为可预测。
// 返回值：
//   - nil → 正常转发请求到上游
//   - InterceptDecision{SkipCapture: true} → 跳过捕获（AllowList 不匹配时）
//   - InterceptDecision{Wait: true} → 暂停等待用户操作（断点命中时）
//   - InterceptDecision{ShortCircuit: true} → 短路响应（脚本取消/阻止列表/本地映射）
//   - InterceptDecision{UpstreamURL: ...} → 重定向到其他上游（远程映射）
func (e *Engine) OnRequest(f *models.Flow) (*proxy.InterceptDecision, error) {
	// 步骤 1：Allow List（允许列表）
	// 当启用且无规则匹配时，跳过该请求的捕获（不影响代理转发）
	if e.allowListBlocks(f) {
		return &proxy.InterceptDecision{SkipCapture: true}, nil
	}
	// 步骤 2：Breakpoints（断点调试）——请求阶段
	// 命中时暂停流程，等待用户手动恢复
	if e.breakpointHitRequest(f) {
		e.pauseFlow(f, "request")
		return &proxy.InterceptDecision{Wait: true}, nil
	}
	return e.continueRequest(f)
}

// continueRequest 运行断点后的剩余请求阶段工具。
// 此方法既在 OnRequest 中直接调用，也在请求断点被用户恢复时调用。
func (e *Engine) continueRequest(f *models.Flow) (*proxy.InterceptDecision, error) {
	// 步骤 3：Scripting（脚本）——onRequest 阶段
	// 如果 onRequest 返回 false，则取消请求并返回 404
	ok, _ := e.runScripts(f, "request")
	if !ok {
		f.ToolType = "Scripting"
		f.ResponseStatus = 404
		f.ResponseReason = "Not Found"
		f.ResponseHeaders = []models.Header{{Name: "Content-Type", Value: "text/plain"}}
		body := "Cancelled by Scripting tool"
		f.ResponseBody = []byte(body)
		f.ResponseSize = int64(len(body))
		return &proxy.InterceptDecision{ShortCircuit: true}, nil
	}
	// 步骤 4：Block List（阻止列表）
	// 匹配时短路响应 404
	if e.applyBlockList(f) {
		return &proxy.InterceptDecision{ShortCircuit: true}, nil
	}
	// 步骤 5：Map Local（本地映射）
	// 匹配时从本地文件或内联内容提供响应，短路
	if e.applyMapLocal(f) {
		return &proxy.InterceptDecision{ShortCircuit: true}, nil
	}
	// 步骤 6：Map Remote（远程映射）
	// 匹配时重写请求目标 URL
	if target, ok := e.applyMapRemote(f); ok {
		return &proxy.InterceptDecision{UpstreamURL: target}, nil
	}
	// 步骤 7：Rules（规则引擎）——请求阶段
	// 添加/删除/替换请求头和请求体
	e.applyRules(f, "request")
	// 步骤 8：No Caching（禁用缓存）——请求阶段
	// 移除条件请求头（If-None-Match 等），强制拉取最新内容
	e.applyNoCaching(f)
	return nil, nil // 正常转发
}

// OnResponse 实现 proxy.Interceptor 接口的响应阶段。
// 工具按固定顺序处理响应数据。
func (e *Engine) OnResponse(f *models.Flow) error {
	// 步骤 1：Rules（规则引擎）——响应阶段
	// 添加/删除/替换响应头和响应体
	e.applyRules(f, "response")
	// 步骤 2：No Caching（禁用缓存）——响应阶段
	// 移除缓存相关响应头（Cache-Control、ETag 等）
	e.applyNoCaching(f)
	// 步骤 3：Scripting（脚本）——onResponse 阶段
	// 执行用户脚本，可修改响应
	_, _ = e.runScripts(f, "response")
	// 步骤 4：Breakpoints（断点调试）——响应阶段
	// 命中时暂停流程，等待用户手动恢复
	if e.breakpointHitResponse(f) {
		e.pauseFlow(f, "response")
		f.WaitingForDecision = true
	}
	return nil
}
