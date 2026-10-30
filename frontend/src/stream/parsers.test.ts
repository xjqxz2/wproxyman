// parsers.test.ts — 流式解析的回归测试。
//
// 用真实报文样例（OpenAI chat.completions / responses、Anthropic messages、
// 私有协议、纯文本）覆盖还原逻辑。这些断言原先是一次性的临时脚本，
// 现在落到仓库里：解析器以后被改动时有人守着。
//
// 运行：cd frontend && npm test

import { describe, expect, it } from 'vitest';
import { accumulateStream, providerLabelKey } from './parsers';
import { jsonPathGet } from './jsonpath';
import type { SSEMessage, StreamRule } from '../types';

// evs 用给定的 data 序列构造事件列表（atMs 递增，模拟真实到达时间）。
function evs(rows: Array<{ event?: string; data: string; comment?: string }>): SSEMessage[] {
  return rows.map((r, i) => ({
    seq: i + 1,
    event: r.event ?? 'message',
    data: r.data,
    comment: r.comment,
    atMs: i * 40,
    size: r.data.length + 12,
  }));
}

describe('JSONPath 子集', () => {
  const doc = { a: { b: [{ c: 'x' }, { c: 'y' }] }, 'k-1': { z: 3 }, arr: [10, 20, 30] };

  it('点号与下标', () => {
    expect(jsonPathGet(doc, '$.a.b[0].c')).toBe('x');
    expect(jsonPathGet(doc, '$.a.b[1].c')).toBe('y');
  });
  it('引号键与负下标', () => {
    expect(jsonPathGet(doc, "$['k-1'].z")).toBe(3);
    expect(jsonPathGet(doc, '$.arr[-1]')).toBe(30);
  });
  it('缺失或类型不符返回 undefined', () => {
    expect(jsonPathGet(doc, '$.a.nope.deep')).toBeUndefined();
    expect(jsonPathGet(doc, '$.a.b[0].c.d')).toBeUndefined();
  });
});

describe('OpenAI 兼容（chat.completions）', () => {
  const events = evs([
    { data: '{"id":"1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}' },
    { data: '{"choices":[{"index":0,"delta":{"content":"你"}}]}' },
    { data: '{"choices":[{"index":0,"delta":{"content":"好"}}]}' },
    { data: '{"choices":[{"index":0,"delta":{"reasoning_content":"先想一下"}}]}' },
    // tool_calls 的 arguments 是分片下发的 JSON 字符串
    { data: '{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"weather","arguments":"{\\"ci"}}]}}]}' },
    { data: '{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\\":\\"北京\\"}"}}]}}]}' },
    { data: '{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}' },
    { data: '{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}' },
    { data: '[DONE]' },
  ]);
  const acc = accumulateStream(events);

  it('识别为 openai', () => {
    expect(acc.provider).toBe('openai');
    expect(providerLabelKey(acc)).toBe('stream.providerOpenAI');
  });
  it('拼接文本与思维链', () => {
    expect(acc.text).toBe('你好');
    expect(acc.reasoning).toBe('先想一下');
  });
  it('tool_calls 分片拼成合法 JSON', () => {
    expect(acc.toolCalls).toHaveLength(1);
    expect(acc.toolCalls[0].name).toBe('weather');
    expect(acc.toolCalls[0].id).toBe('call_1');
    expect(acc.toolCalls[0].args).toBe('{"city":"北京"}');
  });
  it('元信息：model / finish_reason / usage', () => {
    expect(acc.model).toBe('gpt-4o-mini');
    expect(acc.finishReason).toBe('tool_calls');
    expect(acc.usage).toMatchObject({ inputTokens: 12, outputTokens: 7, totalTokens: 19 });
  });
});

