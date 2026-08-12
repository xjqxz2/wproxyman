// api.ts — 后端（Go/Wails）调用的统一封装层
// 职责：在 Wails 自动生成的绑定与事件总线之上提供一层薄封装，
// 将后端方法组织为 proxy / ssl / flows / tools / sessions / codegen / cert / settings 等分组，
// 供 App.tsx 与各组件调用。
// 交互模块：
//   - wailsjs/go/main/App：Wails 自动生成的后端方法绑定；
//   - wailsjs/runtime：EventsOn（订阅后端事件）、EventsEmit（向后端发送事件）；
//   - types.ts：定义本层返回值/入参的规范类型。
//
// 类型说明：Wails 生成的 TS 模型类仅用于类型标注——运行时实际载荷是普通 JSON
// 对象。Go 的 []byte 字段在运行时序列化为 base64 字符串（而生成的 `number[]`
// 类型标注并不准确），因此在本边界统一转换为 types.ts 中的规范类型。
import * as App from '../../wailsjs/go/main/App';
import { EventsOn, EventsEmit } from '../../wailsjs/runtime';
import type { EngineConfig, Flow, Settings } from '../types';

// Events — 前后端事件名常量表（与 Go 侧发送的事件名保持一致）。
// FlowNew/FlowUpdated/FlowCompleted/FlowPaused/FlowResumed/FlowDeleted：
// 流量生命周期事件；FlowsCleared/FlowsReplaced/FlowsImported：列表级事件；
// ProxyStatus/CertStatus：代理与证书状态事件；AppReady：后端就绪信号。
export const Events = {
  FlowNew: 'flow:new',
  FlowUpdated: 'flow:updated',
  FlowCompleted: 'flow:completed',
  FlowPaused: 'flow:paused',
  FlowResumed: 'flow:resumed',
  FlowDeleted: 'flow:deleted',
  FlowsCleared: 'flows:cleared',
  FlowsReplaced: 'flows:replaced',
  FlowsImported: 'flows:imported',
  ProxyStatus: 'proxy:status',
  CertStatus: 'cert:status',
  AppReady: 'app:ready',
} as const;

// api — 后端方法分组封装对象。
// 每个方法名与 Go 侧 App 结构体方法一一对应；
// 需要返回 Flow/EngineConfig/Settings 的调用处使用类型断言转换到 types.ts 规范类型。
export const api = {
  // ---- 代理控制 ----
  // startProxy：启动代理，port 缺省为 0（使用设置中的端口）。
  startProxy: (port = 0) => App.StartProxy(port),
  stopProxy: () => App.StopProxy(),
  isProxyRunning: () => App.IsProxyRunning(),
  getProxyPort: () => App.GetProxyPort(),
  // setSystemProxyEnabled：开启/关闭系统级代理（把流量导向本代理）。
  setSystemProxyEnabled: (on: boolean) => App.SetSystemProxyEnabled(on),
  getSystemProxyEnabled: () => App.GetSystemProxyEnabled(),

  // ---- SSL 解密 ----
  // setSSLProxyEnabled：开启/关闭指定主机的 SSL 解密（host 为空串表示全局默认）。
  setSSLProxyEnabled: (host: string, on: boolean) => App.SetSSLProxyEnabled(host, on),
  // getSSLProxyState：获取 SSL 默认开关与各主机开关。
  getSSLProxyState: () => App.GetSSLProxyState(),

  // ---- 流量（Flows）----
  // getFlows：拉取全部流量；getFlow：按 id 拉取单条流量。
  getFlows: () => App.GetFlows() as unknown as Promise<Flow[]>,
  getFlow: (id: string) => App.GetFlow(id) as unknown as Promise<Flow>,
  clearFlows: () => App.ClearFlows(),
  deleteFlow: (id: string) => App.DeleteFlow(id),
  // setFlowPinned：设置流量的置顶标记。
  setFlowPinned: (id: string, pinned: boolean) => App.SetFlowPinned(id, pinned),

  // ---- 工具（Tools）----
  // getToolConfig：获取工具引擎总配置；applyToolConfig：保存并应用工具配置。
  getToolConfig: () => App.GetToolConfig() as unknown as Promise<EngineConfig>,
  applyToolConfig: (cfg: EngineConfig) => App.ApplyToolConfig(cfg as never),
  // resolveBreakpoint：处理断点，modified 为编辑后的流量（恢复/放弃/修改转发）。
  resolveBreakpoint: (flowId: string, modified: Flow) =>
    App.ResolveBreakpoint(flowId, modified as never),
  // sendRequest：把构造好的流量作为新请求发出（用于 Compose/Repeater 工具）。
  sendRequest: (flow: Flow) => App.SendRequest(flow as never) as unknown as Promise<Flow>,

  // ---- 会话（Sessions）/ 导入导出 ----
  saveSession: (path: string) => App.SaveSession(path),
  openSession: (path: string) => App.OpenSession(path) as unknown as Promise<Flow[]>,
  importSession: (path: string) => App.ImportSession(path) as unknown as Promise<Flow[]>,
  // exportHAR / importHAR：与 HAR 格式互导。
  exportHAR: (path: string, ids: string[]) => App.ExportHAR(path, ids),
  importHAR: (path: string) => App.ImportHAR(path) as unknown as Promise<Flow[]>,

  // ---- 代码生成（Codegen）----
  generateCurl: (id: string) => App.GenerateCurl(id),
  generateNodeFetch: (id: string) => App.GenerateNodeFetch(id),
  generatePostman: (ids: string[]) => App.GeneratePostmanCollection(ids),
  // importCurlText：解析 cURL 命令文本并生成一条新的流量。
  importCurlText: (text: string) => App.ImportCurlText(text) as unknown as Promise<Flow>,

  // ---- 证书（CA Certificate）----
  getCACertPEM: () => App.GetCACertPEM(),
  installCertificate: () => App.InstallCertificate(),
  removeCertificate: () => App.RemoveCertificate(),
  isCertificateInstalled: () => App.IsCertificateInstalled(),

  // ---- 设置 / 其他 ----
  getSettings: () => App.GetSettings() as unknown as Promise<Settings>,
  setSettings: (s: Settings) => App.SetSettings(s as never),
  getLANIPs: () => App.GetLANIPs(),

  // ---- 下载 ----
  // saveFlowContent：将请求或响应内容保存到文件（side 指定 request/response）。
  saveFlowContent: (flowId: string, side: 'request' | 'response') => App.SaveFlowContent(flowId, side),
};

// onEvent — 订阅 Wails 后端事件。
// 参数：event — 事件名（见 Events 常量）；cb — 收到事件数据时回调（data 已按 T 类型化）。
// 返回值：取消订阅函数（调用后不再接收该事件）。
export function onEvent<T>(event: string, cb: (data: T) => void): () => void {
  const stop = EventsOn(event, cb as (data: unknown) => void);
  return () => stop();
}

// emit — 向 Wails 后端发送事件。
// 参数：event — 事件名；data — 可选的事件载荷（将被序列化后传给 Go 侧）。
export function emit(event: string, data?: unknown) {
  EventsEmit(event, data);
}
