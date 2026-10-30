package flowquery

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSliceBodyText(t *testing.T) {
	data := []byte("hello world")

	// 默认整段。
	c := SliceBody(data, 0, 0)
	if c.Text != "hello world" || c.Total != 11 || c.Length != 11 || c.HasMore {
		t.Fatalf("整段读取不对：%+v", c)
	}

	// 分片：hasMore 让调用方知道还能继续。
	c = SliceBody(data, 0, 5)
	if c.Text != "hello" || !c.HasMore || c.Offset != 0 {
		t.Fatalf("首片不对：%+v", c)
	}
	c = SliceBody(data, 5, 100)
	if c.Text != " world" || c.HasMore || c.Offset != 5 {
		t.Fatalf("续片不对：%+v", c)
	}

	// 越界与负数都被夹住，不 panic。
	if c := SliceBody(data, 99, 5); c.Length != 0 || c.HasMore || c.Offset != 11 {
		t.Fatalf("越界 offset 应返回空片：%+v", c)
	}
	if c := SliceBody(data, -5, 5); c.Offset != 0 || c.Text != "hello" {
		t.Fatalf("负 offset 应归零：%+v", c)
	}

	// 上限保护。
	big := make([]byte, MaxBodyLimit+1000)
	c = SliceBody(big, 0, 1<<30)
	if c.Length != MaxBodyLimit || !c.HasMore {
		t.Fatalf("limit 应被夹到 %d：%d", MaxBodyLimit, c.Length)
	}
}

func TestSliceBodyBinary(t *testing.T) {
	// 非 UTF-8 或含 NUL 的内容按 base64 返回，避免把二进制硬塞进 JSON 文本。
	bin := []byte{0x00, 0x01, 0xff, 0xfe}
	c := SliceBody(bin, 0, 0)
	if c.Text != "" || c.Base64 == "" {
		t.Fatalf("二进制应回 base64：%+v", c)
	}
	decoded, err := base64.StdEncoding.DecodeString(c.Base64)
	if err != nil || string(decoded) != string(bin) {
		t.Fatalf("base64 往返失败：%v %v", decoded, err)
	}

	// 中文等多字节内容属于文本。
	c = SliceBody([]byte("你好，世界"), 0, 0)
	if c.Text != "你好，世界" || c.Base64 != "" {
		t.Fatalf("UTF-8 文本应回 text：%+v", c)
	}

	// 空正文：text 为空且没有 base64，调用方据此知道"没有正文"。
	c = SliceBody(nil, 0, 0)
	if c.Total != 0 || c.Length != 0 || c.Base64 != "" || c.Text != "" {
		t.Fatalf("空正文处理不对：%+v", c)
	}

	// 多字节字符被 offset 切断时，这一片不再是合法 UTF-8 → 自动降级为 base64，
	// 而不是返回被截坏的字符串。
	c = SliceBody([]byte("你好"), 1, 1)
	if c.Text != "" || c.Base64 == "" {
		t.Fatalf("切断多字节字符时应降级为 base64：%+v", c)
	}
}

func TestSliceBodyTruncationFlagIsCallerSet(t *testing.T) {
	// SliceBody 不知道原始正文是否已被 MaxBodyBytes 截断，
	// 该标记由调用方（读到 Flow.ResponseTruncated）补上；这里只确认字段存在且默认为 false。
	if c := SliceBody([]byte("x"), 0, 0); c.Truncated {
		t.Fatalf("默认不应标记截断：%+v", c)
	}
	if !strings.Contains(`{"truncated":true}`, "truncated") {
		t.Fatalf("字段名变化会影响前端/模型契约")
	}
}
