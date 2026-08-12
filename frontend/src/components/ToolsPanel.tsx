// ToolsPanel.tsx — 工具面板（Tools）入口与各工具配置页
// 职责：根据 activeView 渲染对应的工具配置页面（Map Local / Map Remote / Block List /
// Allow List / Breakpoints / Scripting / Network Conditions / No Caching / Rules /
// External Proxy / Compose / Repeater / Diff），并支持配置的即时保存。
// 交互模块：
//   - store：useApp（读取/写入 toolConfig，通过 patchSection 实现即时应用）；
//   - services/api：api.applyToolConfig（保存配置）、api.sendRequest（Compose/Repeater 发请求）；
//   - types：各类工具规则类型、bodyToText、formatDuration。
//   - @monaco-editor/react：脚本编辑（Scripting）使用的代码编辑器。
// 说明：所有工具的开关/规则变更都会立刻写回 store 并调用后端 applyToolConfig
//（模拟 Proxyman 的即时生效行为）。
import { useState } from 'react';
import type { ReactNode } from 'react';
import Editor from '@monaco-editor/react';
import { Plus, Send, Trash2, X } from 'lucide-react';
import { useApp } from '../store';
import { api } from '../services/api';
import { useI18n } from '../i18n';
import type {
  AllowListRule,
  BlockListRule,
  BreakpointRule,
  EngineConfig,
  Flow,
  Header,
  MapLocalRule,
  MapRemoteRule,
  NetworkProfile,
  RuleAction,
  RuleToolEntry,
  ScriptEntry,
  ToolRule,
  URLMatch,
} from '../types';
import { bodyToText, formatDuration } from '../types';

/* ------------------------------------------------------------------ */
/* 组件局部样式（仅使用设计 token，不覆盖任何全局样式）                */
/* ------------------------------------------------------------------ */

// PANEL_STYLES — 工具面板使用的局部 CSS（注入到 <style> 标签）。
// 所有颜色/背景都引用 theme.css 的 CSS 变量，保证与全局主题一致。
const PANEL_STYLES = `
.tp-col { display: flex; flex-direction: column; gap: 8px; }
.tp-row { display: flex; align-items: center; gap: 8px; }
.tp-grow { flex: 1; min-width: 0; }
.tp-label { display: block; font-size: 11px; color: var(--text-tertiary); margin-bottom: 4px; }
.tp-mono { font-family: var(--font-mono); font-size: 12px; color: var(--text-primary); word-break: break-all; }
.tp-method { width: 120px; }
.tp-num { width: 120px; }
.tp-select { background: var(--bg-input); border: 1px solid var(--border); border-radius: 6px; color: var(--text-primary); padding: 5px 8px; font-size: 12.5px; outline: none; max-width: 100%; }
.tp-select:focus { border-color: var(--border-focus); }
.tp-select option { background: var(--bg-toolbar); }
.tp-textarea { background: var(--bg-input); border: 1px solid var(--border); border-radius: 6px; color: var(--text-primary); padding: 8px 10px; font-size: 12.5px; font-family: var(--font-mono); line-height: 1.5; outline: none; resize: vertical; min-height: 80px; }
.tp-textarea:focus { border-color: var(--border-focus); }
.tp-textarea::placeholder { color: var(--text-tertiary); }
.tp-switch-row { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-secondary); white-space: nowrap; cursor: pointer; user-select: none; }
.tp-check { accent-color: var(--accent); }
.tp-log { font-family: var(--font-mono); font-size: 11.5px; line-height: 1.5; color: var(--warn); background: rgba(251, 191, 36, 0.07); border: 1px solid rgba(251, 191, 36, 0.22); border-radius: 6px; padding: 8px 10px; max-height: 140px; overflow: auto; white-space: pre-wrap; word-break: break-all; }
.tp-error { color: var(--error); font-size: 12.5px; margin-top: 8px; word-break: break-word; }
.tp-response-body { max-height: 300px; overflow: auto; background: var(--bg-input); border: 1px solid var(--border); border-radius: 6px; padding: 10px; font-family: var(--font-mono); font-size: 12px; line-height: 1.5; white-space: pre-wrap; word-break: break-all; margin: 0; }
.tp-monaco { border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
.tp-empty { text-align: center; color: var(--text-tertiary); font-size: 12.5px; }
.tp-mt8 { margin-top: 8px; }
`;

// PanelStyles — 将组件局部样式注入当前页面的 <style> 标签。
function PanelStyles() {
  return <style>{PANEL_STYLES}</style>;
}

/* ------------------------------------------------------------------ */
/* 可复用原语（文件内私有，不导出）                                     */
/* ------------------------------------------------------------------ */

// Switch — 开关按钮（无障碍开关语义，用 aria-pressed 表示状态）。
// 参数：checked — 当前开关状态；onChange — 状态变化回调；title — 悬停提示。
function Switch({
  checked,
  onChange,
  title,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  title?: string;
}) {
  return (
    <button
      type="button"
      className={`switch${checked ? ' on' : ''}`}
      onClick={() => onChange(!checked)}
      title={title}
      aria-pressed={checked}
    />
  );
}

// CardHeader — 工具卡片标题行：总开关 + 标题文字。
// 参数：title — 标题；checked / onChange — 总开关状态及其回调。
function CardHeader({
  title,
  checked,
  onChange,
}: {
  title: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="tc-head">
      <Switch checked={checked} onChange={onChange} />
      <span className="tc-title">{title}</span>
    </div>
  );
}

// MatchRow — URL 匹配规则编辑行：匹配模式输入 + Regex 开关 + 方法输入。
// 参数：match — 当前 URLMatch；onChange — 更新整个 match 对象。
function MatchRow({ match, onChange }: { match: URLMatch; onChange: (m: URLMatch) => void }) {
  const { t } = useI18n();
  return (
    <div className="tp-row" style={{ flexWrap: 'wrap' }}>
      {/* URL 匹配模式（支持通配/正则）。 */}
      <input
        className="input tp-grow"
        value={match.pattern}
        placeholder={t('tools.common.urlPatternPlaceholder')}
        onChange={(e) => onChange({ ...match, pattern: e.target.value })}
      />
      {/* 是否按正则解释模式。 */}
      <label className="tp-switch-row">
        <span>{t('tools.common.regex')}</span>
        <Switch checked={match.isRegex} onChange={(v) => onChange({ ...match, isRegex: v })} />
      </label>
      {/* 匹配的 HTTP 方法（逗号分隔，留空表示任意）。 */}
      <input
        className="input tp-method"
        value={match.method}
        placeholder={t('tools.common.any')}
        title={t('tools.common.methodsTitle')}
        onChange={(e) => onChange({ ...match, method: e.target.value })}
      />
    </div>
  );
}

