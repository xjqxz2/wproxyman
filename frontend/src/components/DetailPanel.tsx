// DetailPanel — 右侧详情面板：展示当前选中流量（Flow）的详细信息。
// 自包含：所有内部子组件（Overview/Message/WebSocket/Breakpoint）都在本文件中。
//
// 标签页：Overview（概览） | Request（请求） | Response（响应） | WebSocket（仅 flow.isWebSocket 时显示）。
// Request/Response 共享 MessageView，其内部有 Headers（头）| Body（体）| Raw（原文）三个子标签。
// 断点处理：当选中流量 id 处于待处理断点状态时，弹出一个可编辑模态框，
// 替换掉请求/响应的只读检视视图（用于编辑后放行或取消）。
//
// 交互模块：
//   - store：useFlows（流量列表、选中 id）、useApp（待处理断点列表）；
//   - services/api：resolveBreakpoint（放行断点）、generateCurl/generateNodeFetch（代码生成）、saveFlowContent（保存正文）；
//   - types：bodyToText/decodeBody/formatBytes/formatDuration/formatJSON/formatTime/formatXML/isLikelyText/mimeCategory。
//   - @monaco-editor/react：惰性加载的代码编辑器（用于断点编辑，离线可降级为 <pre>）。

import { Component, lazy, Suspense, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { loader } from '@monaco-editor/react';
import type { EditorProps, Monaco } from '@monaco-editor/react';
import { useI18n } from '../i18n';
import { ArrowDown, ArrowUp, Copy, Download, Plus, Send, Terminal, Trash2 } from 'lucide-react';
import { useApp, useFlows } from '../store';
import { api } from '../services/api';
import type { Flow, Header } from '../types';
import {
  bodyToText,
  decodeBody,
  formatBytes,
  formatDuration,
  formatJSON,
  formatTime,
  formatXML,
  isLikelyText,
  mimeCategory,
} from '../types';

// ---------------------------------------------------------------------------
// Monaco 编辑器
// ---------------------------------------------------------------------------

// 从本地打包副本（public/monaco/vs）加载 Monaco，而不是 CDN，
// 以保证在受限/离线网络环境下编辑器不会白屏。必须在任何编辑器挂载前执行。
loader.config({ paths: { vs: './monaco/vs' } });

// 安装的 @monaco-editor/react (4.7.0) 暴露的是 `beforeMount` 而非 `beforeRender`；
// 在此处定义主题，使每个编辑器挂载时都使用面板主题。
function defineWpxTheme(monaco: Monaco): void {
  monaco.editor.defineTheme('wpx-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [],
    colors: { 'editor.background': '#1e1f23' },
  });
}

// Monaco 是惰性加载的。动态引入编辑器组件，使面板其余部分在编辑器模块
// 不可用时仍能正常工作；若渲染失败则降级为纯 <pre> 文本展示。
const MonacoAsync = lazy(() => import('@monaco-editor/react').then((m) => ({ default: m.default })));

// EditorBoundary — 编辑器错误边界：Monaco 渲染异常时显示 fallback 内容
//（避免整个详情面板因编辑器崩溃而白屏）。
class EditorBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    if (this.state.failed) return this.props.fallback;
    return this.props.children;
  }
}

// LazyEditor — 惰性 + 容错的编辑器包装。
// 参数：props — Monaco Editor 的 EditorProps（value、language、onChange 等）。
// 返回值：加载中显示 "Loading editor…"，失败显示纯文本 <pre>。
function LazyEditor(props: EditorProps) {
  const { t } = useI18n();
  return (
    <EditorBoundary fallback={<pre className="body-view">{String(props.value ?? '')}</pre>}>
      <Suspense fallback={<div className="body-empty">{t('detail.loading')}</div>}>
        <MonacoAsync {...props} loading={<div className="body-empty">{t('detail.loading')}</div>} />
      </Suspense>
    </EditorBoundary>
  );
}

