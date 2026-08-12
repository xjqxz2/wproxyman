// Package tools 断点调试（Breakpoint）工具的实现。
// breakpoint.go 实现在请求或响应阶段暂停匹配的 Flow，允许用户检查和修改内容。
//
// 工作流程：
//   1. 请求到达 → OnRequest 检查断点规则
//   2. 命中断点 → pauseFlow()：创建 channel，标记 Flow 为等待状态，代理挂起
//   3. 前端显示暂停的 Flow，用户可以修改请求/响应
//   4. 用户点击恢复 → ResolveBreakpoint() → resumeFlow()
//   5. resumeFlow()：将修改后的内容复制回原 Flow 指针，关闭 channel 唤醒代理
//   6. 代理继续执行剩余管道
//
// 技术要点：
//   - 使用 Go channel 阻塞代理 goroutine，直到用户恢复
//   - 断点支持 request 和 response 两种阶段
//   - 修改后的 Flow 通过指针赋值写回原对象（代理持有原指针引用）
package tools

import (
	"fmt"

	"wproxyman/internal/models"
	"wproxyman/internal/proxy"
)

// BreakpointRule 定义了断点调试规则——匹配的请求会在指定阶段暂停。
type BreakpointRule struct {
	Rule
	// Phases 指定断点阶段："request"（请求阶段暂停）和/或 "response"（响应阶段暂停）。
	Phases []string `json:"phases"`
}

// BreakpointConfig 持有所有断点规则和启用标志。
type BreakpointConfig struct {
	Enabled bool             `json:"enabled"` // 是否启用断点工具
	Rules   []BreakpointRule `json:"rules"`   // 断点规则列表
}

// waitEntry 记录一个暂停的 Flow 及其暂停阶段。
// ch 是用于阻塞/唤醒代理 goroutine 的 channel。
type waitEntry struct {
	phase string           // 暂停阶段："request" 或 "response"
	ch    chan *models.Flow // 唤醒 channel（缓冲大小为 1）
	flow  *models.Flow     // 原始 Flow 指针（恢复时通过指针赋值写回修改内容）
}

// breakpointHitRequest 检查请求阶段是否应触发断点。
func (e *Engine) breakpointHitRequest(f *models.Flow) bool {
	return e.breakpointHit(f, "request")
}

// breakpointHitResponse 检查响应阶段是否应触发断点。
func (e *Engine) breakpointHitResponse(f *models.Flow) bool {
	return e.breakpointHit(f, "response")
}

// breakpointHit 检查指定阶段的断点规则是否匹配。
func (e *Engine) breakpointHit(f *models.Flow, phase string) bool {
	cfg := e.Breakpoints
	if !cfg.Enabled {
		return false
	}
	// 遍历所有断点规则
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if !r.Enabled || !r.Match.Matches(f) {
			continue
		}
		// 检查规则的阶段列表是否包含当前阶段
		for _, p := range r.Phases {
			if p == phase {
				return true
			}
		}
	}
	return false
}

// pauseFlow 为指定 Flow 注册一个等待 channel 并标记为暂停状态。
// 代理 goroutine 后续会通过此 channel 阻塞等待。
func (e *Engine) pauseFlow(f *models.Flow, phase string) {
	e.mu.Lock()
	e.waiters[f.ID] = &waitEntry{phase: phase, flow: f, ch: make(chan *models.Flow, 1)}
	f.WaitingForDecision = true
	e.mu.Unlock()
}

// resumeFlow 用用户修改后的 Flow 内容恢复暂停的流程。
// 修改后的 Flow 内容通过指针赋值写回原始 Flow 指针，
// 这样持有原指针引用的代理 goroutine 就能看到修改。
func (e *Engine) resumeFlow(flowID string, modified *models.Flow) error {
	e.mu.Lock()
	w, ok := e.waiters[flowID]
	if ok {
		delete(e.waiters, flowID) // 立即从 waiting map 中移除
	}
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("flow %s is not paused", flowID)
	}
	// 如果用户提供了修改后的 Flow，将内容复制回原始指针
	if modified != nil {
		modified.WaitingForDecision = false
		if w.flow != nil && modified != w.flow {
			*w.flow = *modified // 指针赋值：将修改后的内容复制到原 Flow
		}
		w.ch <- w.flow // 发送到 channel 唤醒代理
	} else {
		// 用户未修改：直接恢复原始 Flow
		f := w.flow
		if f == nil {
			f = &models.Flow{ID: flowID}
		}
		f.WaitingForDecision = false
		w.ch <- f
	}
	close(w.ch)
	return nil
}

// WaitForDecision 实现 proxy.Interceptor 接口的等待方法。
// 在 Flow 被恢复前阻塞当前 goroutine，恢复后重新执行剩余管道阶段。
// 代理在捕获到断点决策后会调用此方法。
func (e *Engine) WaitForDecision(flowID string) (*proxy.InterceptDecision, error) {
	e.mu.RLock()
	w, ok := e.waiters[flowID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("flow %s is not paused", flowID)
	}
	// 阻塞等待 channel 有数据（用户恢复时发送）
	f := <-w.ch
	// 根据暂停阶段继续执行剩余管道
	if w.phase == "request" {
		return e.continueRequest(f)
	}
	// 响应阶段 → 正常返回（管道已执行完毕）
	return &proxy.InterceptDecision{}, nil
}

// ResolveBreakpoint 由前端 UI 调用，恢复指定断点的 Flow。
// modified 是用户可能在断点编辑器中修改后的 Flow（nil 表示未修改）。
func (e *Engine) ResolveBreakpoint(flowID string, modified *models.Flow) error {
	return e.resumeFlow(flowID, modified)
}

// PausedFlows 返回当前所有暂停 Flow 的 ID 列表。
// 供前端获取当前挂起的断点列表。
func (e *Engine) PausedFlows() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.waiters))
	for id := range e.waiters {
		out = append(out, id)
	}
	return out
}
