package main

// 文件说明： 流量仓库操作：查询/清空/删除 Flow、置顶、按域名分组（前端数据接口）。

import (
	"sort"

	"wproxyman/internal/models"
)

// GetFlows returns all captured flows (newest last).
func (a *App) GetFlows() []*models.Flow {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*models.Flow, len(a.flows))
	copy(out, a.flows)
	return out
}

// GetFlow returns a single flow by ID.
func (a *App) GetFlow(id string) *models.Flow {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.flowIdx[id]
}

// ClearFlows removes all captured flows.
func (a *App) ClearFlows() {
	a.mu.Lock()
	a.flows = nil
	a.flowIdx = make(map[string]*models.Flow)
	a.mu.Unlock()
	a.emit("flows:cleared", nil)
}

// DeleteFlow removes a single flow.
func (a *App) DeleteFlow(id string) {
	a.mu.Lock()
	if _, ok := a.flowIdx[id]; ok {
		delete(a.flowIdx, id)
		for i, f := range a.flows {
			if f.ID == id {
				a.flows = append(a.flows[:i], a.flows[i+1:]...)
				break
			}
		}
	}
	a.mu.Unlock()
	a.emit("flow:deleted", id)
}

// SetFlowPinned toggles pinning for a flow.
func (a *App) SetFlowPinned(id string, pinned bool) {
	a.mu.Lock()
	if f, ok := a.flowIdx[id]; ok {
		f.IsPinned = pinned
	}
	a.mu.Unlock()
}

// GetFlowsByDomain groups flows by host for the Domains source-list view.
func (a *App) GetFlowsByDomain() map[string][]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string][]string)
	for _, f := range a.flows {
		host := f.Host
		if host == "" {
			host = "(unknown)"
		}
		out[host] = append(out[host], f.ID)
	}
	// sort for determinism
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// GetFlowCount returns the number of captured flows.
func (a *App) GetFlowCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.flows)
}