// HeadersEditor — 请求/响应头列表编辑器：支持逐行编辑名称/值、删除行、新增空行。
// 参数：headers — 头列表；onChange — 更新整个列表；title — 区块标题（默认 'Headers'）。
function HeadersEditor({
  headers,
  onChange,
  title,
}: {
  headers: Header[];
  onChange: (next: Header[]) => void;
  title?: string;
}) {
  const { t } = useI18n();
  return (
    <div className="tp-col">
      <span className="tp-label">{title ?? t('tools.common.headers')}</span>
      {/* 逐行渲染头：name + value 输入 + 删除按钮（原地更新副本再整体回调）。 */}
      {headers.map((h, i) => (
        <div key={i} className="tp-row">
          <input
            className="input tp-grow"
            value={h.name}
            placeholder={t('tools.common.name')}
            onChange={(e) => {
              const next = [...headers];
              next[i] = { ...next[i], name: e.target.value };
              onChange(next);
            }}
          />
          <input
            className="input tp-grow"
            value={h.value}
            placeholder={t('tools.common.value')}
            onChange={(e) => {
              const next = [...headers];
              next[i] = { ...next[i], value: e.target.value };
              onChange(next);
            }}
          />
          <button
            type="button"
            className="tb-btn"
            title={t('tools.common.removeHeader')}
            onClick={() => onChange(headers.filter((_, j) => j !== i))}
          >
            <X size={14} />
          </button>
        </div>
      ))}
      {/* 新增一条空头条目。 */}
      <div>
        <button
          type="button"
          className="btn"
          onClick={() => onChange([...headers, { name: '', value: '' }])}
        >
          <Plus size={14} /> {t('tools.common.addHeader')}
        </button>
      </div>
    </div>
  );
}

// RuleListEditor — 通用规则列表编辑器：为各规则类工具复用。
// 每个规则渲染为一张卡片（启用开关 + 名称 + 删除按钮 + 匹配规则 + 额外字段），
// 底部提供"添加规则"按钮。
// 参数：rules — 规则列表；onChange — 更新整个列表；newRule — 创建新规则的工厂；
// renderExtras — 可选：渲染规则特有的额外字段（传入 rule 与局部 patch 函数）；
// addLabel — 添加按钮文案（默认 'Add Rule'）。
function RuleListEditor<R extends ToolRule>({
  rules,
  onChange,
  newRule,
  renderExtras,
  addLabel,
}: {
  rules: R[];
  onChange: (next: R[]) => void;
  newRule: () => R;
  renderExtras?: (rule: R, patch: (p: Partial<R>) => void) => ReactNode;
  addLabel?: string;
}) {
  const { t } = useI18n();
  return (
    <>
      {rules.map((rule, idx) => {
        // patch — 原地更新第 idx 条规则的指定字段。
        const patch = (p: Partial<R>) => {
          const next = [...rules];
          next[idx] = { ...rule, ...p } as R;
          onChange(next);
        };
        // patchMatch — 更新第 idx 条规则的 match 字段。
        const patchMatch = (m: Partial<URLMatch>) => {
          const next = [...rules];
          next[idx] = { ...rule, match: { ...rule.match, ...m } } as R;
          onChange(next);
        };
        return (
          <div key={rule.id} className="tool-card">
            <div className="tc-head">
              {/* 规则启用开关 + 名称输入 + 删除按钮。 */}
              <Switch checked={rule.enabled} onChange={(v) => patch({ enabled: v } as Partial<R>)} />
              <input
                className="input tp-grow"
                value={rule.name}
                placeholder={t('tools.common.ruleName')}
                onChange={(e) => patch({ name: e.target.value } as Partial<R>)}
              />
              <div className="tc-actions">
                <button
                  type="button"
                  className="tb-btn"
                  title={t('tools.common.deleteRule')}
                  onClick={() => onChange(rules.filter((r) => r.id !== rule.id))}
                >
                  <Trash2 size={15} />
                </button>
              </div>
            </div>
            <MatchRow match={rule.match} onChange={patchMatch} />
            {renderExtras?.(rule, patch)}
          </div>
        );
      })}
      {/* 添加新规则按钮。 */}
      <button type="button" className="btn" onClick={() => onChange([...rules, newRule()])}>
        <Plus size={14} /> {addLabel ?? t('tools.common.addRule')}
      </button>
    </>
  );
}

