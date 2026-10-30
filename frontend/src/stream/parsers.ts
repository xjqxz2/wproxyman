// parsers.ts — 把流式事件还原成"完整的一次回复"。
//
// 设计取舍：解析放在前端而不是后端。
//   1. 自定义规则本来就要用户可编辑，放前端才能改完即生效；
//   2. 新增私有协议只需加一条规则，不必改 Go 代码重新发版；
//   3. 原始事件已存在 flow / 会话文件里，解析结果随时可重新推导，不丢信息。
//
// 识别方式是**按报文结构嗅探**，不是按域名白名单——所以 vLLM / Ollama /
// one-api 这类自建服务、以及任何走 OpenAI 协议的网关都能正确识别。

import { SSEMessage, StreamRule } from '../types';
import { jsonPathGet, parseJson } from './jsonpath';

export type StreamProvider = 'openai' | 'openai-responses' | 'anthropic' | 'custom' | 'raw';

export interface StreamToolCall {
  id?: string;
  name?: string;
  // args 是拼起来的 JSON 字符串（流式协议里参数是分片下发的）
  args: string;
  index?: number;
}

export interface StreamUsage {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

export interface AccumulatedStream {
  provider: StreamProvider;
  text: string;
  reasoning: string;
  toolCalls: StreamToolCall[];
  usage?: StreamUsage;
  model?: string;
  finishReason?: string;
  stopReason?: string;
  // 统计：事件总数、可解析为 JSON 的数量、JSON 解析失败数。
  eventCount: number;
  jsonEvents: number;
  parseErrors: number;
  // 提示信息：只带 i18n key（+ 可选参数），由界面翻译后展示。
  notes: StreamNote[];
  // 自定义规则名（provider === 'custom' 时展示）。
  ruleName?: string;
}

// StreamNote 是一条解析提示（i18n key + 可选规则名）。
export interface StreamNote {
  key: string;
  name?: string;
}

function emptyResult(provider: StreamProvider): AccumulatedStream {
  return {
    provider,
    text: '',
    reasoning: '',
    toolCalls: [],
    eventCount: 0,
    jsonEvents: 0,
    parseErrors: 0,
    notes: [],
  };
}

function asString(v: unknown): string {
  if (typeof v === 'string') return v;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  return '';
}

function asNumber(v: unknown): number | undefined {
  if (typeof v === 'number') return v;
  if (typeof v === 'string' && v.trim() !== '' && !Number.isNaN(Number(v))) return Number(v);
  return undefined;
}

// --- OpenAI chat.completions ---

function looksLikeOpenAIChat(parsed: unknown[]): boolean {
  return parsed.some((p) => {
    const choices = jsonPathGet(p, '$.choices');
    return Array.isArray(choices) && choices.length > 0;
  });
}

function parseOpenAIChat(events: SSEMessage[], res: AccumulatedStream): void {
  // tool_calls 分片按 index 归并：id/name 通常只在第一片里，
  // arguments 是多片拼接的 JSON 字符串。
  const byIndex = new Map<number, StreamToolCall>();
  const order: number[] = [];

  for (const ev of events) {
    const p = parseJson(ev.data);
    if (p === undefined) continue;
    const model = jsonPathGet(p, '$.model');
    if (typeof model === 'string' && model) res.model = model;

    const usage = jsonPathGet(p, '$.usage');
    if (usage && typeof usage === 'object') {
      const u = usage as Record<string, unknown>;
      res.usage = {
        inputTokens: asNumber(u.prompt_tokens) ?? res.usage?.inputTokens,
        outputTokens: asNumber(u.completion_tokens) ?? res.usage?.outputTokens,
        totalTokens: asNumber(u.total_tokens) ?? res.usage?.totalTokens,
      };
    }

    const delta = jsonPathGet(p, '$.choices[0].delta');
    if (delta && typeof delta === 'object') {
      const d = delta as Record<string, unknown>;
      const content = asString(d.content);
      if (content) res.text += content;
      // DeepSeek / 部分推理模型把思维链放在 reasoning_content（也有叫 reasoning 的）
      const reasoning = asString(d.reasoning_content) || asString(d.reasoning);
      if (reasoning) res.reasoning += reasoning;

      const calls = d.tool_calls;
      if (Array.isArray(calls)) {
        for (const c of calls) {
          if (!c || typeof c !== 'object') continue;
          const call = c as Record<string, unknown>;
          const idx = asNumber(call.index) ?? 0;
          let entry = byIndex.get(idx);
          if (!entry) {
            entry = { args: '', index: idx };
            byIndex.set(idx, entry);
            order.push(idx);
          }
          const id = asString(call.id);
          if (id) entry.id = id;
          const fn = call.function as Record<string, unknown> | undefined;
          if (fn) {
            const name = asString(fn.name);
            if (name) entry.name = name;
            const args = asString(fn.arguments);
            if (args) entry.args += args;
          }
        }
      }
    }

    const finish = asString(jsonPathGet(p, '$.choices[0].finish_reason'));
    if (finish) res.finishReason = finish;
  }
  res.toolCalls = order.map((i) => byIndex.get(i)!);
}

// --- OpenAI responses API（语义事件流）---

const RESPONSES_EVENTS = new Set([
  'response.created',
  'response.in_progress',
  'response.output_item.added',
  'response.output_item.done',
  'response.content_part.added',
  'response.content_part.done',
  'response.output_text.delta',
  'response.output_text.done',
  'response.reasoning_summary_text.delta',
  'response.reasoning_summary_text.done',
  'response.function_call_arguments.delta',
  'response.completed',
  'response.failed',
]);

function looksLikeResponses(events: SSEMessage[]): boolean {
  return events.some((ev) => RESPONSES_EVENTS.has(ev.event ?? ''));
}

function parseResponses(events: SSEMessage[], res: AccumulatedStream): void {
  for (const ev of events) {
    const p = parseJson(ev.data);
    if (p === undefined) continue;
    switch (ev.event) {
      case 'response.output_text.delta':
        res.text += asString(jsonPathGet(p, '$.delta'));
        break;
      case 'response.reasoning_summary_text.delta':
        res.reasoning += asString(jsonPathGet(p, '$.delta'));
        break;
      case 'response.function_call_arguments.delta': {
        const name = asString(jsonPathGet(p, '$.name'));
        const callId = asString(jsonPathGet(p, '$.item_id'));
        let entry = res.toolCalls.find((c) => c.id && c.id === callId);
        if (!entry) {
          entry = { id: callId || undefined, name: name || undefined, args: '' };
          res.toolCalls.push(entry);
        }
        entry.args += asString(jsonPathGet(p, '$.delta'));
        break;
      }
      case 'response.completed': {
        const model = asString(jsonPathGet(p, '$.response.model'));
        if (model) res.model = model;
        const u = jsonPathGet(p, '$.response.usage');
        if (u && typeof u === 'object') {
          const usage = u as Record<string, unknown>;
          res.usage = {
            inputTokens: asNumber(usage.input_tokens),
            outputTokens: asNumber(usage.output_tokens),
            totalTokens: asNumber(usage.total_tokens),
          };
        }
        break;
      }
      default:
        break;
    }
  }
}

// --- Anthropic messages ---

const ANTHROPIC_EVENTS = new Set([
  'message_start',
  'content_block_start',
  'content_block_delta',
  'content_block_stop',
  'message_delta',
  'message_stop',
  'ping',
]);

function looksLikeAnthropic(events: SSEMessage[]): boolean {
  return events.some(
    (ev) => ANTHROPIC_EVENTS.has(ev.event ?? '') || asString(jsonPathGet(parseJson(ev.data), '$.type')) === 'content_block_delta',
  );
}

function parseAnthropic(events: SSEMessage[], res: AccumulatedStream): void {
  // 正在累积的 tool_use 块：input_json_delta 分片要拼到对应的块上。
  let currentTool: StreamToolCall | null = null;

  for (const ev of events) {
    const p = parseJson(ev.data);
    const type = ev.event === 'message' ? asString(jsonPathGet(p, '$.type')) : ev.event;

    switch (type) {
      case 'message_start': {
        const model = asString(jsonPathGet(p, '$.message.model'));
        if (model) res.model = model;
        const inTok = asNumber(jsonPathGet(p, '$.message.usage.input_tokens'));
        if (inTok !== undefined) res.usage = { ...(res.usage ?? {}), inputTokens: inTok };
        break;
      }
      case 'content_block_start': {
        const block = jsonPathGet(p, '$.content_block');
        if (block && typeof block === 'object') {
          const b = block as Record<string, unknown>;
          if (b.type === 'tool_use') {
            currentTool = { id: asString(b.id) || undefined, name: asString(b.name) || undefined, args: '' };
            res.toolCalls.push(currentTool);
          } else {
            currentTool = null;
          }
        }
        break;
      }
      case 'content_block_delta': {
        const delta = jsonPathGet(p, '$.delta');
        if (delta && typeof delta === 'object') {
          const d = delta as Record<string, unknown>;
          switch (d.type) {
            case 'text_delta':
              res.text += asString(d.text);
              break;
            case 'thinking_delta':
              res.reasoning += asString(d.thinking);
              break;
            case 'input_json_delta':
              if (currentTool) currentTool.args += asString(d.partial_json);
              break;
            default:
              break;
          }
        }
        break;
      }
      case 'content_block_stop':
        currentTool = null;
        break;
      case 'message_delta': {
        const stop = asString(jsonPathGet(p, '$.delta.stop_reason'));
        if (stop) res.stopReason = stop;
        const outTok = asNumber(jsonPathGet(p, '$.usage.output_tokens'));
        if (outTok !== undefined) res.usage = { ...(res.usage ?? {}), outputTokens: outTok };
        break;
      }
      default:
        break;
    }
  }
}

// --- 自定义规则（非标协议）---

function parseWithRule(events: SSEMessage[], rule: StreamRule, res: AccumulatedStream): boolean {
  let matched = false;
  for (const ev of events) {
    if (rule.stopValue && ev.data.trim() === rule.stopValue.trim()) break;
    const p = parseJson(ev.data);
    if (p === undefined) continue;
    if (rule.matchPath && jsonPathGet(p, rule.matchPath) === undefined) continue;
    const delta = jsonPathGet(p, rule.deltaPath);
    if (delta === undefined || delta === null) continue;
    const text = asString(delta);
    if (!text) continue;
    matched = true;
    res.text += text;
  }
  return matched;
}

// --- 入口 ---

// accumulateStream 把一条流的事件还原成累积内容。
// rules 是用户在设置里配置的非标协议规则（可为空）。
export function accumulateStream(events: SSEMessage[], rules: StreamRule[] = []): AccumulatedStream {
  const contentEvents = events.filter((e) => !e.comment);
  const parsed: unknown[] = [];
  let jsonEvents = 0;
  let parseErrors = 0;

  for (const ev of contentEvents) {
    const s = ev.data.trim();
    if (!s) continue;
    if (s === '[DONE]') continue;
    const p = parseJson(ev.data);
    if (p === undefined) {
      // data 不是 JSON：SSE 里很常见（纯文本流），不算错误。
      if (s.startsWith('{') || s.startsWith('[')) parseErrors++;
      continue;
    }
    jsonEvents++;
    parsed.push(p);
  }

  const base = (provider: StreamProvider): AccumulatedStream => {
    const r = emptyResult(provider);
    r.eventCount = events.length;
    r.jsonEvents = jsonEvents;
    r.parseErrors = parseErrors;
    return r;
  };

  // 1) 内置预设：按结构嗅探，不认域名。
  if (looksLikeResponses(contentEvents)) {
    const r = base('openai-responses');
    parseResponses(contentEvents, r);
    if (r.text || r.reasoning || r.toolCalls.length) return r;
  }

  if (looksLikeOpenAIChat(parsed)) {
    const r = base('openai');
    parseOpenAIChat(contentEvents, r);
    if (r.text || r.reasoning || r.toolCalls.length) return r;
  }

  if (looksLikeAnthropic(contentEvents)) {
    const r = base('anthropic');
    parseAnthropic(contentEvents, r);
    if (r.text || r.reasoning || r.toolCalls.length) return r;
  }

  // 2) 自定义规则（非标协议）。
  for (const rule of rules) {
    if (!rule.deltaPath) continue;
    const r = base('custom');
    r.ruleName = rule.name;
    if (parseWithRule(contentEvents, rule, r)) {
      r.notes.push({ key: 'stream.noteCustomRule', name: rule.name || rule.deltaPath });
      return r;
    }
  }

  // 3) 兜底：把非 JSON 的 data 原样拼起来（纯文本 SSE）。
  const raw = base('raw');
  const plain = contentEvents
    .map((e) => e.data)
    .filter((d) => {
      const s = d.trim();
      return s && s !== '[DONE]' && !s.startsWith('{') && !s.startsWith('[');
    });
  if (plain.length) {
    raw.text = plain.join('');
    raw.notes.push({ key: 'stream.notePlainText' });
    return raw;
  }

  raw.notes.push({ key: 'stream.noteUnknown' });
  return raw;
}

// providerLabelKey 返回协议名的 i18n key（界面负责翻译）。
export function providerLabelKey(res: AccumulatedStream): string {
  switch (res.provider) {
    case 'openai':
      return 'stream.providerOpenAI';
    case 'openai-responses':
      return 'stream.providerOpenAIResponses';
    case 'anthropic':
      return 'stream.providerAnthropic';
    case 'custom':
      return 'stream.providerCustom';
    default:
      return 'stream.providerRaw';
  }
}
