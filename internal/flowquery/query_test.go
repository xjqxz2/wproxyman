package flowquery

import (
	"encoding/json"
	"testing"

	"wproxyman/internal/models"
)

func mk(id, method, host, path string, status int, opts ...func(*models.Flow)) *models.Flow {
	f := &models.Flow{
		ID: id, Method: method, Host: host, Path: path,
		FullURL: "https://" + host + path, ResponseStatus: status,
		StartedAt: 1000, Duration: 10, ResponseSize: 100,
		ResponseBody: []byte("SECRET-BODY"),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

func sample() []*models.Flow {
	return []*models.Flow{
		mk("1", "GET", "api.example.com", "/v1/users", 200),
		mk("2", "POST", "api.example.com", "/v1/orders", 201),
		mk("3", "GET", "api.example.com", "/v1/users/me", 401),
		mk("4", "GET", "cdn.example.com", "/app.js", 200),
		mk("5", "GET", "track.example.net", "/collect", 500, func(f *models.Flow) {
			f.Error = "upstream reset"
		}),
		mk("6", "POST", "api.openai.com", "/v1/chat/completions", 200, func(f *models.Flow) {
			f.IsStream = true
			f.StreamKind = "sse"
			f.SSEMsgs = []models.SSEMessage{{Seq: 1, Data: "a"}, {Seq: 2, Data: "b"}}
			f.IsWebSocket = false
		}),
		nil, // 允许有 nil，不应 panic
	}
}

func TestQueryFilter(t *testing.T) {
	flows := sample()
	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"按主机", Filter{Host: "api.example.com"}, []string{"1", "2", "3"}},
		{"主机不区分大小写", Filter{Host: "API.EXAMPLE.COM"}, []string{"1", "2", "3"}},
		{"按方法", Filter{Method: "post"}, []string{"2", "6"}},
		{"状态码精确", Filter{Status: "401"}, []string{"3"}},
		{"状态码按类", Filter{Status: "2xx"}, []string{"1", "2", "4", "6"}},
		{"仅失败", Filter{OnlyFailed: true}, []string{"3", "5"}},
		{"failed 关键字", Filter{Status: "failed"}, []string{"3", "5"}},
		{"仅流式", Filter{OnlyStream: true}, []string{"6"}},
		{"搜索路径", Filter{Search: "orders"}, []string{"2"}},
		{"搜索主机", Filter{Search: "cdn."}, []string{"4"}},
		{"搜索状态码", Filter{Search: "500"}, []string{"5"}},
		{"组合", Filter{Host: "api.example.com", Method: "GET", Status: "2xx"}, []string{"1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Query(flows, tc.f, Page{}, nil)
			if len(got.Flows) != len(tc.want) {
				t.Fatalf("命中 %d 条，期望 %d：%+v", len(got.Flows), len(tc.want), got.Flows)
			}
			for i, id := range tc.want {
				if got.Flows[i].ID != id {
					t.Fatalf("第 %d 条 id=%s，期望 %s", i, got.Flows[i].ID, id)
				}
			}
		})
	}
}

func TestQueryPagination(t *testing.T) {
	flows := sample()

	// 第一页：limit 2。Total 是总数（含 nil），Matched 是命中数。
	p1 := Query(flows, Filter{}, Page{Limit: 2}, nil)
	if p1.Total != len(flows) {
		t.Fatalf("total = %d, want %d", p1.Total, len(flows))
	}
	if p1.Matched != 6 {
		t.Fatalf("matched = %d, want 6（nil 不计入）", p1.Matched)
	}
	if p1.Returned != 2 || !p1.HasMore {
		t.Fatalf("第一页返回 %d 条 hasMore=%v", p1.Returned, p1.HasMore)
	}

	// 最后一页：不应再有更多。
	last := Query(flows, Filter{}, Page{Limit: 2, Offset: 4}, nil)
	if last.Returned != 2 || last.HasMore {
		t.Fatalf("最后一页返回 %d 条 hasMore=%v", last.Returned, last.HasMore)
	}

	// offset 超出范围：返回空但结构正常。
	over := Query(flows, Filter{}, Page{Limit: 2, Offset: 99}, nil)
	if over.Returned != 0 || over.HasMore {
		t.Fatalf("越界 offset 应返回空：%+v", over)
	}

	// 上限保护：limit 超过 MaxLimit 时被夹住。
	huge := Query(flows, Filter{}, Page{Limit: 99999}, nil)
	if huge.Limit != MaxLimit {
		t.Fatalf("limit 应被夹到 %d，实际 %d", MaxLimit, huge.Limit)
	}

	// 默认值：不给 limit 也要有上限。
	def := Query(flows, Filter{}, Page{}, nil)
	if def.Limit != DefaultLimit {
		t.Fatalf("默认 limit = %d, want %d", def.Limit, DefaultLimit)
	}
}

func TestQueryProjection(t *testing.T) {
	flows := sample()
	got := Query(flows, Filter{Host: "api.openai.com"}, Page{}, []string{"fullUrl", "streamKind", "eventCount"})
	if len(got.Flows) != 1 {
		t.Fatalf("应命中 1 条")
	}
	s := got.Flows[0]
	if s.FullURL == "" || s.StreamKind != "sse" || s.EventCount != 2 {
		t.Fatalf("请求的字段没填上：%+v", s)
	}
	// 未请求的可选字段必须为空（omitempty 后不会出现在 JSON 里）。
	if s.HTTPVersion != "" || s.ClientAddr != "" || len(s.ResponseHeaders) != 0 {
		t.Fatalf("未请求的字段不应有值：%+v", s)
	}

	// 关键：正文绝不出现在摘要里（这是给 AI 用的载荷，不能带 SECRET-BODY）。
	raw, _ := json.Marshal(got.Flows[0])
	if string(raw) == "" {
		t.Fatalf("序列化失败")
	}
	if contains(string(raw), "SECRET-BODY") || contains(string(raw), "responseBody") || contains(string(raw), "requestBody") {
		t.Fatalf("摘要里不应出现正文：%s", raw)
	}

	// 默认（不指定 fields）时也应只有基础字段。
	def := Query(flows, Filter{Host: "cdn.example.com"}, Page{}, nil)
	r2, _ := json.Marshal(def.Flows[0])
	if contains(string(r2), "requestHeaders") || contains(string(r2), "clientAddr") {
		t.Fatalf("默认摘要不应带可选字段：%s", r2)
	}
}

func TestQueryDoesNotMutateFlows(t *testing.T) {
	flows := sample()
	Query(flows, Filter{}, Page{}, []string{"responseHeaders"})
	if flows[0].ID != "1" || string(flows[0].ResponseBody) != "SECRET-BODY" {
		t.Fatalf("查询不应改动原对象：%+v", flows[0])
	}
}

func TestKnownField(t *testing.T) {
	if !KnownField("fullUrl") || !KnownField("responseHeaders") {
		t.Fatalf("合法字段被拒")
	}
	if KnownField("responseBody") || KnownField("随便写") {
		t.Fatalf("非法字段应被拒（正文永远不可请求）")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
