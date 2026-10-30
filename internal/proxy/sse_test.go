package proxy

import (
	"strings"
	"testing"
	"time"

	"wproxyman/internal/models"
)

// TestStreamKindFor 验证流式响应的识别范围。
func TestStreamKindFor(t *testing.T) {
	cases := map[string]string{
		"text/event-stream":                  "sse",
		"text/event-stream; charset=utf-8":   "sse",
		"TEXT/EVENT-STREAM":                  "sse",
		"application/x-ndjson":               "ndjson",
		"application/jsonl":                  "ndjson",
		"application/json-seq":               "ndjson",
		"application/json":                   "",
		"text/html; charset=utf-8":           "",
		"":                                   "",
		"text/plain":                         "",
		"application/x-ndjson; charset=utf8": "ndjson",
	}
	for ct, want := range cases {
		if got := streamKindFor(ct); got != want {
			t.Errorf("streamKindFor(%q) = %q, want %q", ct, got, want)
		}
	}
}

// summarize 只保留用于断言的字段，避免比较 AtMs（依赖真实时间）。
func summarize(msgs []models.SSEMessage) []models.SSEMessage {
	out := make([]models.SSEMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, models.SSEMessage{
			Seq: m.Seq, Event: m.Event, Data: m.Data, ID: m.ID, Retry: m.Retry, Comment: m.Comment,
		})
	}
	return out
}

func ev(seq int, event, data string) models.SSEMessage {
	if event == "" {
		event = "message"
	}
	return models.SSEMessage{Seq: seq, Event: event, Data: data}
}

