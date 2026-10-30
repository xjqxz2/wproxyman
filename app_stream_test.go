package main

import (
	"encoding/json"
	"testing"

	"wproxyman/internal/models"
)

// TestOnStreamEventPayload 验证 flow:stream 事件的负载契约。
//
// 这是 Go 侧（map 的 key）与前端（App.tsx 里读取的字段名）之间唯一的接口。
// 两边都是"看起来对"就行的弱类型，改名不会被任何一端的测试发现，
// 所以在这里把字段钉死：漏一个，前端就会静默不更新。
func TestOnStreamEventPayload(t *testing.T) {
	a := NewApp() // 不启动 Wails：emit 只把事件放进队列
	f := models.NewFlow()
	f.ResponseSize = 2048
	f.FirstByteAt = 1699999999999
	f.StreamKind = "sse"
	f.StreamTruncated = true
	f.ResponseTruncated = true
	msgs := []models.SSEMessage{{Seq: 1, Event: "message", Data: "hello", AtMs: 12, Size: 20}}

	a.onStream(f, msgs)

	select {
	case m := <-a.eventCh:
		if m.name != "flow:stream" {
			t.Fatalf("event name = %q, want flow:stream", m.name)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(m.data, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		// 前端 App.tsx 读取的字段，一个都不能少。
		for _, key := range []string{"id", "messages", "size", "firstByteAt", "streamKind", "truncated", "responseTruncated"} {
			if _, ok := payload[key]; !ok {
				t.Errorf("payload 缺少字段 %q（前端读取该字段，改名会静默失效）", key)
			}
		}

		var id string
		_ = json.Unmarshal(payload["id"], &id)
		if id != f.ID {
			t.Errorf("id = %q, want %q", id, f.ID)
		}
		var size int64
		_ = json.Unmarshal(payload["size"], &size)
		if size != 2048 {
			t.Errorf("size = %d, want 2048", size)
		}
		var gotMsgs []models.SSEMessage
		_ = json.Unmarshal(payload["messages"], &gotMsgs)
		if len(gotMsgs) != 1 || gotMsgs[0].Data != "hello" {
			t.Errorf("messages = %+v", gotMsgs)
		}
		var kind string
		_ = json.Unmarshal(payload["streamKind"], &kind)
		if kind != "sse" {
			t.Errorf("streamKind = %q, want sse", kind)
		}
		var truncated bool
		_ = json.Unmarshal(payload["truncated"], &truncated)
		if !truncated {
			t.Errorf("truncated = false, want true")
		}
	default:
		t.Fatal("没有产生 flow:stream 事件")
	}
}
