// types.ts — 前端 TypeScript 类型定义与格式化工具函数
// 职责：定义与 Go 后端模型（wproxyman/internal/models 与 tools）对应的
// 前端类型，并提供请求/响应体的解码与展示格式化工具。
// 交互模块：被 store.ts、services/api.ts 及所有组件引用；
// 是前后端数据契约（JSON 结构）在前端的唯一定义处。
// 说明：请求/响应体（body）以 base64 字符串形式传输（Go []byte → JSON），
// 需要先 decodeBody 解码为字节数组再按需展示。

// Header — 单个 HTTP 头（名称/值对），与后端 Header 模型对应。
export interface Header {
  name: string;
  value: string;
}

// Cookie — HTTP Cookie 的解析结果，字段均与后端模型一一对应。
export interface Cookie {
  name: string;
  value: string;
  domain?: string;
  path?: string;
  expires?: string;
  secure?: boolean;
  httpOnly?: boolean;
  sameSite?: string;
}

// WSMessage — WebSocket 单条消息。
// direction：消息方向（请求/响应）；opcode：WebSocket 操作码；
// data：消息内容（base64）；timestamp：消息时间戳（毫秒）。
export interface WSMessage {
  direction: 'request' | 'response';
  opcode: string;
  data: string; // base64 编码的消息数据
  timestamp: number;
}

// Flow — 一条完整的 HTTP/HTTPS/WebSocket 流量记录，与后端 Flow 模型对应。
// 字段分组：来源信息 / 请求信息 / 响应信息 / 时间信息 / WebSocket / 附加标记。
export interface Flow {
  id: string;
  sourceId?: string;
  sourceName?: string;
  clientAddr?: string;

  // ---- 请求行信息 ----
  scheme: string;
  method: string;
  host: string;
  path: string;
  query: string;
  fullUrl: string;
  httpVersion: string;
  tls: boolean;

  // ---- 请求头与请求体 ----
  requestHeaders: Header[];
  requestBody: string; // base64 编码的请求体
  requestSize: number;
  requestMimeType: string;
  requestCookies?: Cookie[];
  requestTruncated?: boolean; // 请求体是否因超过大小上限被截断

  // ---- 响应头与响应体 ----
  responseStatus: number;
  responseReason: string;
  responseHeaders: Header[];
  responseBody: string; // base64 编码的响应体
  responseSize: number;
  responseMimeType: string;
  responseCookies?: Cookie[];
  responseTruncated?: boolean; // 响应体是否被截断

  // ---- 时间信息 ----
  startedAt: number;
  completedAt: number;
  duration: number;

  // ---- WebSocket 信息 ----
  isWebSocket: boolean;
  webSocketClosed?: boolean;
  webSocketMessages?: WSMessage[];

  // ---- 附加标记 ----
  error?: string; // 请求出错时的错误信息
  toolType?: string; // 命中工具类型（如断点/脚本等）
  isPinned?: boolean; // 是否被置顶（固定）
  isSaved?: boolean; // 是否已保存
}

// ============ 工具（Tools）配置类型（镜像 internal/tools）============

// URLMatch — URL 匹配规则：按模式（支持正则）与方法过滤。
export interface URLMatch {
  pattern: string;
  isRegex: boolean; // pattern 是否按正则解释
  method: string; // HTTP 方法（* 表示任意）
  ignoreCase: boolean; // 匹配时是否忽略大小写
}

// ToolRule — 所有工具规则的公共基接口。
export interface ToolRule {
  id: string;
  name: string;
  enabled: boolean;
  match: URLMatch;
}

// MapLocalRule — 本地映射规则：将匹配请求替换为本地文件或内联内容。
export interface MapLocalRule extends ToolRule {
  localFile: string; // 本地文件路径（type = "file" 时）
  body: string; // 内联响应体（type = "inline" 时）
  status: number; // 替换响应的状态码
  headers: Header[]; // 替换响应的响应头
  type: string; // "file" | "inline" — 本地文件 / 内联文本两种模式
}

// MapRemoteRule — 远程映射规则：将匹配请求转发到指定目标 URL。
export interface MapRemoteRule extends ToolRule {
  targetUrl: string;
}

// BlockListRule — 黑名单规则：匹配到的请求将被直接拦截（阻断）。
export interface BlockListRule extends ToolRule {}

// AllowListRule — 白名单规则：匹配到的请求放行，其余被阻断。
export interface AllowListRule extends ToolRule {}

