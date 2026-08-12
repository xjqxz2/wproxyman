// App.tsx — 应用根组件
// 职责：编排整个抓包工具的界面布局，并在启动时完成
// 后端（Go/Wails）状态的初始化拉取与事件订阅。
// 交互模块：
//   - store：useFlows（流量数据）、useApp（代理/SSL/工具/设置等应用状态）
//   - services/api：api（后端 RPC）、emit（主动通知后端）、Events/onEvent（订阅后端推送事件）
//   - components/*：Toolbar（工具栏）、SourceList（来源侧栏）、FlowTable（流量列表）、
//     ToolsPanel（工具面板）、DetailPanel（详情面板）、SettingsPanel（设置弹窗）、StatusBar（状态栏）
// 布局：上下为 Toolbar/StatusBar，中间为 SourceList + 主面板（流量表或工具面板）+ 详情面板。
import { useEffect, useState } from 'react';
import { useFlows, useApp } from './store';
import { api, emit, Events, onEvent } from './services/api';
import type { Flow, ProxyStatus } from './types';
import Toolbar from './components/Toolbar';
import SourceList from './components/SourceList';
import FlowTable from './components/FlowTable';
import ToolsPanel from './components/ToolsPanel';
import DetailPanel from './components/DetailPanel';
import SettingsPanel from './components/SettingsPanel';
import StatusBar from './components/StatusBar';
import './styles/theme.css';

// App — 根组件
// 返回值：完整的应用界面结构（无 props、无对外暴露的状态）。
export default function App() {
  // 从全局 store 中取出流量操作与各类应用状态 setter。
  const { setFlows, upsertFlow, removeFlow, clear } = useFlows();
  const { setProxy, setSSL, setCertInstalled, setToolConfig, setSettings, addPendingBreakpoint, removePendingBreakpoint, activeView, settingsOpen, setSettingsOpen } = useApp();
  // ready — 标记初始数据是否已加载完成（目前用于保证事件订阅前数据就绪）。
  const [ready, setReady] = useState(false);

  // 仅挂载时执行一次：初始化数据 + 订阅后端事件。
  useEffect(() => {
    // 并发拉取初始数据（流量列表、代理状态、端口、系统代理、SSL、工具配置、设置、证书状态）。
    const init = async () => {
      try {
        const [flows, proxyRunning, port, sysProxy, sslState, toolCfg, settings, certInstalled] = await Promise.all([
          api.getFlows(),
          api.isProxyRunning(),
          api.getProxyPort(),
          api.getSystemProxyEnabled(),
          api.getSSLProxyState(),
          api.getToolConfig(),
          api.getSettings(),
          api.isCertificateInstalled(),
        ]);
        setFlows(flows);
        setProxy({ running: proxyRunning, port, systemProxy: sysProxy } as ProxyStatus);
        setSSL(sslState.default as boolean, (sslState.hosts as Record<string, boolean>) ?? {});
        if (toolCfg) setToolConfig(toolCfg);
        setSettings(settings);
        setCertInstalled(certInstalled);
      } catch (e) {
        // 初始化失败只记录日志，界面仍可渲染（后续事件推送会补齐数据）。
        console.error('init failed', e);
      }
      setReady(true);
      // 通知后端 UI 已渲染完成，以便其显示窗口
      //（避免启动时的黑屏闪烁）。
      emit('ui:ready');
    };
    init();

    // 订阅后端推送事件，实时同步流量/代理/证书变化到 store。
    // 返回的 unsubs 数组用于在组件卸载时逐一取消订阅。
    const unsubs = [
      onEvent<Flow>(Events.FlowNew, (f) => upsertFlow(f)),
      onEvent<Flow>(Events.FlowUpdated, (f) => upsertFlow(f)),
      onEvent<Flow>(Events.FlowCompleted, (f) => upsertFlow(f)),
      // 流量被暂停时：更新流量数据，并记录为待处理断点（暂停在断点上）。
      onEvent<Flow>(Events.FlowPaused, (f) => {
        upsertFlow(f);
        addPendingBreakpoint(f.id);
      }),
      // 流量恢复时：移除对应的待处理断点标记。
      onEvent<string>(Events.FlowResumed, (id) => removePendingBreakpoint(id)),
      // 流量被删除：从列表中移除。
      onEvent<string>(Events.FlowDeleted, (id) => removeFlow(id)),
      // 列表被清空。
      onEvent(Events.FlowsCleared, () => clear()),
      // 全量替换流量列表（如加载新会话/文件）。
      onEvent<Flow[]>(Events.FlowsReplaced, (flows) => setFlows(flows)),
      // 导入流量后，直接从 store 当前状态重新设置列表。
      onEvent<Flow[]>(Events.FlowsImported, () => setFlows(useFlows.getState().flows)),
      // 代理运行状态（端口/系统代理开关）变更。
      onEvent<ProxyStatus>(Events.ProxyStatus, (p) => setProxy(p)),
      // 证书安装状态变更。
      onEvent<{ installed: boolean }>(Events.CertStatus, (d) => setCertInstalled(d.installed)),
      // 后端"应用就绪"信号（当前无额外处理，仅占位保持订阅通道）。
      onEvent(Events.AppReady, () => {}),
    ];
    // 清理函数：卸载时取消全部事件订阅，避免内存泄漏。
    return () => unsubs.forEach((u) => u());
  }, []);

  // 界面布局：工具栏 → 主内容区（来源栏 + 流量表/工具面板 + 详情面板）→ 状态栏；
  // 设置弹窗开启时在最上层渲染 SettingsPanel。
  return (
    <div className="app">
      <Toolbar />
      <div className="app-body">
        <SourceList />
        {activeView === 'flows' ? <FlowTable /> : <ToolsPanel view={activeView} />}
        <DetailPanel />
      </div>
      <StatusBar />
      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} />}
    </div>
  );
}