// ResponseView — 请求响应结果展示（Compose / Repeater 共用）。
// 参数：flow — 发送请求后返回的流量（null 表示尚无结果）；error — 错误信息；
// loading — 是否正在发送。
function ResponseView({
  flow,
  error,
  loading,
}: {
  flow: Flow | null;
  error: string;
  loading: boolean;
}) {
  const { t } = useI18n();
  // 发送中：显示进度提示。
  if (loading) {
    return (
      <div className="tool-card">
        <div className="subtitle" style={{ margin: 0 }}>
          {t('tools.common.sending')}
        </div>
      </div>
    );
  }
  // 发送失败：显示错误信息。
  if (error) return <div className="tp-error">{t('tools.common.errorWith', { error })}</div>;
  // 尚无结果：不渲染。
  if (!flow) return null;
  return (
    <div className="tool-card">
      <div className="tc-head">
        <span className="tc-title">{t('tools.common.response')}</span>
        <div className="tc-actions">
          {/* 状态码与原因短语 + 耗时。 */}
          <span className="tag blue">
            {flow.responseStatus || '-'} {flow.responseReason}
          </span>
          <span className="tag amber">{formatDuration(flow.duration)}</span>
        </div>
      </div>
      {/* 响应头逐行展示。 */}
      {flow.responseHeaders.length > 0 && (
        <div className="tp-col" style={{ marginBottom: 8 }}>
          {flow.responseHeaders.map((h, i) => (
            <div key={i} className="tp-row" style={{ alignItems: 'flex-start' }}>
              <span className="tp-mono" style={{ width: 200, flexShrink: 0 }}>
                {h.name}
              </span>
              <span className="tp-mono">{h.value}</span>
            </div>
          ))}
        </div>
      )}
      {/* 响应体（解码为文本展示，空体显示占位符）。 */}
      <pre className="tp-response-body">{bodyToText(flow.responseBody) || t('tools.common.emptyBody')}</pre>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* 配置管道（EngineConfig 的读写与各类默认值/工厂）                      */
/* ------------------------------------------------------------------ */

// FALLBACKS — EngineConfig 各分区在缺失时的默认值（保证前端渲染安全）。
const FALLBACKS: { [K in keyof EngineConfig]: EngineConfig[K] } = {
  mapLocal: { enabled: false, rules: [] },
  mapRemote: { enabled: false, rules: [] },
  blockList: { enabled: false, blockAll: false, rules: [] },
  allowList: { enabled: false, rules: [] },
  breakpoints: { enabled: false, rules: [] },
  scripts: { enabled: false, scripts: [] },
  noCaching: false,
  networkConditions: {
    enabled: false,
    profile: { name: 'Off', downloadBps: 0, uploadBps: 0, latencyMs: 0, packetLossPct: 0 },
  },
  externalProxy: {
    enabled: false,
    type: 'http',
    host: '',
    port: 0,
    username: '',
    password: '',
    bypassDomains: [],
  },
  rules: { enabled: false, rules: [] },
};

/**
 * patchSection — 修改当前 EngineConfig 的某一个顶层分区并立即应用。
 * 从 store 读取最新状态以避免陈旧闭包（对应 Proxyman 的即时生效行为）。
 * 参数：key — 顶层分区名；fn — 接收当前分区值（深拷贝）并返回新值的函数。
 * 副作用：更新 store 中的 toolConfig 并调用 api.applyToolConfig 同步到后端。
 */
function patchSection<K extends keyof EngineConfig>(
  key: K,
  fn: (sec: EngineConfig[K]) => EngineConfig[K],
): void {
  const cur = useApp.getState().toolConfig;
  if (!cur) return;
  const base = cur[key] ?? FALLBACKS[key];
  const next: EngineConfig = Object.assign({}, cur, { [key]: fn(structuredClone(base)) });
  useApp.getState().setToolConfig(next);
  api.applyToolConfig(next).catch(console.error);
}

// newMatch — 创建默认的 URL 匹配规则（默认模式 *://example.com/*，忽略大小写）。
function newMatch(pattern = '*://example.com/*'): URLMatch {
  return { pattern, isRegex: false, method: '', ignoreCase: true };
}

// newMapLocalRule — 创建一条默认的本地映射规则（file 模式）。
function newMapLocalRule(t: (key: string, params?: Record<string, string | number>) => string): MapLocalRule {
  return {
    id: crypto.randomUUID(),
    name: t('tools.mapLocal.defaultRuleName'),
    enabled: true,
    match: newMatch(),
    type: 'file',
    localFile: '',
    body: '',
    status: 200,
    headers: [],
  };
}

// newMapRemoteRule — 创建一条默认的远程映射规则。
function newMapRemoteRule(t: (key: string, params?: Record<string, string | number>) => string): MapRemoteRule {
  return { id: crypto.randomUUID(), name: t('tools.mapRemote.defaultRuleName'), enabled: true, match: newMatch(), targetUrl: '' };
}

// newBlockListRule — 创建一条默认的黑名单规则。
function newBlockListRule(t: (key: string, params?: Record<string, string | number>) => string): BlockListRule {
  return { id: crypto.randomUUID(), name: t('tools.blockList.defaultRuleName'), enabled: true, match: newMatch() };
}

// newAllowListRule — 创建一条默认的白名单规则。
function newAllowListRule(t: (key: string, params?: Record<string, string | number>) => string): AllowListRule {
  return { id: crypto.randomUUID(), name: t('tools.allowList.defaultRuleName'), enabled: true, match: newMatch() };
}

// newBreakpointRule — 创建一条默认的断点规则（默认在 request 阶段生效）。
function newBreakpointRule(t: (key: string, params?: Record<string, string | number>) => string): BreakpointRule {
  return { id: crypto.randomUUID(), name: t('tools.breakpoint.defaultRuleName'), enabled: true, match: newMatch(), phases: ['request'] };
}

// newScript — 创建一条默认脚本（带示例 handle 函数骨架，演示 ctx 用法）。
function newScript(t: (key: string, params?: Record<string, string | number>) => string): ScriptEntry {
  return {
    id: crypto.randomUUID(),
    name: t('tools.scripting.newScript'),
    enabled: true,
    match: newMatch('*://*/*'),
    code: [
      // 注释：脚本在每个匹配请求前（phase = "request"）和每个匹配响应前
      //（phase = "response"）执行；ctx 字段说明与修改/中止约定。
      t('tools.scripting.templateComment1'),
      t('tools.scripting.templateComment2'),
      t('tools.scripting.templateComment3'),
      t('tools.scripting.templateComment4'),
      'function handle(ctx) {',
      '  // ctx.headers["X-Test"] = "hello";',
      '  return true;',
      '}',
    ].join('\n'),
    log: [],
  };
}

// newAction — 创建一条默认规则动作（addHeader / request 阶段）。
function newAction(): RuleAction {
  return { type: 'addHeader', headerName: '', headerValue: '', from: '', to: '', phase: 'request' };
}

// newRuleEntry — 创建一条默认的规则条目（含一条默认动作）。
function newRuleEntry(t: (key: string, params?: Record<string, string | number>) => string): RuleToolEntry {
  return { id: crypto.randomUUID(), name: t('tools.rules.newRule'), enabled: true, match: newMatch(), actions: [newAction()] };
}

// NETWORK_PRESETS — 网络条件预设档（仿 Proxyman）：
// 名称 → 下载/上传速率（B/s）、延迟（ms）、丢包率。
const NETWORK_PRESETS: NetworkProfile[] = [
  { name: 'Off', downloadBps: 0, uploadBps: 0, latencyMs: 0, packetLossPct: 0 },
  { name: '56 kbps', downloadBps: 7168, uploadBps: 4224, latencyMs: 120, packetLossPct: 0 },
  { name: '256 kbps', downloadBps: 32768, uploadBps: 16384, latencyMs: 100, packetLossPct: 0 },
  { name: '1 Mbps', downloadBps: 125000, uploadBps: 62500, latencyMs: 60, packetLossPct: 0 },
  { name: '3G', downloadBps: 200000, uploadBps: 96000, latencyMs: 150, packetLossPct: 0 },
  { name: '4G', downloadBps: 1125000, uploadBps: 375000, latencyMs: 40, packetLossPct: 0 },
  { name: 'Fast 4G', downloadBps: 2750000, uploadBps: 1000000, latencyMs: 30, packetLossPct: 0 },
  { name: 'LTE', downloadBps: 5000000, uploadBps: 2500000, latencyMs: 20, packetLossPct: 0 },
  { name: 'Edge', downloadBps: 30000, uploadBps: 25000, latencyMs: 400, packetLossPct: 0 },
  { name: '2G', downloadBps: 6250, uploadBps: 2500, latencyMs: 800, packetLossPct: 0 },
];

/* ------------------------------------------------------------------ */
/* 工具配置页                                                          */
/* ------------------------------------------------------------------ */

// MapLocalPage — "本地映射"工具页：把匹配请求替换为本地文件或内联内容。
// 交互：读取 store.toolConfig 的 mapLocal 分区，所有编辑经 patchSection 即时保存。
function MapLocalPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  // 合并默认值与实际配置，保证渲染不缺字段。
  const sec = { ...FALLBACKS.mapLocal, ...cfg.mapLocal };
  return (
    <div className="tool-page">
      <h2>{t('tools.mapLocal.title')}</h2>
      <div className="subtitle">{t('tools.mapLocal.subtitle')}</div>
      {/* 工具总开关。 */}
      <div className="tool-card">
        <CardHeader
          title={t('tools.mapLocal.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('mapLocal', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      {/* 规则列表，每条的额外字段：类型（文件/内联）、状态码、本地文件或内联正文、额外响应头。 */}
      <RuleListEditor
        rules={sec.rules}
        onChange={(rules) => patchSection('mapLocal', (s) => ({ ...s, rules }))}
        newRule={() => newMapLocalRule(t)}
        renderExtras={(rule, patch) => (
          <div className="tp-col tp-mt8">
            <div className="tp-row">
              {/* 映射类型：File（本地文件）/ Inline（内联内容）。 */}
              <div>
                <span className="tp-label">{t('tools.common.type')}</span>
                <select
                  className="tp-select"
                  value={rule.type}
                  onChange={(e) => patch({ type: e.target.value })}
                >
                  <option value="file">{t('tools.common.file')}</option>
                  <option value="inline">{t('tools.common.inline')}</option>
                </select>
              </div>
              {/* 替换响应的状态码。 */}
              <div>
                <span className="tp-label">{t('tools.common.status')}</span>
                <input
                  type="number"
                  className="input tp-num"
                  value={String(rule.status)}
                  onChange={(e) => patch({ status: parseInt(e.target.value, 10) || 200 })}
                />
              </div>
            </div>
            {/* 按类型展示对应的来源输入：本地文件路径 / 内联响应体。 */}
            {rule.type === 'file' ? (
              <div>
                <span className="tp-label">{t('tools.mapLocal.localFile')}</span>
                <input
                  className="input"
                  style={{ width: '100%' }}
                  value={rule.localFile}
                  placeholder="C:\path\to\file.txt"
                  onChange={(e) => patch({ localFile: e.target.value })}
                />
              </div>
            ) : (
              <div>
                <span className="tp-label">{t('tools.mapLocal.responseBody')}</span>
                <textarea
                  className="tp-textarea"
                  style={{ width: '100%' }}
                  value={rule.body}
                  placeholder={t('tools.mapLocal.responseBodyPlaceholder')}
                  onChange={(e) => patch({ body: e.target.value })}
                />
              </div>
            )}
            {/* 替换响应时附加的响应头。 */}
            <HeadersEditor
              headers={rule.headers}
              onChange={(headers) => patch({ headers })}
              title={t('tools.mapLocal.extraHeaders')}
            />
          </div>
        )}
      />
    </div>
  );
}

// MapRemotePage — "远程映射"工具页：把匹配请求重定向到另一个 URL。
function MapRemotePage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.mapRemote, ...cfg.mapRemote };
  return (
    <div className="tool-page">
      <h2>{t('tools.mapRemote.title')}</h2>
      <div className="subtitle">{t('tools.mapRemote.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.mapRemote.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('mapRemote', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      {/* 规则列表，额外字段：目标 URL。 */}
      <RuleListEditor
        rules={sec.rules}
        onChange={(rules) => patchSection('mapRemote', (s) => ({ ...s, rules }))}
        newRule={() => newMapRemoteRule(t)}
        renderExtras={(rule, patch) => (
          <div className="tp-mt8">
            <span className="tp-label">{t('tools.mapRemote.targetUrl')}</span>
            <input
              className="input"
              style={{ width: '100%' }}
              value={rule.targetUrl}
              placeholder="https://example.com/new/path"
              onChange={(e) => patch({ targetUrl: e.target.value })}
            />
          </div>
        )}
      />
    </div>
  );
}

// BlockListPage — "黑名单"工具页：匹配请求在发出前被直接阻断。
// 另有"Block All Requests"开关：直接阻断全部请求。
function BlockListPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.blockList, ...cfg.blockList };
  return (
    <div className="tool-page">
      <h2>{t('tools.blockList.title')}</h2>
      <div className="subtitle">{t('tools.blockList.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.blockList.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('blockList', (s) => ({ ...s, enabled: v }))}
        />
        {/* 阻断所有请求的快捷开关。 */}
        <div className="tp-row tp-mt8">
          <label className="tp-switch-row">
            <span>{t('tools.blockList.blockAll')}</span>
            <Switch
              checked={sec.blockAll}
              onChange={(v) => patchSection('blockList', (s) => ({ ...s, blockAll: v }))}
            />
          </label>
        </div>
      </div>
      <RuleListEditor
        rules={sec.rules}
        onChange={(rules) => patchSection('blockList', (s) => ({ ...s, rules }))}
        newRule={() => newBlockListRule(t)}
      />
    </div>
  );
}

// AllowListPage — "白名单"工具页：只记录匹配的请求，其余直接放行不记录。
function AllowListPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.allowList, ...cfg.allowList };
  return (
    <div className="tool-page">
      <h2>{t('tools.allowList.title')}</h2>
      <div className="subtitle">{t('tools.allowList.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.allowList.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('allowList', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      <RuleListEditor
        rules={sec.rules}
        onChange={(rules) => patchSection('allowList', (s) => ({ ...s, rules }))}
        newRule={() => newAllowListRule(t)}
      />
    </div>
  );
}

// BreakpointPage — "断点"工具页：匹配请求/响应时暂停以便检视与修改。
// 每条规则可用复选框选择生效阶段（request / response）。
function BreakpointPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.breakpoints, ...cfg.breakpoints };
  return (
    <div className="tool-page">
      <h2>{t('tools.breakpoint.title')}</h2>
      <div className="subtitle">{t('tools.breakpoint.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.breakpoint.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('breakpoints', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      <RuleListEditor
        rules={sec.rules}
        onChange={(rules) => patchSection('breakpoints', (s) => ({ ...s, rules }))}
        newRule={() => newBreakpointRule(t)}
        renderExtras={(rule, patch) => (
          <div className="tp-row tp-mt8">
            {/* 生效阶段复选框：request / response 可多选。 */}
            {(['request', 'response'] as const).map((ph) => (
              <label key={ph} className="tp-switch-row">
                <input
                  type="checkbox"
                  className="tp-check"
                  checked={rule.phases.includes(ph)}
                  onChange={(e) => {
                    // 勾选时追加阶段，取消勾选时移除阶段。
                    const phases = e.target.checked
                      ? [...rule.phases, ph]
                      : rule.phases.filter((p) => p !== ph);
                    patch({ phases });
                  }}
                />
                <span>{ph === 'request' ? t('tools.common.request') : t('tools.common.response')}</span>
              </label>
            ))}
          </div>
        )}
      />
    </div>
  );
}

// ScriptingPage — "脚本"工具页：在匹配请求/响应上运行自定义 JavaScript。
// 每条脚本含启用开关、名称、匹配规则、Monaco 代码编辑器与运行日志。
function ScriptingPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.scripts, ...cfg.scripts };
  return (
    <div className="tool-page">
      <h2>{t('tools.scripting.title')}</h2>
      <div className="subtitle">{t('tools.scripting.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.scripting.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('scripts', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      {sec.scripts.map((entry, idx) => {
        // patch — 更新第 idx 条脚本的字段；patchMatch — 更新其匹配规则。
        const patch = (p: Partial<ScriptEntry>) => {
          const scripts = [...sec.scripts];
          scripts[idx] = { ...entry, ...p };
          patchSection('scripts', (s) => ({ ...s, scripts }));
        };
        const patchMatch = (m: URLMatch) => {
          const scripts = [...sec.scripts];
          scripts[idx] = { ...entry, match: m };
          patchSection('scripts', (s) => ({ ...s, scripts }));
        };
        return (
          <div key={entry.id} className="tool-card">
            <div className="tc-head">
              {/* 启用开关 + 名称 + 删除。 */}
              <Switch checked={entry.enabled} onChange={(v) => patch({ enabled: v })} />
              <input
                className="input tp-grow"
                value={entry.name}
                placeholder={t('tools.scripting.scriptName')}
                onChange={(e) => patch({ name: e.target.value })}
              />
              <div className="tc-actions">
                <button
                  type="button"
                  className="tb-btn"
                  title={t('tools.scripting.deleteScript')}
                  onClick={() =>
                    patchSection('scripts', (s) => ({
                      ...s,
                      scripts: s.scripts.filter((x) => x.id !== entry.id),
                    }))
                  }
                >
                  <Trash2 size={15} />
                </button>
              </div>
            </div>
            <MatchRow match={entry.match} onChange={patchMatch} />
            {/* 脚本代码编辑器（Monaco，JavaScript 高亮）。 */}
            <div className="tp-monaco tp-mt8">
              <Editor
                height={260}
                language="javascript"
                theme="vs-dark"
                value={entry.code}
                onChange={(v) => patch({ code: v ?? '' })}
              />
            </div>
            {/* 脚本运行日志（黄色警示样式）。 */}
            {entry.log && entry.log.length > 0 && (
              <div className="tp-log tp-mt8">{entry.log.join('\n')}</div>
            )}
          </div>
        );
      })}
      {/* 新增脚本按钮。 */}
      <button
        type="button"
        className="btn"
        onClick={() => patchSection('scripts', (s) => ({ ...s, scripts: [...s.scripts, newScript(t)] }))}
      >
        <Plus size={14} /> {t('tools.scripting.newScript')}
      </button>
    </div>
  );
}