// BreakpointRule — 断点规则：在指定阶段（请求/响应）暂停流量以便手动编辑。
export interface BreakpointRule extends ToolRule {
  phases: string[]; // 生效阶段："request" | "response"
}

// ScriptEntry — 自定义脚本规则：匹配请求时执行用户编写的 JS 代码。
export interface ScriptEntry {
  id: string;
  name: string;
  enabled: boolean;
  match: URLMatch;
  code: string; // 脚本源码
  log?: string[]; // 脚本运行日志
}

// NetworkProfile — 网络条件配置（限速/延迟/丢包模拟）。
export interface NetworkProfile {
  name: string;
  downloadBps: number; // 下载限速（字节/秒）
  uploadBps: number; // 上传限速（字节/秒）
  latencyMs: number; // 模拟网络延迟（毫秒）
  packetLossPct: number; // 模拟丢包率（百分比）
}

// RuleAction — 规则工具中的单个动作：增删改请求头/响应体或重定向。
export interface RuleAction {
  type: 'addHeader' | 'removeHeader' | 'replaceHeader' | 'replaceBody' | 'redirect';
  headerName: string;
  headerValue: string;
  from: string; // 替换类动作的源文本
  to: string; // 替换类动作的目标文本 / 重定向目标 URL
  phase: string; // 动作生效阶段（请求/响应）
}

// RuleToolEntry — 规则工具条目：一组动作与 URL 匹配规则绑定。
export interface RuleToolEntry extends ToolRule {
  actions: RuleAction[];
}

// EngineConfig — 工具引擎总配置：涵盖所有子工具的开/关与规则集合。
export interface EngineConfig {
  mapLocal: { enabled: boolean; rules: MapLocalRule[] };
  mapRemote: { enabled: boolean; rules: MapRemoteRule[] };
  blockList: { enabled: boolean; blockAll: boolean; rules: BlockListRule[] };
  allowList: { enabled: boolean; rules: AllowListRule[] };
  breakpoints: { enabled: boolean; rules: BreakpointRule[] };
  scripts: { enabled: boolean; scripts: ScriptEntry[] };
  noCaching: boolean; // 禁用缓存
  networkConditions: { enabled: boolean; profile: NetworkProfile };
  externalProxy: {
    enabled: boolean;
    host: string;
    port: number;
    username: string;
    password: string;
    bypassDomains: string[]; // 不走外部代理的域名白名单
    type: string;
  };
  rules: { enabled: boolean; rules: RuleToolEntry[] };
}

// Settings — 应用设置（端口、自动启动、SSL、主题等），与后端持久化模型对应。
export interface Settings {
  proxyPort: number; // 代理监听端口
  autoStartProxy: boolean; // 启动时自动开启代理
  sslEnabledDefault: boolean; // SSL 代理默认开关
  sslHosts: Record<string, boolean>; // 各主机名 SSL 解密开关
  theme: string; // 界面主题
  maxBodyBytes: number; // 请求/响应体捕获大小上限（字节）
  toolConfig?: EngineConfig; // 工具引擎配置
}

// ProxyStatus — 代理运行状态（是否运行、监听端口、系统代理开关）。
export interface ProxyStatus {
  running: boolean;
  port: number;
  systemProxy: boolean;
}

// ============ UI 辅助函数 ============

// decodeBody — 将 base64 字符串解码为字节数组。
// 参数：b64 — base64 编码的请求/响应体。
// 返回值：解码后的 Uint8Array；输入为空或解码失败时返回空数组。
export function decodeBody(b64: string): Uint8Array {
  if (!b64) return new Uint8Array(0);
  try {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes;
  } catch {
    return new Uint8Array(0);
  }
}

// bodyToText — 将 base64 请求/响应体转为可直接展示的 UTF-8 文本。
// 参数：b64 — base64 编码的 body。
// 返回值：解码后的文本；非 UTF-8/二进制数据时回退为 latin1 逐字节文本。
export function bodyToText(b64: string): string {
  if (!b64) return '';
  try {
    // 先按字节构造 UTF-8 百分号编码串，再用 decodeURIComponent 还原为文本。
    return decodeURIComponent(
      Array.from(decodeBody(b64))
        .map((b) => '%' + b.toString(16).padStart(2, '0'))
        .join(''),
    );
  } catch {
    // 二进制或非法 UTF-8：回退为 latin1（每字节对应一个字符）逐字拼接。
    const bin = atob(b64);
    let out = '';
    for (let i = 0; i < bin.length; i++) out += String.fromCharCode(bin.charCodeAt(i));
    return out;
  }
}