// EDITOR_OPTIONS — 编辑器的公共选项：关闭小地图、不超出末行滚动、
// 字号 12、自动换行。
const EDITOR_OPTIONS = {
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  fontSize: 12,
  wordWrap: 'on',
} as const;

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// pickLanguage — 根据 MIME 类型挑选编辑器语法高亮语言。
// 参数：mime — 请求/响应的 MIME 类型；返回值：Monaco 语言标识，未知类型回退 'plaintext'。
function pickLanguage(mime: string): string {
  const m = (mime || '').toLowerCase();
  if (m.includes('json')) return 'json';
  if (m.includes('html')) return 'html';
  if (m.includes('css')) return 'css';
  if (m.includes('javascript') || m.includes('ecmascript')) return 'javascript';
  if (m.includes('xml')) return 'xml';
  return 'plaintext';
}

// textToBase64 — 将 UTF-8 文本编码为 base64 字符串（供断点编辑后回传后端）。
// 参数：text — 普通文本；返回值：base64 编码串。
function textToBase64(text: string): string {
  return btoa(unescape(encodeURIComponent(text)));
}

// buildRaw — 按原始 HTTP 报文格式（CRLF 分隔）拼装请求或响应文本。
// 参数：flow — 流量记录；side — 'request' | 'response'。
// 返回值：类似 "GET /path?q=1 HTTP/1.1\r\nHeader: value\r\n\r\nbody" 的原始报文。
function buildRaw(flow: Flow, side: 'request' | 'response'): string {
  // 规范化 HTTP 版本号：以 "HTTP/" 开头则原样使用，否则补前缀，缺失时默认 HTTP/1.1。
  const version =
    flow.httpVersion && flow.httpVersion.startsWith('HTTP/')
      ? flow.httpVersion
      : flow.httpVersion
        ? `HTTP/${flow.httpVersion}`
        : 'HTTP/1.1';

  if (side === 'request') {
    // 请求：请求行（方法 + 路径 + 查询串 + 版本）+ 请求头 + 空行 + 请求体。
    const query = flow.query ? (flow.query.startsWith('?') ? flow.query : `?${flow.query}`) : '';
    const start = `${flow.method} ${flow.path}${query} ${version}`;
    return [start, ...(flow.requestHeaders ?? []).map((h) => `${h.name}: ${h.value}`), '', bodyToText(flow.requestBody)].join('\r\n');
  }

  // 响应：状态行（版本 + 状态码 + 原因短语）+ 响应头 + 空行 + 响应体。
  const start = `${version} ${flow.responseStatus || ''} ${flow.responseReason || ''}`.trim();
  return [start, ...(flow.responseHeaders ?? []).map((h) => `${h.name}: ${h.value}`), '', bodyToText(flow.responseBody)].join('\r\n');
}

// hexSummary — 生成二进制的十六进制摘要文本（每个字节显示为 0xNN）。
// 参数：b64 — base64 正文；limit — 最多展示的字节数（默认 512），超出截断并追加省略号。
function hexSummary(b64: string, limit = 512): string {
  const bytes = decodeBody(b64);
  const n = Math.min(bytes.length, limit);
  const parts: string[] = [];
  for (let i = 0; i < n; i++) {
    parts.push('0x' + bytes[i].toString(16).padStart(2, '0'));
  }
  if (bytes.length > n) parts.push('…');
  return parts.join(' ');
}

// copyToClipboard — 复制文本到剪贴板（带兼容降级）。
// 优先使用 navigator.clipboard API；不可用或失败时，回退到
// 隐藏 textarea + document.execCommand('copy') 的传统方案。
async function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // 失败则继续走下面的传统方案。
    }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand('copy');
  } catch {
    // 剪贴板不可用 — 没有别的办法，静默忽略。
  }
  document.body.removeChild(ta);
}

// ---------------------------------------------------------------------------
// JSON / XML 美化格式
// ---------------------------------------------------------------------------

// stripBOM — 若文本以 UTF-8 字节序标记（BOM）开头则移除之。
function stripBOM(s: string): string {
  return s.charCodeAt(0) === 0xfeff ? s.slice(1) : s;
}

// looksLikeJson — 内容嗅探判断是否为 JSON（Content-Type 缺失时的兜底方案）。
function looksLikeJson(text: string): boolean {
  const t = text.trim();
  return t.startsWith('{') || t.startsWith('[');
}