// TestStreamParserSSE 覆盖 SSE 规范里容易踩坑的帧格式。
func TestStreamParserSSE(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []models.SSEMessage
	}{
		{"单条事件", "data: hello\n\n", []models.SSEMessage{ev(1, "", "hello")}},
		{"命名事件", "event: ping\ndata: {}\n\n", []models.SSEMessage{ev(1, "ping", "{}")}},
		{"多行 data 以换行拼接", "data: a\ndata: b\n\n", []models.SSEMessage{ev(1, "", "a\nb")}},
		{"注释心跳", ": keep-alive\n\n", []models.SSEMessage{{Seq: 1, Event: "message", Comment: "keep-alive"}}},
		{"CRLF 行尾", "data: x\r\n\r\n", []models.SSEMessage{ev(1, "", "x")}},
		{"CR 行尾", "data: y\r\r", []models.SSEMessage{ev(1, "", "y")}},
		{"id 与 retry", "id: 42\nretry: 1000\ndata: z\n\n", []models.SSEMessage{{Seq: 1, Event: "message", Data: "z", ID: "42", Retry: 1000}}},
		{"OpenAI 的 DONE 哨兵", "data: [DONE]\n\n", []models.SSEMessage{ev(1, "", "[DONE]")}},
		{"只有字段名没有冒号", "data\n\n", []models.SSEMessage{ev(1, "", "")}},
		{"连续两条", "data: 1\n\ndata: 2\n\n", []models.SSEMessage{ev(1, "", "1"), ev(2, "", "2")}},
		{"纯空行不产生事件", "\n\n\n", nil},
		{"忽略未知字段", "foo: bar\ndata: ok\n\n", []models.SSEMessage{ev(1, "", "ok")}},
		{"值里的冒号完整保留", "data: a:b:c\n\n", []models.SSEMessage{ev(1, "", "a:b:c")}},
		{"只去掉一个前导空格", "data:  x\n\n", []models.SSEMessage{ev(1, "", " x")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newStreamParser("sse", time.Now())
			// 连同 Flush 一起断言：流结束时一定会调用 Flush，而块末尾单独的
			// '\r' 无法区分是 '\r' 还是 '\r\n'，解析器会正确地推迟到那时。
			msgs := p.Feed([]byte(tc.input), time.Now())
			msgs = append(msgs, p.Flush(time.Now())...)
			got := summarize(msgs)
			if len(got) != len(tc.want) {
				t.Fatalf("事件数 = %d, want %d（%+v）", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("第 %d 条 = %+v, want %+v", i+1, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestStreamParserSplitChunks 验证增量解析：一块数据可以停在任意字节位置
// （包括多字节 UTF-8 中间、\r 与 \n 之间、一帧的中间）。
func TestStreamParserSplitChunks(t *testing.T) {
	t.Run("逐字节喂入中文", func(t *testing.T) {
		p := newStreamParser("sse", time.Now())
		input := "data: 你好世界\n\n"
		var got []models.SSEMessage
		for i := 0; i < len(input); i++ {
			got = append(got, p.Feed([]byte(input[i:i+1]), time.Now())...)
		}
		if len(got) != 1 || got[0].Data != "你好世界" {
			t.Fatalf("逐字节喂入解析结果 = %+v", summarize(got))
		}
	})

	t.Run("一帧跨两块", func(t *testing.T) {
		p := newStreamParser("sse", time.Now())
		var got []models.SSEMessage
		got = append(got, p.Feed([]byte("data: a\n"), time.Now())...)
		got = append(got, p.Feed([]byte("data: b\n\n"), time.Now())...)
		if len(got) != 1 || got[0].Data != "a\nb" {
			t.Fatalf("跨块解析结果 = %+v", summarize(got))
		}
	})

	t.Run("CR 停在块末尾", func(t *testing.T) {
		p := newStreamParser("sse", time.Now())
		var got []models.SSEMessage
		got = append(got, p.Feed([]byte("data: x\r"), time.Now())...)
		if len(got) != 0 {
			t.Fatalf("还没遇到行尾就不该出事件：%+v", summarize(got))
		}
		got = append(got, p.Feed([]byte("\n\r\n"), time.Now())...)
		if len(got) != 1 || got[0].Data != "x" {
			t.Fatalf("CRLF 跨块解析结果 = %+v", summarize(got))
		}
	})

	t.Run("Flush 处理没有结尾空行的残留", func(t *testing.T) {
		p := newStreamParser("sse", time.Now())
		if got := p.Feed([]byte("data: tail"), time.Now()); len(got) != 0 {
			t.Fatalf("未结束的帧不该提前出事件：%+v", summarize(got))
		}
		got := p.Flush(time.Now())
		if len(got) != 1 || got[0].Data != "tail" {
			t.Fatalf("Flush 结果 = %+v", summarize(got))
		}
	})

	t.Run("序号跨块连续", func(t *testing.T) {
		p := newStreamParser("sse", time.Now())
		var got []models.SSEMessage
		got = append(got, p.Feed([]byte("data: 1\n\ndata: 2\n"), time.Now())...)
		got = append(got, p.Feed([]byte("\ndata: 3\n\n"), time.Now())...)
		if len(got) != 3 || got[0].Seq != 1 || got[2].Seq != 3 {
			t.Fatalf("序号不连续：%+v", summarize(got))
		}
	})
}

// TestStreamParserNDJSON 验证 NDJSON：每行一条，空行忽略。
func TestStreamParserNDJSON(t *testing.T) {
	p := newStreamParser("ndjson", time.Now())
	got := summarize(p.Feed([]byte("{\"i\":1}\n\n{\"i\":2}\n{\"i\":3}"), time.Now()))
	if len(got) != 2 {
		t.Fatalf("换行结尾前应有 2 条（第 3 条未换行），实际 %+v", got)
	}
	tail := summarize(p.Flush(time.Now()))
	if len(tail) != 1 || tail[0].Data != `{"i":3}` {
		t.Fatalf("Flush 应补出最后一行：%+v", tail)
	}
}

// TestStreamParserHugeLineGuard 验证超长行不会无限占用内存。
func TestStreamParserHugeLineGuard(t *testing.T) {
	p := newStreamParser("ndjson", time.Now())
	huge := strings.Repeat("x", maxStreamLineBytes+10)
	got := p.Feed([]byte(huge), time.Now())
	if len(got) != 1 {
		t.Fatalf("超长行应被当作一个事件抛出，实际 %d 条", len(got))
	}
	if len(got[0].Data) != len(huge) {
		t.Fatalf("事件内容长度 = %d, want %d", len(got[0].Data), len(huge))
	}
	if len(p.buf) != 0 {
		t.Fatalf("抛出后缓冲区应清空，实际 %d 字节", len(p.buf))
	}
}