// isLikelyText — 粗略判断 base64 body 是否可能是文本内容。
// 判定方法：取前 512 字节，若其中出现 NUL 字节（0x00）则视为二进制。
// 返回值：true 表示可能为文本（或空数据），false 表示更可能是二进制。
export function isLikelyText(b64: string): boolean {
  const bytes = decodeBody(b64);
  const n = Math.min(bytes.length, 512);
  for (let i = 0; i < n; i++) {
    if (bytes[i] === 0) return false;
  }
  return n > 0 || bytes.length === 0;
}

// formatBytes — 将字节数格式化为人类可读的字符串（B/KB/MB/GB）。
// 参数：n — 字节数；返回值：如 "1.5 KB"。
export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

// formatDuration — 将毫秒时长格式化为可读字符串。
// 参数：ms — 毫秒；返回值：如 "<1 ms"、"230 ms"、"1.25 s"。
export function formatDuration(ms: number): string {
  if (ms < 1) return '<1 ms';
  if (ms < 1000) return `${ms.toFixed(0)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

// formatTime — 将时间戳格式化为 24 小时制本地时间字符串。
// 参数：ts — 毫秒时间戳；返回值：如 "14:03:22"。
export function formatTime(ts: number): string {
  const d = new Date(ts);
  return d.toLocaleTimeString('en-US', { hour12: false });
}

// ============ 格式化辅助函数 ============

// formatJSON — 美化 JSON 文本（2 空格缩进）。
// 参数：text — 待美化的 JSON 字符串。
// 返回值：美化后的 JSON；若解析失败则原样返回（用于展示非 JSON 文本）。
export function formatJSON(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

// formatXML — 美化 XML 文本（2 空格缩进）。
// 实现：基于轻量级标签扫描器逐标签处理，可安全处理格式不规范的 XML。
// 返回值：格式化后的文本；若无可格式化内容则原样返回。
export function formatXML(text: string): string {
  const input = text.trim();
  if (!input || !input.includes('<')) return text;
  let out = '';
  let indent = 0;
  // 正则同时匹配两种片段：XML 标签（<...>）与标签之间的文本。
  const re = /(<[^>]+>)|([^<]+)/g;
  let m: RegExpExecArray | null;
  let any = false; // 是否产生了任何格式化输出
  while ((m = re.exec(input)) !== null) {
    const tag = m[1];
    const textPart = m[2];
    if (tag) {
      const trimmed = tag.trim();
      const isClosing = /^<\//.test(trimmed); // 闭合标签 </x>
      const isDeclaration = /^<\?/.test(trimmed) || /^<!/.test(trimmed); // 处理指令/DOCTYPE
      const isSelfClose = /\/>$/.test(trimmed); // 自闭合标签 <x/>
      // 闭合标签先减缩进，保证闭合标签与开始标签对齐。
      if (isClosing) indent = Math.max(0, indent - 1);
      out += '  '.repeat(indent) + trimmed + '\n';
      // 只有非闭合、非自闭合、非声明的开始标签才增加缩进。
      if (!isClosing && !isSelfClose && !isDeclaration) indent++;
      any = true;
    } else if (textPart && textPart.trim()) {
      // 标签间的纯文本片段：去除首尾空白后按当前缩进输出。
      out += '  '.repeat(indent) + textPart.trim() + '\n';
      any = true;
    }
  }
  return any ? out.trim() : text;
}

// mimeCategory — 对 MIME 类型进行分类，供界面决定如何展示内容
//（JSON/XML/HTML 做语法高亮，图片/音频/视频做预览，其余按文本或二进制处理）。
// 参数：mime — 原始 MIME 字符串（可带 charset 等参数）。
// 返回值：'json' | 'xml' | 'html' | 'text' | 'image' | 'audio' | 'video' | 'binary'。
export function mimeCategory(mime: string): 'json' | 'xml' | 'html' | 'text' | 'image' | 'audio' | 'video' | 'binary' {
  const m = (mime || '').toLowerCase().split(';')[0].trim();
  if (m.includes('json')) return 'json';
  if (m.includes('xml') || m.includes('xhtml')) return 'xml';
  if (m.includes('html')) return 'html';
  if (m.startsWith('image/')) return 'image';
  if (m.startsWith('audio/')) return 'audio';
  if (m.startsWith('video/')) return 'video';
  if (m.includes('text/') || m === 'application/javascript' || m.includes('x-www-form-urlencoded') || m.includes('graphql')) return 'text';
  return 'binary';
}