describe('Anthropic 兼容（messages）', () => {
  const events = evs([
    { event: 'message_start', data: '{"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":25}}}' },
    { event: 'content_block_start', data: '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}' },
    { event: 'content_block_delta', data: '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"答案"}}' },
    { event: 'content_block_delta', data: '{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"推理中"}}' },
    { event: 'content_block_stop', data: '{"type":"content_block_stop","index":0}' },
    { event: 'content_block_start', data: '{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"search"}}' },
    { event: 'content_block_delta', data: '{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"q\\":"}}' },
    { event: 'content_block_delta', data: '{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\\"golang\\"}"}}' },
    { event: 'content_block_stop', data: '{"type":"content_block_stop","index":1}' },
    { event: 'message_delta', data: '{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}' },
    { event: 'message_stop', data: '{"type":"message_stop"}' },
    { event: 'ping', data: '{"type":"ping"}' },
  ]);
  const acc = accumulateStream(events);

  it('识别为 anthropic', () => {
    expect(acc.provider).toBe('anthropic');
    expect(providerLabelKey(acc)).toBe('stream.providerAnthropic');
  });
  it('文本与思维链', () => {
    expect(acc.text).toBe('答案');
    expect(acc.reasoning).toBe('推理中');
  });
  it('tool_use 的 input_json_delta 拼回 JSON', () => {
    expect(acc.toolCalls).toHaveLength(1);
    expect(acc.toolCalls[0].name).toBe('search');
    expect(acc.toolCalls[0].args).toBe('{"q":"golang"}');
  });
  it('元信息：model / stop_reason / usage', () => {
    expect(acc.model).toBe('claude-sonnet-4');
    expect(acc.stopReason).toBe('end_turn');
    expect(acc.usage).toMatchObject({ inputTokens: 25, outputTokens: 42 });
  });
});

describe('OpenAI responses API', () => {
  const events = evs([
    { event: 'response.created', data: '{"type":"response.created","response":{"model":"gpt-5"}}' },
    { event: 'response.output_text.delta', data: '{"type":"response.output_text.delta","delta":"部分"}' },
    { event: 'response.output_text.delta', data: '{"type":"response.output_text.delta","delta":"文本"}' },
    { event: 'response.completed', data: '{"type":"response.completed","response":{"model":"gpt-5","usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}}}' },
  ]);
  const acc = accumulateStream(events);

  it('识别为 responses 并拼接文本', () => {
    expect(acc.provider).toBe('openai-responses');
    expect(acc.text).toBe('部分文本');
    expect(acc.usage).toMatchObject({ outputTokens: 4 });
  });
});

describe('自定义 JSONPath 规则（非标协议）', () => {
  const events = evs([
    { event: 'chunk', data: '{"payload":{"frag":"内部"}}' },
    { event: 'chunk', data: '{"payload":{"frag":"网关"}}' },
    { event: 'keepalive', data: '{"type":"ka"}' },
    { event: 'end', data: '[END]' },
  ]);
  const rule: StreamRule = {
    name: '内部网关',
    matchPath: '$.payload.frag',
    deltaPath: '$.payload.frag',
    stopValue: '[END]',
  };
  const acc = accumulateStream(events, [rule]);

  it('按规则提取增量', () => {
    expect(acc.provider).toBe('custom');
    expect(acc.text).toBe('内部网关');
    expect(acc.ruleName).toBe('内部网关');
  });
  it('notes 带 key 与规则名（界面负责翻译）', () => {
    expect(acc.notes[0]).toEqual({ key: 'stream.noteCustomRule', name: '内部网关' });
  });
  it('内置预设不匹配时才落到自定义规则', () => {
    // 同样的规则套在一条标准 OpenAI 流上：预设优先，不应被自定义规则截胡。
    const openai = evs([{ data: '{"payload":{"frag":"x"},"choices":[{"delta":{"content":"正式"}}]}' }]);
    const got = accumulateStream(openai, [rule]);
    expect(got.provider).toBe('openai');
    expect(got.text).toBe('正式');
  });
});

describe('兜底与统计', () => {
  it('纯文本 SSE 按文本拼接', () => {
    const acc = accumulateStream(evs([{ data: 'Hello ' }, { data: 'world' }, { data: '[DONE]' }]));
    expect(acc.provider).toBe('raw');
    expect(acc.text).toBe('Hello world');
  });

  it('无法识别时给出可操作的提示', () => {
    const acc = accumulateStream(evs([{ data: '{"unknown":1}' }]));
    expect(acc.provider).toBe('raw');
    expect(acc.notes[0].key).toBe('stream.noteUnknown');
  });

  it('心跳帧不计入文本但计入统计', () => {
    const acc = accumulateStream(
      evs([
        { data: 'ping', comment: 'ping' },
        { data: '{"choices":[{"delta":{"content":"a"}}]}' },
        { data: 'ping', comment: 'ping' },
        { data: '{"choices":[{"delta":{"content":"b"}}]}' },
      ]),
    );
    expect(acc.text).toBe('ab');
    expect(acc.eventCount).toBe(4);
  });

  it('空事件列表不炸', () => {
    const acc = accumulateStream([]);
    expect(acc.text).toBe('');
    expect(acc.toolCalls).toEqual([]);
  });
});