// NetworkConditionsPage — "网络条件"工具页：模拟网络速率与延迟。
// 支持预设档（Off/3G/4G/LTE 等）与手动自定义（自定义时名称显示为 Custom）。
function NetworkConditionsPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  // 合并默认值与实际配置（profile 需逐层合并）。
  const sec = {
    ...FALLBACKS.networkConditions,
    ...cfg.networkConditions,
    profile: {
      ...FALLBACKS.networkConditions.profile,
      ...(cfg.networkConditions?.profile ?? {}),
    },
  };
  // 当前选中的预设名；非预设值（如手动修改过）显示为 'custom'。
  const presetNames = NETWORK_PRESETS.map((p) => p.name);
  const selected = presetNames.includes(sec.profile.name) ? sec.profile.name : 'custom';

  // patchNumber — 编辑数值字段（下载/上传/延迟）：空串视为 0，
  // 非数值视为 0，修改后名称置为 'Custom'。
  const patchNumber = (field: 'downloadBps' | 'uploadBps' | 'latencyMs', raw: string) => {
    const n = raw === '' ? 0 : Number(raw);
    patchSection('networkConditions', (s) => ({
      ...s,
      profile: { ...s.profile, [field]: Number.isFinite(n) ? n : 0, name: 'Custom' },
    }));
  };

  return (
    <div className="tool-page">
      <h2>{t('tools.networkConditions.title')}</h2>
      <div className="subtitle">{t('tools.networkConditions.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.networkConditions.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('networkConditions', (s) => ({ ...s, enabled: v }))}
        />
        <div className="tp-col tp-mt8">
          {/* 预设档下拉：选预设则应用其参数（保留当前丢包率），选 Custom 则仅改名。 */}
          <div>
            <span className="tp-label">{t('tools.networkConditions.preset')}</span>
            <select
              className="tp-select"
              value={selected}
              onChange={(e) => {
                const name = e.target.value;
                if (name === 'custom') {
                  patchSection('networkConditions', (s) => ({
                    ...s,
                    profile: { ...s.profile, name: 'Custom' },
                  }));
                } else {
                  const preset = NETWORK_PRESETS.find((p) => p.name === name);
                  if (preset) {
                    patchSection('networkConditions', (s) => ({
                      ...s,
                      profile: { ...preset, packetLossPct: s.profile.packetLossPct },
                    }));
                  }
                }
              }}
            >
              {NETWORK_PRESETS.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.name}
                </option>
              ))}
              <option value="custom">{t('tools.networkConditions.custom')}</option>
            </select>
          </div>
          {/* 手动数值输入：下载/上传速率（B/s）与延迟（ms）。 */}
          <div className="tp-row" style={{ flexWrap: 'wrap' }}>
            <div>
              <span className="tp-label">{t('tools.networkConditions.download')}</span>
              <input
                type="number"
                className="input tp-num"
                value={String(sec.profile.downloadBps)}
                onChange={(e) => patchNumber('downloadBps', e.target.value)}
              />
            </div>
            <div>
              <span className="tp-label">{t('tools.networkConditions.upload')}</span>
              <input
                type="number"
                className="input tp-num"
                value={String(sec.profile.uploadBps)}
                onChange={(e) => patchNumber('uploadBps', e.target.value)}
              />
            </div>
            <div>
              <span className="tp-label">{t('tools.networkConditions.latency')}</span>
              <input
                type="number"
                className="input tp-num"
                value={String(sec.profile.latencyMs)}
                onChange={(e) => patchNumber('latencyMs', e.target.value)}
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// NoCachingPage — "禁用缓存"工具页：移除请求/响应中的缓存相关头
//（Cache-Control、ETag、If-Modified-Since 等）。
function NoCachingPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const enabled = cfg.noCaching ?? FALLBACKS.noCaching;
  return (
    <div className="tool-page">
      <h2>{t('tools.noCaching.title')}</h2>
      <div className="subtitle">{t('tools.noCaching.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.noCaching.title')}
          checked={enabled}
          onChange={(v) => patchSection('noCaching', () => v)}
        />
      </div>
    </div>
  );
}

