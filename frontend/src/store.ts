// store.ts — 全局状态管理（Zustand）
// 职责：维护两大状态切片：
//   1. useFlows — 流量（Flow）列表及其筛选/选中/搜索状态；
//   2. useApp — 应用级状态（代理、SSL、证书、工具配置、设置、待处理断点、当前视图等）。
// 交互模块：App.tsx 负责初始化与事件推送写入；各组件通过 hook 读取/更新状态；
// filterFlows 供列表类组件做统一的筛选计算。
import { create } from 'zustand';
import type { EngineConfig, Flow, ProxyStatus, Settings } from './types';

// FlowsState — 流量切片的状态接口。
// 字段：flows（全部流量）、selectedId（当前选中流量 id）、searchQuery（搜索关键字）、
// sourceSelection（来源筛选：'all' | 'domains' | 域名 | 'favorites' | 'pinned' | 'saved' | 设备）、
// domainSelection（已选中的域名）、showOnlyWS（仅显示 WebSocket）、showOnlyFailed（仅显示失败）。
interface FlowsState {
  flows: Flow[];
  selectedId: string | null;
  searchQuery: string;
  // 来源列表选中项：'all' | 'domains' | 域名 | 'favorites' | 'pinned' | 'saved' | 设备名
  sourceSelection: string;
  domainSelection: string | null;
  showOnlyWS: boolean;
  showOnlyFailed: boolean;

  setFlows: (flows: Flow[]) => void;
  upsertFlow: (f: Flow) => void;
  removeFlow: (id: string) => void;
  clear: () => void;
  setSelected: (id: string | null) => void;
  setSearch: (q: string) => void;
  setSourceSelection: (s: string) => void;
  setDomainSelection: (d: string | null) => void;
  togglePinned: (id: string) => void;
}

// useFlows — 流量状态 store 实例。
export const useFlows = create<FlowsState>((set, get) => ({
  flows: [],
  selectedId: null,
  searchQuery: '',
  sourceSelection: 'all',
  domainSelection: null,
  showOnlyWS: false,
  showOnlyFailed: false,

  // 整体替换流量列表。
  setFlows: (flows) => set({ flows }),
  // 插入或更新单条流量：已存在（按 id）则原位替换，否则追加到末尾。
  upsertFlow: (f) => {
    const { flows } = get();
    const idx = flows.findIndex((x) => x.id === f.id);
    if (idx >= 0) {
      const next = [...flows];
      next[idx] = f;
      set({ flows: next });
    } else {
      set({ flows: [...flows, f] });
    }
  },
  // 删除指定 id 的流量；若删除的正是当前选中项，则同时清空选中。
  removeFlow: (id) => set({ flows: get().flows.filter((x) => x.id !== id), selectedId: get().selectedId === id ? null : get().selectedId }),
  // 清空全部流量与选中。
  clear: () => set({ flows: [], selectedId: null }),
  setSelected: (id) => set({ selectedId: id }),
  setSearch: (q) => set({ searchQuery: q }),
  // 切换来源选中项时，同时清空二级域名筛选（域名属于某个来源之下）。
  setSourceSelection: (s) => set({ sourceSelection: s, domainSelection: null }),
  setDomainSelection: (d) => set({ domainSelection: d }),
  // 切换指定流量的置顶标记（isPinned 取反）。
  togglePinned: (id) => {
    const { flows } = get();
    set({
      flows: flows.map((f) => (f.id === id ? { ...f, isPinned: !f.isPinned } : f)),
    });
  },
}));

// --- 派生筛选函数（基于流量切片做组合过滤）---

// filterFlows — 按来源/域名/搜索词/类型标记组合筛选流量列表。
// 参数：state — 流量切片状态（含 flows 与各类筛选条件）。
// 返回值：过滤后的 Flow 数组（不修改原始列表）。
export function filterFlows(state: FlowsState): Flow[] {
  const { flows, sourceSelection, domainSelection, searchQuery, showOnlyWS, showOnlyFailed } = state;
  let out = flows;
  // 收藏视图：仅显示已固定且未保存的流量（即手动收藏项）。
  if (sourceSelection === 'favorites') out = out.filter((f) => f.isSaved === false && f.isPinned);
  // 置顶视图：仅显示固定流量。
  if (sourceSelection === 'pinned') out = out.filter((f) => f.isPinned);
  // 域名筛选：按请求主机名过滤。
  if (domainSelection) out = out.filter((f) => f.host === domainSelection);
  // 仅 WebSocket / 仅失败（错误或状态码 >= 400）。
  if (showOnlyWS) out = out.filter((f) => f.isWebSocket);
  if (showOnlyFailed) out = out.filter((f) => !!f.error || f.responseStatus >= 400);
  // 关键字搜索：对 URL、方法、主机、状态码做不区分大小写的子串匹配。
  if (searchQuery) {
    const q = searchQuery.toLowerCase();
    out = out.filter(
      (f) =>
        f.fullUrl.toLowerCase().includes(q) ||
        f.method.toLowerCase().includes(q) ||
        f.host.toLowerCase().includes(q) ||
        (f.responseStatus ? String(f.responseStatus) : '').includes(q),
    );
  }
  return out;
}