// looksLikeXml — 内容嗅探判断是否为 XML（Content-Type 缺失时的兜底方案）。
// 排除以 <html / <!doctype 开头的 HTML 文档。
function looksLikeXml(text: string): boolean {
  const t = text.trim();
  return t.startsWith('<?xml') || (t.startsWith('<') && !/^<(html|!doctype)/i.test(t));
}

// ---------------------------------------------------------------------------
// Overview（概览）标签页
// ---------------------------------------------------------------------------

// OvRow — 概览页的一行键值对（key 灰色，value 正文色）。
function OvRow({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="ov-row">
      <span className="k">{k}</span>
      <span className="v">{v}</span>
    </div>
  );
}

// OverviewTab — 概览标签页：分 Request / Response 两个区块展示基本信息，
// 以及错误标签（如有）与命中的工具标签（如有）。
function OverviewTab({ flow }: { flow: Flow }) {
  const { t } = useI18n();
  return (
    <div className="detail-content">
      <div className="ov-section">
        <div className="ov-title">{t('detail.request')}</div>
        <OvRow k={t('detail.url')} v={flow.fullUrl} />
        <OvRow k={t('detail.method')} v={flow.method} />
        <OvRow k={t('detail.host')} v={flow.host} />
        <OvRow k={t('detail.clientAddr')} v={flow.clientAddr || '—'} />
        <OvRow k={t('detail.tls')} v={flow.tls ? t('detail.yes') : t('detail.no')} />
        <OvRow k={t('detail.httpVersion')} v={flow.httpVersion || '—'} />
      </div>

      <div className="ov-section">
        <div className="ov-title">{t('detail.response')}</div>
        <OvRow k={t('detail.status')} v={flow.responseStatus ? `${flow.responseStatus} ${flow.responseReason}`.trim() : '—'} />
        <OvRow k={t('detail.statusCode')} v={flow.responseStatus ? String(flow.responseStatus) : '—'} />
        <OvRow k={t('detail.mimeType')} v={flow.responseMimeType || '—'} />
        <OvRow k={t('detail.size')} v={formatBytes(flow.responseSize)} />
        <OvRow k={t('detail.duration')} v={formatDuration(flow.duration)} />
      </div>

      {/* 请求出错时展示红色错误标签。 */}
      {flow.error && (
        <div className="ov-section">
          <span className="tag red">{flow.error}</span>
        </div>
      )}

      {/* 命中工具（如断点/脚本/规则）时展示蓝色工具标签。 */}
      {flow.toolType && (
        <div className="ov-section">
          <span className="tag blue">{t('detail.toolApplied', { tool: flow.toolType })}</span>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Request / Response（请求 / 响应）视图
// ---------------------------------------------------------------------------

// Sub — MessageView 的子标签类型：headers（头）| body（体）| raw（原文）。
type Sub = 'headers' | 'body' | 'raw';

// HeadersTable — 渲染头字段的键值表格（无头时显示占位文本）。
function HeadersTable({ headers }: { headers: Header[] }) {
  const { t } = useI18n();
  if (headers.length === 0) return <div className="body-empty">{t('detail.noHeaders')}</div>;
  return (
    <table className="hdr-table">
      <tbody>
        {headers.map((h, i) => (
          <tr key={i}>
            <td>{h.name}</td>
            <td>{h.value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// BodyView — 正文展示：根据内容是否像文本决定渲染方式。
//   - 文本：优先 JSON（Content-Type 或内容嗅探判定）美化展示；
//     其次 XML 美化；其余按纯文本展示。
//   - 二进制：图片按 <img> 预览；其它类型展示十六进制摘要与大小说明。
// 参数：b64 — base64 正文；mime — MIME 类型。
function BodyView({ b64, mime }: { b64: string; mime: string }) {
  const { t } = useI18n();
  if (!b64) return <div className="body-empty">{t('detail.noBody')}</div>;

  if (isLikelyText(b64)) {
    const text = stripBOM(bodyToText(b64));
    const cat = mimeCategory(mime);

    // JSON — 先按 Content-Type 判断，再回退到内容嗅探。
    if (cat === 'json' || looksLikeJson(text)) {
      try {
        const pretty = JSON.stringify(JSON.parse(text), null, 2);
        return <pre className="body-view">{pretty}</pre>;
      } catch {
        // JSON 解析失败：按下面普通文本展示。
      }
    }

    // XML — 先按 Content-Type 判断，再回退到内容嗅探。
    if (cat === 'xml' || looksLikeXml(text)) {
      const formatted = formatXML(text);
      return <pre className="body-view">{formatted}</pre>;
    }

    return <div className="body-view">{text}</div>;
  }

  // 二进制内容：图片内联预览，其余展示十六进制摘要。
  const cat = mimeCategory(mime);
  if (cat === 'image' || mime.startsWith('image/')) {
    return (
      <div className="body-image">
        <img src={`data:${mime};base64,${b64}`} alt={t('detail.body')} />
      </div>
    );
  }

  return (
    <div className="body-view">
      {hexSummary(b64)}
      <div style={{ color: 'var(--text-tertiary)', marginTop: 8 }}>
        {t('detail.binaryBody', { mime: mime || t('detail.unknownType'), size: formatBytes(decodeBody(b64).length) })}
      </div>
    </div>
  );
}

// isDownloadableAsset — 判断正文是否作为"可下载资产"处理：
// 图片、音频、视频、PDF、压缩包、Office 文档等二进制正文提供下载按钮；
// 纯文本视图保持内联展示。
function isDownloadableAsset(mime: string, b64: string): boolean {
  if (!b64) return false;
  const cat = mimeCategory(mime);
  return cat === 'image' || cat === 'audio' || cat === 'video' || cat === 'binary';
}

// MessageView — 请求 / 响应 共用视图。
// 内部三个子标签：Headers（头表格）、Body（正文/下载）、Raw（原始报文）。
// 参数：flow — 流量记录；side — 'request' | 'response'，决定读取哪一侧数据。
function MessageView({ flow, side }: { flow: Flow; side: 'request' | 'response' }) {
  const { t } = useI18n();
  // 当前子标签与下载结果提示（ok + 文本）。
  const [sub, setSub] = useState<Sub>('headers');
  const [dlMsg, setDlMsg] = useState<{ ok: boolean; text: string } | null>(null);
  // 根据 side 取出对应的头/体/截断标记/MIME。
  const isReq = side === 'request';
  const headers = (isReq ? flow.requestHeaders : flow.responseHeaders) ?? [];
  const bodyB64 = isReq ? flow.requestBody : flow.responseBody;
  const truncated = isReq ? flow.requestTruncated : flow.responseTruncated;
  const mime = isReq ? flow.requestMimeType : flow.responseMimeType;
  // 原始报文（按 flow/side 缓存）；若为 JSON 则额外美化后再展示。
  const raw = useMemo(() => buildRaw(flow, side), [flow, side]);
  const rawDisplay = useMemo(() => (mimeCategory(mime) === 'json' ? formatJSON(raw) : raw), [raw, mime]);

  // handleDownload — 将请求/响应正文保存到本地文件（调用后端保存并返回路径）。
  const handleDownload = async () => {
    setDlMsg(null);
    try {
      const path = await api.saveFlowContent(flow.id, side);
      setDlMsg({ ok: true, text: t('detail.savedTo', { path }) });
    } catch (e) {
      setDlMsg({ ok: false, text: String(e) });
    }
  };

  const subtabs: Sub[] = ['headers', 'body', 'raw'];

  return (
    <>
      {/* 子标签切换栏。 */}
      <div className="detail-subtabs">
        {subtabs.map((s) => (
          <button key={s} className={`detail-subtab${sub === s ? ' active' : ''}`} onClick={() => setSub(s)}>
            {s === 'headers' ? t('detail.headers') : s === 'body' ? t('detail.body') : t('detail.raw')}
          </button>
        ))}
      </div>

      <div className="detail-content">
        {sub === 'headers' && <HeadersTable headers={headers} />}

        {sub === 'body' && (
          <>
            {/* 正文被截断时的提示。 */}
            {truncated && <div className="body-truncated-note">{t('detail.truncated')}</div>}
            {/* 可下载资产（二进制）时提供"保存到文件"按钮与结果反馈。 */}
            {isDownloadableAsset(mime, bodyB64) && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px' }}>
                <button type="button" className="btn" onClick={handleDownload}>
                  <Download size={14} />
                  {t('detail.saveToFile', { side: isReq ? t('detail.requestSide') : t('detail.responseSide') })}
                </button>
                {dlMsg && (
                  <span style={{ fontSize: 11.5, color: dlMsg.ok ? 'var(--ok)' : 'var(--error)', wordBreak: 'break-all' }}>
                    {dlMsg.text}
                  </span>
                )}
              </div>
            )}
            <BodyView b64={bodyB64} mime={mime} />
          </>
        )}

        {sub === 'raw' && (
          // RAW 为只读展示 — 以纯文本渲染（速度快，大正文或离线 Monaco CDN
          // 时也不会白屏）。
          <pre className="body-view">{rawDisplay}</pre>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// WebSocket 标签页
// ---------------------------------------------------------------------------

// WebSocketTab — WebSocket 消息列表：每条消息按方向（请求↑蓝/响应↓绿）着色，
// 展示 opcode 标签、时间戳与负载内容（文本直接显示，二进制显示大小摘要与截断预览）。
function WebSocketTab({ flow }: { flow: Flow }) {
  const { t } = useI18n();
  const messages = flow.webSocketMessages ?? [];
  if (messages.length === 0) return <div className="body-empty">{t('detail.noWSMessages')}</div>;

  return (
    <div className="detail-content">
      {messages.map((m, i) => {
        // 文本消息解码为文本；二进制消息展示大小 + 前 256 字符预览。
        const isText = m.opcode.toLowerCase() === 'text';
        const payload = isText
          ? bodyToText(m.data)
          : t('detail.binaryPayload', {
              size: formatBytes(decodeBody(m.data).length),
              excerpt: m.data.slice(0, 256) + (m.data.length > 256 ? ' …' : ''),
            });
        return (
          <div
            key={i}
            style={{ display: 'flex', gap: 8, padding: '6px 12px', borderBottom: '1px solid rgba(53, 54, 60, 0.5)', alignItems: 'flex-start' }}
          >
            {/* 方向图标：请求向上箭头（蓝），响应向下箭头（绿）。 */}
            {m.direction === 'request' ? (
              <ArrowUp size={14} style={{ flexShrink: 0, marginTop: 2, color: '#60a5fa' }} />
            ) : (
              <ArrowDown size={14} style={{ flexShrink: 0, marginTop: 2, color: '#34d399' }} />
            )}
            {/* opcode 标签（请求蓝 / 响应绿）。 */}
            <span className={m.direction === 'request' ? 'tag blue' : 'tag green'} style={{ flexShrink: 0 }}>
              {m.opcode}
            </span>
            {/* 消息时间戳。 */}
            <span style={{ flexShrink: 0, color: 'var(--text-tertiary)', fontFamily: 'var(--font-mono)', fontSize: 11.5, marginTop: 1 }}>
              {formatTime(m.timestamp)}
            </span>
            {/* 消息负载内容（等宽字体、可换行、可断词）。 */}
            <pre
              style={{
                margin: 0,
                flex: 1,
                minWidth: 0,
                fontFamily: 'var(--font-mono)',
                fontSize: 12,
                color: 'var(--text-primary)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
              }}
            >
              {payload}
            </pre>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 断点模态框
// ---------------------------------------------------------------------------

// BreakpointModal — 断点编辑模态框：当流量命中断点并暂停时弹出。
// 允许用户编辑请求/响应头与正文，然后执行（放行修改后的流量）或取消（原样放行）。
// 参数：flow — 暂停的流量；side — 断点所在阶段（request/response）。
function BreakpointModal({ flow, side }: { flow: Flow; side: 'request' | 'response' }) {
  // 放行断点后从待处理列表移除该流量。
  const removePendingBreakpoint = useApp((s) => s.removePendingBreakpoint);
  const { t } = useI18n();
  const isReq = side === 'request';

  // 编辑状态：正文文本、头列表（可增删改）、错误信息、提交中的忙碌标记。
  const [bodyText, setBodyText] = useState(() => bodyToText(isReq ? flow.requestBody : flow.responseBody));
  const [headers, setHeaders] = useState<Header[]>(() => [...(isReq ? flow.requestHeaders : flow.responseHeaders)]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // setHeader — 更新指定索引处的单个头字段（只改传入的字段，其余保留）。
  const setHeader = (i: number, patch: Partial<Header>) => setHeaders((hs) => hs.map((h, j) => (j === i ? { ...h, ...patch } : h)));

  // resolve — 向后端提交断点决议（放行），成功后从待处理列表移除。
  // 提交期间防重复点击（busy 标记），失败时展示错误信息并解除忙碌。
  const resolve = async (modified: Flow) => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await api.resolveBreakpoint(flow.id, modified);
      removePendingBreakpoint(flow.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  // execute — 把编辑后的正文（重新 base64 编码）与头合并进流量，提交决议。
  const execute = () => {
    const modified: Flow = {
      ...flow,
      ...(isReq
        ? { requestBody: textToBase64(bodyText), requestHeaders: headers }
        : { responseBody: textToBase64(bodyText), responseHeaders: headers }),
    };
    void resolve(modified);
  };

  // cancel — 放弃修改，以原始流量原样放行。
  const cancel = () => void resolve(flow);

  // sectionLabel — 区块标题（小节标签）的样式。
  const sectionLabel: React.CSSProperties = {
    fontSize: 11,
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--text-tertiary)',
    margin: '10px 0 6px',
  };

  return (
    <div className="modal-overlay">
      <div className="modal">
        <div className="modal-header">{t('detail.breakpointTitle', { method: flow.method, path: flow.path })}</div>
        <div className="modal-body">
          {/* 提交失败的错误提示。 */}
          {error && <div style={{ color: 'var(--error)', fontSize: 12, marginBottom: 8 }}>{error}</div>}

          {/* 可编辑的头列表：每行 name + value 输入框 + 删除按钮。 */}
          <div style={sectionLabel}>{t('detail.sectionHeaders', { side: isReq ? t('detail.request') : t('detail.response') })}</div>
          {headers.map((h, i) => (
            <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
              <input
                className="input"
                style={{ flex: '0 0 32%', minWidth: 0 }}
                value={h.name}
                placeholder={t('detail.headerName')}
                onChange={(e) => setHeader(i, { name: e.target.value })}
              />
              <input
                className="input"
                style={{ flex: 1, minWidth: 0 }}
                value={h.value}
                placeholder={t('detail.value')}
                onChange={(e) => setHeader(i, { value: e.target.value })}
              />
              <button className="btn" title={t('detail.removeHeader')} onClick={() => setHeaders((hs) => hs.filter((_, j) => j !== i))}>
                <Trash2 size={14} />
              </button>
            </div>
          ))}
          {/* 新增一个空头条目。 */}
          <button className="btn" onClick={() => setHeaders((hs) => [...hs, { name: '', value: '' }])}>
            <Plus size={14} /> {t('detail.addHeader')}
          </button>

          {/* 可编辑的正文（Monaco 编辑器，按 MIME 自动选择语言）。 */}
          <div style={sectionLabel}>{t('detail.body')}</div>
          <LazyEditor
            height={220}
            language={pickLanguage(isReq ? flow.requestMimeType : flow.responseMimeType)}
            theme="wpx-dark"
            beforeMount={defineWpxTheme}
            value={bodyText}
            onChange={(v) => setBodyText(v ?? '')}
            options={{ ...EDITOR_OPTIONS, readOnly: false }}
          />
        </div>
        {/* 底部操作：取消（原样放行）/ 执行（提交修改）。 */}
        <div className="modal-footer">
          <button className="btn" disabled={busy} onClick={cancel}>
            {t('detail.cancel')}
          </button>
          <button className="btn primary" disabled={busy} onClick={execute}>
            {t('detail.execute')}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// DetailPanel
// ---------------------------------------------------------------------------

// Tab — 详情面板顶层标签类型：overview（概览）| request（请求）|
// response（响应）| websocket（WebSocket）。
type Tab = 'overview' | 'request' | 'response' | 'websocket';

// TabBtn — 顶层标签按钮：label（文案）、active（是否高亮）、onClick（点击回调）。
function TabBtn({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button className={`detail-tab${active ? ' active' : ''}`} onClick={onClick}>
      {label}
    </button>
  );
}

// DetailPanel — 右侧详情面板主组件（无 props，数据来自全局 store）。
export default function DetailPanel() {
  const { t } = useI18n();
  // 全部流量、当前选中 id 与待处理断点列表。
  const flows = useFlows((s) => s.flows);
  const selectedId = useFlows((s) => s.selectedId);
  const pendingBreakpoints = useApp((s) => s.pendingBreakpoints);

  // 从流量列表中取出选中的流量（不存在时为 null）。
  const flow = useMemo(() => flows.find((f) => f.id === selectedId) ?? null, [flows, selectedId]);

  // 当前标签页；若当前选中非 WebSocket 流量却停留在 websocket 标签，则回退到 overview。
  const [tab, setTab] = useState<Tab>('overview');
  const activeTab: Tab = tab === 'websocket' && flow && !flow.isWebSocket ? 'overview' : tab;

  // 断点侧：仅当选中流量处于待处理断点且当前标签为 request/response 时，
  // 返回对应 side 以决定弹出编辑模态框；否则为 null（不弹框）。
  const bpSide: 'request' | 'response' | null =
    flow && pendingBreakpoints.includes(flow.id) && (activeTab === 'request' || activeTab === 'response') ? activeTab : null;

  // 复制 cURL / Node fetch 命令到剪贴板（代码生成失败时静默忽略）。
  const copyCurl = async (f: Flow) => {
    try {
      await copyToClipboard(await api.generateCurl(f.id));
    } catch {
      // codegen failed — nothing to copy
    }
  };
  const copyNodeFetch = async (f: Flow) => {
    try {
      await copyToClipboard(await api.generateNodeFetch(f.id));
    } catch {
      // codegen failed — nothing to copy
    }
  };

  return (
    <div className="detail">
      {flow ? (
        <>
          {/* 顶层标签栏：Overview / Request / Response，WebSocket 流量额外显示 WebSocket。 */}
          <div className="detail-tabs">
            <TabBtn label={t('detail.overview')} active={activeTab === 'overview'} onClick={() => setTab('overview')} />
            <TabBtn label={t('detail.request')} active={activeTab === 'request'} onClick={() => setTab('request')} />
            <TabBtn label={t('detail.response')} active={activeTab === 'response'} onClick={() => setTab('response')} />
            {flow.isWebSocket && <TabBtn label={t('detail.websocket')} active={activeTab === 'websocket'} onClick={() => setTab('websocket')} />}
          </div>

          {/* 按当前标签渲染对应内容区。 */}
          <div style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
            {activeTab === 'overview' && <OverviewTab flow={flow} />}
            {activeTab === 'request' && <MessageView flow={flow} side="request" />}
            {activeTab === 'response' && <MessageView flow={flow} side="response" />}
            {activeTab === 'websocket' && <WebSocketTab flow={flow} />}
          </div>
        </>
      ) : (
        // 未选中流量时的占位提示。
        <div className="detail-content" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <div className="body-empty">{t('detail.selectHint')}</div>
        </div>
      )}

      {/* 底部操作栏：发送到 Compose / 复制 cURL / 复制 Node Fetch（未选中时禁用）。 */}
      <div style={{ display: 'flex', gap: 8, padding: '8px 12px', borderTop: '1px solid var(--border)', flexShrink: 0 }}>
        <button className="btn" disabled={!flow} onClick={() => flow && void copyCurl(flow)} title={t('detail.copyCurlTitle')}>
          <Send size={14} /> {t('detail.sendToCompose')}
        </button>
        <button className="btn" disabled={!flow} onClick={() => flow && void copyCurl(flow)} title={t('detail.copyCurlTitle')}>
          <Terminal size={14} /> {t('detail.copyCurl')}
        </button>
        <button className="btn" disabled={!flow} onClick={() => flow && void copyNodeFetch(flow)} title={t('detail.copyNodeFetchTitle')}>
          <Copy size={14} /> {t('detail.copyNodeFetch')}
        </button>
      </div>

      {/* 断点编辑模态框：key 用 流量id:阶段 保证切换流量/阶段时重建编辑状态。 */}
      {bpSide && flow && <BreakpointModal key={`${flow.id}:${bpSide}`} flow={flow} side={bpSide} />}
    </div>
  );
}
