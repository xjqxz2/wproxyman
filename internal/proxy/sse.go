package proxy

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"wproxyman/internal/models"
)

// 本文件实现流式响应的增量解析。
//
// 背景：SSE（text/event-stream）与 NDJSON 是长连接逐条推送，若先 io.ReadAll
// 缓冲整包再返回，客户端会一直卡到流结束（LLM 逐 token 输出完全不可用）。
// 因此代理检测到这类响应后改为"边转发边解析"：
//   - 客户端实时收到每一块数据（读到即写、写完即 Flush）；
//   - 同时把解析出的事件累积到 Flow.SSEMsgs，供前端做时间线与累积还原。
//
// 解析器必须是增量且无状态的：一块数据可能停在任意字节位置（包括多字节
// UTF-8 字符中间），未结束的部分留在缓冲区等下一块。

// MaxStreamEvents 限制单条流最多记录多少事件。
// 长连接可能产出成千上万条事件（心跳、长回复），不设上限会吃内存；
// 超出后仍然照常转发给客户端，只是不再记录。
const MaxStreamEvents = 5000

// maxStreamLineBytes 是单行缓冲上限：超过即认为服务端没有按行推送，
// 直接把已缓冲内容当作一个事件抛出，避免内存无上限增长。
const maxStreamLineBytes = 1 << 20

// streamKindFor 根据响应 Content-Type 判断是否属于需要流式处理的响应，
// 返回 "sse" / "ndjson"，不需要流式处理时返回空串。
func streamKindFor(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "text/event-stream":
		return "sse"
	case "application/x-ndjson", "application/ndjson", "application/jsonl",
		"application/json-seq", "text/jsonl", "text/x-ndjson":
		return "ndjson"
	}
	return ""
}

// streamParser 增量解析 SSE / NDJSON。
type streamParser struct {
	kind    string
	started time.Time
	// buf 保存尚未构成完整一行的字节（含停在多字节 UTF-8 中间的情况）。
	buf []byte
	seq int
	// frame 是正在累积的 SSE 帧状态（NDJSON 模式下每行用一次即重置）。
	frame streamFrame
}

// streamFrame 是 SSE 规范里"一帧"累积中的字段。
type streamFrame struct {
	event    string
	data     []string
	id       string
	retry    int
	comments []string
	size     int
	hasField bool
}

func newStreamParser(kind string, started time.Time) *streamParser {
	return &streamParser{kind: kind, started: started}
}

// Feed 追加一块数据，返回本次能够完整解析出的事件（可能为空）。
func (p *streamParser) Feed(chunk []byte, now time.Time) []models.SSEMessage {
	p.buf = append(p.buf, chunk...)
	var out []models.SSEMessage
	for {
		line, consumed, ok := nextLine(p.buf)
		if !ok {
			break
		}
		p.buf = p.buf[consumed:]
		if ev, emitted := p.consumeLine(line, consumed, now); emitted {
			out = append(out, ev)
		}
	}
	// 防御：某一行超长（服务端不换行地推大块内容）时不能无限堆积缓冲，
	// 否则一个不换行的 100MB 响应就能把内存吃光。超过阈值就当作一个事件抛出。
	if len(p.buf) > maxStreamLineBytes {
		ev := p.newEvent(string(p.buf), now)
		p.buf = nil
		p.frame = streamFrame{}
		out = append(out, ev)
	}
	return out
}

// Flush 在流结束时调用：把缓冲区里没有换行结尾的残留内容按最后一帧处理。
func (p *streamParser) Flush(now time.Time) []models.SSEMessage {
	var out []models.SSEMessage
	if len(p.buf) > 0 {
		line := p.buf
		p.buf = nil
		if ev, emitted := p.consumeLine(line, len(line), now); emitted {
			out = append(out, ev)
		}
	}
	if ev, emitted := p.dispatch(now); emitted {
		out = append(out, ev)
	}
	return out
}

// consumeLine 处理一行（consumed 为该行含行尾分隔符的字节数）。
func (p *streamParser) consumeLine(line []byte, consumed int, now time.Time) (models.SSEMessage, bool) {
	if p.kind == "ndjson" {
		p.frame.size += consumed
		if len(bytes.TrimSpace(line)) == 0 {
			p.frame = streamFrame{}
			return models.SSEMessage{}, false
		}
		ev := p.newEvent(string(line), now)
		p.frame = streamFrame{}
		return ev, true
	}

	f := &p.frame
	f.size += consumed
	// 空行 = 一帧结束（SSE 规范）。
	if len(line) == 0 {
		return p.dispatch(now)
	}
	// 注释行（以 ':' 开头）：常被用作心跳，规范里不触发事件，
	// 但抓包工具需要看得见，所以记录为 Comment 事件。
	if line[0] == ':' {
		f.comments = append(f.comments, strings.TrimPrefix(string(line[1:]), " "))
		f.hasField = true
		return models.SSEMessage{}, false
	}
	field, value, _ := strings.Cut(string(line), ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		f.event = value
		f.hasField = true
	case "data":
		f.data = append(f.data, value)
		f.hasField = true
	case "id":
		f.id = value
		f.hasField = true
	case "retry":
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			f.retry = n
		}
		f.hasField = true
	}
	return models.SSEMessage{}, false
}

// dispatch 结束当前帧并生成事件；没有任何字段的空行不产生事件。
func (p *streamParser) dispatch(now time.Time) (models.SSEMessage, bool) {
	f := p.frame
	if !f.hasField {
		return models.SSEMessage{}, false
	}
	p.frame = streamFrame{}
	ev := p.newEvent(strings.Join(f.data, "\n"), now)
	ev.Event = f.event
	if ev.Event == "" {
		ev.Event = "message"
	}
	ev.ID = f.id
	ev.Retry = f.retry
	ev.Comment = strings.Join(f.comments, "\n")
	ev.Size = f.size
	return ev, true
}

// newEvent 生成带序号与时间偏移的事件（data/size 由调用方补齐）。
func (p *streamParser) newEvent(data string, now time.Time) models.SSEMessage {
	p.seq++
	return models.SSEMessage{
		Seq:   p.seq,
		Data:  data,
		AtMs:  now.Sub(p.started).Milliseconds(),
		Size:  p.frame.size,
		Event: "message",
	}
}

// nextLine 从缓冲区里取出一行（不含行尾）。
// 支持 \n、\r\n、\r 三种行尾；\r 位于缓冲区末尾时不能确定是 \r 还是 \r\n，
// 因此返回 ok=false 等下一块数据。
func nextLine(b []byte) (line []byte, consumed int, ok bool) {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			return b[:i], i + 1, true
		case '\r':
			if i+1 >= len(b) {
				return nil, 0, false
			}
			if b[i+1] == '\n' {
				return b[:i], i + 2, true
			}
			return b[:i], i + 1, true
		}
	}
	return nil, 0, false
}