// AppState — 应用级状态切片接口。
// 字段：proxy（代理状态）、sslDefault/sslHosts（SSL 默认开关与按主机开关）、
// certInstalled（证书是否已安装）、toolConfig（工具引擎配置）、settings（应用设置）、
// pendingBreakpoints（等待处理的断点流量 id 列表）、
// activeView（当前视图：'flows' 为流量表，'tools:*' 为各工具配置页）、settingsOpen（设置弹窗开关）。
interface AppState {
  proxy: ProxyStatus;
  sslDefault: boolean;
  sslHosts: Record<string, boolean>;
  certInstalled: boolean;
  toolConfig: EngineConfig | null;
  settings: Settings | null;
  pendingBreakpoints: string[]; // 暂停中等待用户决策的流量 id
  // 'flows' 显示流量表；'tools:*' 显示对应工具配置页。
  activeView: string;
  settingsOpen: boolean;

  setProxy: (p: ProxyStatus) => void;
  setSSL: (def: boolean, hosts: Record<string, boolean>) => void;
  setCertInstalled: (v: boolean) => void;
  setToolConfig: (cfg: EngineConfig) => void;
  setSettings: (s: Settings) => void;
  addPendingBreakpoint: (id: string) => void;
  removePendingBreakpoint: (id: string) => void;
  setActiveView: (v: string) => void;
  setSettingsOpen: (open: boolean) => void;
}

// useApp — 应用级状态 store 实例。
export const useApp = create<AppState>((set) => ({
  proxy: { running: false, port: 0, systemProxy: false },
  sslDefault: true,
  sslHosts: {},
  certInstalled: false,
  toolConfig: null,
  settings: null,
  pendingBreakpoints: [],
  activeView: 'flows',
  settingsOpen: false,

  setProxy: (p) => set({ proxy: p }),
  // 同时更新 SSL 默认开关与按主机开关。
  setSSL: (def, hosts) => set({ sslDefault: def, sslHosts: hosts }),
  setCertInstalled: (v) => set({ certInstalled: v }),
  setToolConfig: (cfg) => set({ toolConfig: cfg }),
  setSettings: (s) => set({ settings: s }),
  // 加入待处理断点列表（去重：已存在则忽略）。
  addPendingBreakpoint: (id) => set((s) => ({ pendingBreakpoints: s.pendingBreakpoints.includes(id) ? s.pendingBreakpoints : [...s.pendingBreakpoints, id] })),
  // 从待处理断点列表移除指定 id。
  removePendingBreakpoint: (id) => set((s) => ({ pendingBreakpoints: s.pendingBreakpoints.filter((x) => x !== id) })),
  setActiveView: (v) => set({ activeView: v }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),
}));

// TOOL_VIEWS — 侧边栏可访问的工具视图清单（id 为路由标识，label 为显示名称）。
export const TOOL_VIEWS: { id: string; label: string }[] = [
  { id: 'tools:maplocal', label: 'Map Local' },
  { id: 'tools:mapremote', label: 'Map Remote' },
  { id: 'tools:blocklist', label: 'Block List' },
  { id: 'tools:allowlist', label: 'Allow List' },
  { id: 'tools:breakpoint', label: 'Breakpoints' },
  { id: 'tools:scripting', label: 'Scripting' },
  { id: 'tools:networkconditions', label: 'Network Conditions' },
  { id: 'tools:nocaching', label: 'No Caching' },
  { id: 'tools:rules', label: 'Rules' },
  { id: 'tools:externalproxy', label: 'External Proxy' },
  { id: 'tools:compose', label: 'Compose' },
  { id: 'tools:repeater', label: 'Repeater' },
  { id: 'tools:diff', label: 'Diff' },
];