// RulesPage — "规则"工具页：重写请求/响应（头、正文、重定向）。
// 每条规则可包含多个动作；动作类型：addHeader / removeHeader / replaceHeader /
// replaceBody / redirect，并可在 request / response / both 阶段生效。
function RulesPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.rules, ...cfg.rules };
  return (
    <div className="tool-page">
      <h2>{t('tools.rules.title')}</h2>
      <div className="subtitle">{t('tools.rules.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.rules.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('rules', (s) => ({ ...s, enabled: v }))}
        />
      </div>
      {sec.rules.map((rule, idx) => {
        // patch / patchMatch / patchAction — 分别更新规则字段、匹配规则、第 ai 条动作。
        const patch = (p: Partial<RuleToolEntry>) => {
          const rules = [...sec.rules];
          rules[idx] = { ...rule, ...p };
          patchSection('rules', (s) => ({ ...s, rules }));
        };
        const patchMatch = (m: URLMatch) => {
          const rules = [...sec.rules];
          rules[idx] = { ...rule, match: m };
          patchSection('rules', (s) => ({ ...s, rules }));
        };
        const patchAction = (ai: number, p: Partial<RuleAction>) => {
          const rules = [...sec.rules];
          const actions = [...rule.actions];
          actions[ai] = { ...actions[ai], ...p };
          rules[idx] = { ...rule, actions };
          patchSection('rules', (s) => ({ ...s, rules }));
        };
        return (
          <div key={rule.id} className="tool-card">
            <div className="tc-head">
              {/* 启用开关 + 名称 + 删除规则。 */}
              <Switch checked={rule.enabled} onChange={(v) => patch({ enabled: v })} />
              <input
                className="input tp-grow"
                value={rule.name}
                placeholder={t('tools.common.ruleName')}
                onChange={(e) => patch({ name: e.target.value })}
              />
              <div className="tc-actions">
                <button
                  type="button"
                  className="tb-btn"
                  title={t('tools.common.deleteRule')}
                  onClick={() =>
                    patchSection('rules', (s) => ({
                      ...s,
                      rules: s.rules.filter((r) => r.id !== rule.id),
                    }))
                  }
                >
                  <Trash2 size={15} />
                </button>
              </div>
            </div>
            <MatchRow match={rule.match} onChange={patchMatch} />
            {/* 动作列表。 */}
            <div className="tp-col tp-mt8">
              <span className="tp-label">{t('tools.rules.actions')}</span>
              {rule.actions.map((action, ai) => (
                <div key={ai} className="tool-card" style={{ marginBottom: 6 }}>
                  <div className="tp-col">
                    <div className="tp-row" style={{ flexWrap: 'wrap' }}>
                      {/* 动作类型下拉。 */}
                      <select
                        className="tp-select"
                        value={action.type}
                        onChange={(e) => patchAction(ai, { type: e.target.value as RuleAction['type'] })}
                      >
                        <option value="addHeader">{t('tools.rules.actionAddHeader')}</option>
                        <option value="removeHeader">{t('tools.rules.actionRemoveHeader')}</option>
                        <option value="replaceHeader">{t('tools.rules.actionReplaceHeader')}</option>
                        <option value="replaceBody">{t('tools.rules.actionReplaceBody')}</option>
                        <option value="redirect">{t('tools.rules.actionRedirect')}</option>
                      </select>
                      {/* 生效阶段下拉：request / response / both。 */}
                      <select
                        className="tp-select"
                        value={action.phase}
                        onChange={(e) => patchAction(ai, { phase: e.target.value })}
                      >
                        <option value="request">{t('tools.common.request')}</option>
                        <option value="response">{t('tools.common.response')}</option>
                        <option value="both">{t('tools.rules.both')}</option>
                      </select>
                      {/* 删除动作。 */}
                      <div className="tc-actions" style={{ marginLeft: 'auto' }}>
                        <button
                          type="button"
                          className="tb-btn"
                          title={t('tools.common.deleteAction')}
                          onClick={() =>
                            patchSection('rules', (s) => ({
                              ...s,
                              rules: s.rules.map((r, ri) =>
                                ri === idx ? { ...r, actions: r.actions.filter((_, aj) => aj !== ai) } : r,
                              ),
                            }))
                          }
                        >
                          <X size={14} />
                        </button>
                      </div>
                    </div>
                    {/* 动作参数按类型区分：增/改头需要名称+值。 */}
                    {(action.type === 'addHeader' || action.type === 'replaceHeader') && (
                      <div className="tp-row">
                        <input
                          className="input tp-grow"
                          value={action.headerName}
                          placeholder={t('tools.rules.headerName')}
                          onChange={(e) => patchAction(ai, { headerName: e.target.value })}
                        />
                        <input
                          className="input tp-grow"
                          value={action.headerValue}
                          placeholder={t('tools.rules.headerValue')}
                          onChange={(e) => patchAction(ai, { headerValue: e.target.value })}
                        />
                      </div>
                    )}
                    {/* 删头只需名称。 */}
                    {action.type === 'removeHeader' && (
                      <input
                        className="input"
                        value={action.headerName}
                        placeholder={t('tools.rules.headerName')}
                        onChange={(e) => patchAction(ai, { headerName: e.target.value })}
                      />
                    )}
                    {/* 替换正文需要 from → to（支持字符串或正则）。 */}
                    {action.type === 'replaceBody' && (
                      <div className="tp-row">
                        <input
                          className="input tp-grow"
                          value={action.from}
                          placeholder={t('tools.rules.fromPlaceholder')}
                          onChange={(e) => patchAction(ai, { from: e.target.value })}
                        />
                        <input
                          className="input tp-grow"
                          value={action.to}
                          placeholder={t('tools.rules.toPlaceholder')}
                          onChange={(e) => patchAction(ai, { to: e.target.value })}
                        />
                      </div>
                    )}
                    {/* 重定向只需目标 URL。 */}
                    {action.type === 'redirect' && (
                      <input
                        className="input"
                        value={action.to}
                        placeholder={t('tools.mapRemote.targetUrl')}
                        onChange={(e) => patchAction(ai, { to: e.target.value })}
                      />
                    )}
                  </div>
                </div>
              ))}
              {/* 为当前规则追加一个动作。 */}
              <div>
                <button
                  type="button"
                  className="btn"
                  onClick={() =>
                    patchSection('rules', (s) => ({
                      ...s,
                      rules: s.rules.map((r, ri) =>
                        ri === idx ? { ...r, actions: [...r.actions, newAction()] } : r,
                      ),
                    }))
                  }
                >
                  <Plus size={14} /> {t('tools.common.addAction')}
                </button>
              </div>
            </div>
          </div>
        );
      })}
      {/* 新增规则。 */}
      <button
        type="button"
        className="btn"
        onClick={() => patchSection('rules', (s) => ({ ...s, rules: [...s.rules, newRuleEntry(t)] }))}
      >
        <Plus size={14} /> {t('tools.common.addRule')}
      </button>
    </div>
  );
}

// ExternalProxyPage — "外部代理"工具页：把流量路由到上游 HTTP 代理。
// 配置：类型（仅 HTTP）、主机、端口、认证（用户名/密码）、旁路域名列表。
function ExternalProxyPage() {
  const cfg = useApp((s) => s.toolConfig);
  const { t } = useI18n();
  if (!cfg) return null;
  const sec = { ...FALLBACKS.externalProxy, ...cfg.externalProxy };
  return (
    <div className="tool-page">
      <h2>{t('tools.externalProxy.title')}</h2>
      <div className="subtitle">{t('tools.externalProxy.subtitle')}</div>
      <div className="tool-card">
        <CardHeader
          title={t('tools.externalProxy.title')}
          checked={sec.enabled}
          onChange={(v) => patchSection('externalProxy', (s) => ({ ...s, enabled: v }))}
        />
        <div className="tp-col tp-mt8">
          <div className="tp-row">
            {/* 代理类型（目前仅支持 HTTP）。 */}
            <div>
              <span className="tp-label">{t('tools.common.type')}</span>
              <select
                className="tp-select"
                value={sec.type}
                onChange={(e) => patchSection('externalProxy', (s) => ({ ...s, type: e.target.value }))}
              >
                <option value="http">HTTP</option>
              </select>
            </div>
            {/* 代理主机。 */}
            <div className="tp-grow">
              <span className="tp-label">{t('tools.common.host')}</span>
              <input
                className="input"
                style={{ width: '100%' }}
                value={sec.host}
                placeholder="proxy.example.com"
                onChange={(e) => patchSection('externalProxy', (s) => ({ ...s, host: e.target.value }))}
              />
            </div>
            {/* 代理端口。 */}
            <div>
              <span className="tp-label">{t('tools.common.port')}</span>
              <input
                type="number"
                className="input tp-num"
                value={String(sec.port)}
                onChange={(e) =>
                  patchSection('externalProxy', (s) => ({
                    ...s,
                    port: parseInt(e.target.value, 10) || 0,
                  }))
                }
              />
            </div>
          </div>
          {/* 认证：用户名 + 密码（密码框隐藏明文）。 */}
          <div className="tp-row">
            <div className="tp-grow">
              <span className="tp-label">{t('tools.common.username')}</span>
              <input
                className="input"
                style={{ width: '100%' }}
                value={sec.username}
                onChange={(e) => patchSection('externalProxy', (s) => ({ ...s, username: e.target.value }))}
              />
            </div>
            <div className="tp-grow">
              <span className="tp-label">{t('tools.common.password')}</span>
              <input
                type="password"
                className="input"
                style={{ width: '100%' }}
                value={sec.password}
                onChange={(e) => patchSection('externalProxy', (s) => ({ ...s, password: e.target.value }))}
              />
            </div>
          </div>
          {/* 旁路域名（逗号分隔）：这些域名不走外部代理。 */}
          <div>
            <span className="tp-label">{t('tools.externalProxy.bypassDomains')}</span>
            <textarea
              className="tp-textarea"
              style={{ width: '100%' }}
              value={sec.bypassDomains.join(', ')}
              placeholder="example.com, *.internal.local"
              onChange={(e) =>
                patchSection('externalProxy', (s) => ({
                  ...s,
                  bypassDomains: e.target.value
                    .split(',')
                    .map((d) => d.trim())
                    .filter(Boolean),
                }))
              }
            />
          </div>
        </div>
      </div>
    </div>
  );
}

// COMPOSE_METHODS — Compose 工具支持选择的 HTTP 方法列表。
const COMPOSE_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'];

// parseUrl — 解析用户输入的 URL 文本为请求所需的各部分。
// 无协议前缀时按 http:// 处理（host 留空，交给后续 host 字段补全）。
// 参数：raw — 原始 URL 文本；返回值：{ scheme, host, path, query }。
function parseUrl(raw: string): { scheme: string; host: string; path: string; query: string } {
  const trimmed = raw.trim();
  // 缺少 http:// 或 https:// 前缀：视为 origin-form 路径。
  if (!/^https?:\/\//i.test(trimmed)) {
    return { scheme: 'http', host: '', path: trimmed, query: '' };
  }
  try {
    const u = new URL(trimmed);
    return {
      scheme: u.protocol.replace(':', ''),
      host: u.host,
      path: u.pathname,
      query: u.search.replace(/^\?/, ''),
    };
  } catch {
    // URL 解析失败：同样按纯路径处理。
    return { scheme: 'http', host: '', path: trimmed, query: '' };
  }
}

// toBase64 — 将 UTF-8 文本编码为 base64（优先 UTF-8 编码，失败时按 Latin-1）。
function toBase64(text: string): string {
  try {
    return btoa(unescape(encodeURIComponent(text)));
  } catch {
    return btoa(text);
  }
}

// buildFlow — 由用户输入构造一条待发送的 Flow（发送前结构）。
// 参数：method（方法）、url（URL）、headers（头列表）、body（正文）、version（HTTP 版本，可选）。
// 返回值：完整的 Flow 对象（响应字段为空，isWebSocket=false）。
function buildFlow(input: {
  method: string;
  url: string;
  headers: Header[];
  body: string;
  version?: string;
}): Flow {
  const parsed = parseUrl(input.url);
  return {
    id: crypto.randomUUID(),
    scheme: parsed.scheme,
    method: input.method,
    host: parsed.host,
    path: parsed.path,
    query: parsed.query,
    fullUrl: input.url,
    httpVersion: input.version ?? 'HTTP/1.1',
    tls: parsed.scheme === 'https',
    requestHeaders: input.headers,
    requestBody: toBase64(input.body),
    requestSize: 0,
    requestMimeType: '',
    responseStatus: 0,
    responseReason: '',
    responseHeaders: [],
    responseBody: '',
    responseSize: 0,
    responseMimeType: '',
    startedAt: 0,
    completedAt: 0,
    duration: 0,
    isWebSocket: false,
  };
}

// ComposePage — "构造请求"工具页：从零构建并发送一条原始 HTTP 请求。
// 表单：方法下拉 + URL 输入 + 请求头编辑 + 正文，发送后展示响应。
function ComposePage() {
  // 表单本地状态：方法、URL、头列表、正文、发送结果、错误与忙碌标记。
  const { t } = useI18n();
  const [method, setMethod] = useState('GET');
  const [url, setUrl] = useState('');
  const [headers, setHeaders] = useState<Header[]>([{ name: '', value: '' }]);
  const [body, setBody] = useState('');
  const [result, setResult] = useState<Flow | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  // send — 组装 Flow 并调用后端发送；发送前过滤掉名称为空的头行。
  const send = async () => {
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const res = await api.sendRequest(
        buildFlow({ method, url, headers: headers.filter((h) => h.name.trim() !== ''), body }),
      );
      setResult(res);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="tool-page">
      <h2>{t('tools.compose.title')}</h2>
      <div className="subtitle">{t('tools.compose.subtitle')}</div>
      <div className="tool-card">
        <div className="tp-col">
          {/* 方法 / URL / 发送按钮。 */}
          <div className="tp-row">
            <select className="tp-select" value={method} onChange={(e) => setMethod(e.target.value)}>
              {COMPOSE_METHODS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
            <input
              className="input tp-grow"
              value={url}
              placeholder="https://example.com/api"
              onChange={(e) => setUrl(e.target.value)}
            />
            <button type="button" className="btn primary" disabled={loading} onClick={send}>
              <Send size={14} /> {t('tools.common.send')}
            </button>
          </div>
          {/* 请求头编辑。 */}
          <HeadersEditor headers={headers} onChange={setHeaders} title={t('tools.common.requestHeaders')} />
          {/* 请求正文。 */}
          <div>
            <span className="tp-label">{t('tools.common.body')}</span>
            <textarea
              className="tp-textarea"
              style={{ width: '100%', minHeight: 120 }}
              value={body}
              placeholder={t('tools.compose.bodyPlaceholder')}
              onChange={(e) => setBody(e.target.value)}
            />
          </div>
        </div>
      </div>
      {/* 响应结果展示。 */}
      <ResponseView flow={result} error={error} loading={loading} />
    </div>
  );
}

// parseRawRequest — 解析原始 HTTP 请求报文文本（Repeater 使用）。
// 支持：首行 "METHOD path HTTP/version"（版本可省略）、
// 后续头行 "Name: value"、空行后为请求体。
// 返回值：解析结果对象；首行不合法时返回 null。
function parseRawRequest(
  text: string,
): { method: string; path: string; version: string; headers: Header[]; body: string } | null {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const first = lines.shift() ?? '';
  // 解析请求行：方法 + 路径（绝对或 origin-form）+ 可选 HTTP 版本。
  const m = first.match(/^(\S+)\s+(\S+)(?:\s+(HTTP\/\S+))?/);
  if (!m) return null;
  const headers: Header[] = [];
  let i = 0;
  // 逐行解析头，直到空行（空行即头的结束与体的开始）。
  for (; i < lines.length; i++) {
    const line = lines[i];
    if (line.trim() === '') {
      i++;
      break;
    }
    const hm = line.match(/^([^:]+):\s*(.*)$/);
    if (hm) headers.push({ name: hm[1].trim(), value: hm[2].trim() });
  }
  // 剩余行即为请求体。
  return { method: m[1], path: m[2], version: m[3] ?? 'HTTP/1.1', headers, body: lines.slice(i).join('\n') };
}

// buildUrl — 由主机与路径构造完整 URL。
// 路径本身已含协议前缀时直接返回；否则以 http:// 前缀拼接（缺失 host 时回退 localhost）。
function buildUrl(hostInput: string, path: string): string {
  if (/^https?:\/\//i.test(path.trim())) return path.trim();
  const host = hostInput.trim() || 'localhost';
  const p = path.trim().startsWith('/') ? path.trim() : '/' + path.trim();
  return `http://${host}${p}`;
}

// RepeaterPage — "重放"工具页：粘贴原始请求报文并重放发送。
// 支持从流量列表右键 "Copy cURL" 获得文本后在此发送。
function RepeaterPage() {
  // 原始报文文本、可选主机、发送结果、错误与忙碌标记。
  const { t } = useI18n();
  const [raw, setRaw] = useState('');
  const [host, setHost] = useState('');
  const [result, setResult] = useState<Flow | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  // send — 解析原始报文，失败则提示格式错误；成功后构造 Flow 发送。
  const send = async () => {
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const parsed = parseRawRequest(raw);
      if (!parsed) {
        setError(t('tools.repeater.parseError'));
        setLoading(false);
        return;
      }
      const res = await api.sendRequest(
        buildFlow({
          method: parsed.method,
          url: buildUrl(host, parsed.path),
          headers: parsed.headers.filter((h) => h.name.trim() !== ''),
          body: parsed.body,
          version: parsed.version,
        }),
      );
      setResult(res);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="tool-page">
      <h2>{t('tools.repeater.title')}</h2>
      <div className="subtitle">{t('tools.repeater.subtitle')}</div>
      <div className="tool-card">
        <div className="tp-col">
          {/* 主机输入（为 origin-form 路径补全用，可留空）+ 发送按钮。 */}
          <div className="tp-row">
            <div className="tp-grow">
              <span className="tp-label">{t('tools.repeater.hostLabel')}</span>
              <input
                className="input"
                style={{ width: '100%' }}
                value={host}
                placeholder="api.example.com"
                onChange={(e) => setHost(e.target.value)}
              />
            </div>
            <div style={{ alignSelf: 'flex-end' }}>
              <button type="button" className="btn primary" disabled={loading} onClick={send}>
                <Send size={14} /> {t('tools.common.send')}
              </button>
            </div>
          </div>
          {/* 原始请求报文输入区。 */}
          <div>
            <span className="tp-label">{t('tools.repeater.rawRequest')}</span>
            <textarea
              className="tp-textarea"
              style={{ width: '100%', minHeight: 180 }}
              value={raw}
              placeholder={'GET /api/users HTTP/1.1\nHost: example.com\nAccept: application/json\n\n'}
              onChange={(e) => setRaw(e.target.value)}
            />
          </div>
        </div>
      </div>
      {/* 响应结果展示。 */}
      <ResponseView flow={result} error={error} loading={loading} />
    </div>
  );
}

// DiffPage — "对比"工具页（占位）：规划中用于对比两条请求。
function DiffPage() {
  const { t } = useI18n();
  return (
    <div className="tool-page">
      <h2>{t('tools.diff.title')}</h2>
      <div className="subtitle">{t('tools.diff.subtitle')}</div>
      <div className="tool-card">
        <div className="tp-empty">{t('tools.diff.empty')}</div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* 入口点                                                              */
/* ------------------------------------------------------------------ */

// ToolsPanel — 工具面板入口组件：按 view 参数分发到对应工具配置页。
// 参数：view — 当前工具视图 id（'tools:maplocal' 等，来自 store.activeView）。
// 返回值：注入局部样式 + 对应的工具页面；配置未加载时显示加载提示。
export default function ToolsPanel({ view }: { view: string }) {
  const toolConfig = useApp((s) => s.toolConfig);
  const { t } = useI18n();

  let page: ReactNode;
  if (!toolConfig) {
    // 工具配置尚未加载（App.init 未完成）时的占位提示。
    page = (
      <div className="tool-page">
        <div className="subtitle">{t('tools.common.loadingConfig')}</div>
      </div>
    );
  } else {
    // 按视图 id 分发到各工具配置页；未知 id 显示"未知工具"提示。
    switch (view) {
      case 'tools:maplocal':
        page = <MapLocalPage />;
        break;
      case 'tools:mapremote':
        page = <MapRemotePage />;
        break;
      case 'tools:blocklist':
        page = <BlockListPage />;
        break;
      case 'tools:allowlist':
        page = <AllowListPage />;
        break;
      case 'tools:breakpoint':
        page = <BreakpointPage />;
        break;
      case 'tools:scripting':
        page = <ScriptingPage />;
        break;
      case 'tools:networkconditions':
        page = <NetworkConditionsPage />;
        break;
      case 'tools:nocaching':
        page = <NoCachingPage />;
        break;
      case 'tools:rules':
        page = <RulesPage />;
        break;
      case 'tools:externalproxy':
        page = <ExternalProxyPage />;
        break;
      case 'tools:compose':
        page = <ComposePage />;
        break;
      case 'tools:repeater':
        page = <RepeaterPage />;
        break;
      case 'tools:diff':
        page = <DiffPage />;
        break;
      default:
        page = (
          <div className="tool-page">
            <div className="subtitle">{t('tools.common.unknownTool')}</div>
          </div>
        );
    }
  }

  return (
    <>
      {/* 注入本组件局部样式。 */}
      <PanelStyles />
      {page}
    </>
  );
}
