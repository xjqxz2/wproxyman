package systemproxy

import "testing"

// TestNormalize 验证代理地址归一化：各平台读回来的写法不同（带协议前缀、
// localhost、结尾斜杠、多余空白），必须能比较成同一个值。
func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"   ":                     "",
		"127.0.0.1:10086":         "127.0.0.1:10086",
		"127.0.0.1:10086 ":        "127.0.0.1:10086",
		"http://127.0.0.1:10086":  "127.0.0.1:10086",
		"http://127.0.0.1:10086/": "127.0.0.1:10086",
		"localhost:10086":         "127.0.0.1:10086",
		"http://localhost:10086":  "127.0.0.1:10086",
		"LOCALHOST:10086":         "127.0.0.1:10086",
		"192.168.1.10:8080":       "192.168.1.10:8080",
		"[::1]:10086":             "[::1]:10086",
		"not-an-address":          "not-an-address",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
