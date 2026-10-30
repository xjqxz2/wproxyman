package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"wproxyman/internal/models"
)

// 本文件验证流式响应（SSE / NDJSON）的行为。核心诉求：**不能缓冲**。
// 旧实现用 io.ReadAll 把整个响应读完才写给客户端，导致 LLM 逐 token 输出
// 一直卡到流结束；同时 RequestTimeout（120s）会把长流掐断。
//
// 断言方式刻意做成确定性的：用 channel 把上游卡住，证明"上游还没结束，
// 客户端就已经收到数据"，而不是靠 sleep 猜时长（那种测试在 CI 上会飘）。

// streamResult 收集代理回调产生的 flow 与增量事件。
type streamResult struct {
	mu        sync.Mutex
	completed *models.Flow
	streamed  []models.SSEMessage
	updates   int
}

func (r *streamResult) onFlow(f *models.Flow, phase string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch phase {
	case "completed":
		r.completed = f
	case "updated":
		r.updates++
	}
}

func (r *streamResult) onStream(_ *models.Flow, msgs []models.SSEMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamed = append(r.streamed, msgs...)
}

func (r *streamResult) snapshot() (*models.Flow, []models.SSEMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed, append([]models.SSEMessage(nil), r.streamed...)
}

// startStreamProxy 启动一个带 OnStream 回调的测试代理。
func startStreamProxy(t *testing.T, cfg Config) (*Server, *streamResult) {
	t.Helper()
	res := &streamResult{}
	cfg.Port = 0
	cfg.OnFlow = res.onFlow
	cfg.OnStream = res.onStream
	srv := NewServer(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv, res
}

// plainClientThroughProxy 返回一个走代理的普通 HTTP 客户端（不解密 HTTPS）。
func plainClientThroughProxy(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	u, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatalf("parse proxy addr: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
		Timeout:   30 * time.Second,
	}
}

// streamReader 在后台持续读取响应体并累积内容。
//
// 注意必须只有一个 reader：早先的写法让 readUntil 与 drain 各自起了一个
// goroutine 读同一个 body，先返回的那个会把后续数据吞进没人再读的 channel，
// 导致"第二条事件没到"的假失败。
type streamReader struct {
	mu   sync.Mutex
	buf  strings.Builder
	done bool
}

func newStreamReader(body io.ReadCloser) *streamReader {
	r := &streamReader{}
	go func() {
		defer body.Close()
		b := make([]byte, 1024)
		for {
			n, err := body.Read(b)
			r.mu.Lock()
			if n > 0 {
				r.buf.Write(b[:n])
			}
			finished := err != nil
			r.done = finished
			r.mu.Unlock()
			if finished {
				return
			}
		}
	}()
	return r
}

func (r *streamReader) content() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *streamReader) finished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

// waitFor 轮询等待内容里出现 want（或流结束）。
func (r *streamReader) waitFor(want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(r.content(), want) {
			return true
		}
		if r.finished() || time.Now().After(deadline) {
			return strings.Contains(r.content(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitDone 等到流结束并返回完整内容（超时返回当前已收到的内容）。
func (r *streamReader) waitDone(timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for !r.finished() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return r.content()
}

// TestStreamSSEIsForwardedIncrementally 是最关键的一条：
// 上游被 gate 卡住（流尚未结束）时，客户端就必须已经收到第一块数据。
// 旧实现（io.ReadAll）在这里会一直读不到东西而超时失败。
func TestStreamSSEIsForwardedIncrementally(t *testing.T) {
	gate := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)

		fmt.Fprint(w, "data: first\n\n")
		fl.Flush()
		<-gate // 卡住：流还没结束
		fmt.Fprint(w, "data: second\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	srv, res := startStreamProxy(t, Config{})
	client := plainClientThroughProxy(t, srv.Addr())

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		resp.Body.Close()
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	sr := newStreamReader(resp.Body)
	// 上游仍被卡住的情况下，也应当能读到第一条事件。
	if !sr.waitFor("data: first", 5*time.Second) {
		t.Fatalf("流被缓冲了：上游还没结束时客户端读不到数据，已收到 %q", sr.content())
	}

	close(gate)
	if !sr.waitFor("data: second", 5*time.Second) {
		t.Fatalf("第二条事件没到客户端，收到 %q", sr.content())
	}
	sr.waitDone(5 * time.Second)
	<-upstreamDone

	// 代理侧应当解析出两条事件，并且不再把 "updated" 整条重推。
	completed, streamed := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if !completed.IsStream || completed.StreamKind != "sse" {
		t.Fatalf("flow 未标记为 sse 流：isStream=%v kind=%q", completed.IsStream, completed.StreamKind)
	}
	if !completed.StreamEnded {
		t.Fatalf("flow 应标记 streamEnded")
	}
	if len(streamed) != 2 {
		t.Fatalf("增量事件数 = %d, want 2 (%+v)", len(streamed), streamed)
	}
	if streamed[0].Data != "first" || streamed[1].Data != "second" {
		t.Fatalf("事件内容不符：%+v", streamed)
	}
	if len(completed.SSEMsgs) != 2 {
		t.Fatalf("flow 上应记录 2 条事件，实际 %d", len(completed.SSEMsgs))
	}
}

// TestStreamTruncationKeepsForwardingEverything 验证超出 MaxBodyBytes 时
// 只停止"保存"，客户端仍然拿到完整数据（旧实现会把客户端一起截断）。
func TestStreamTruncationKeepsForwardingEverything(t *testing.T) {
	var want strings.Builder
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 1; i <= 20; i++ {
			line := fmt.Sprintf("data: event-%02d\n\n", i)
			want.WriteString(line)
			fmt.Fprint(w, line)
			fl.Flush()
		}
	}))
	defer upstream.Close()

	const maxBody = 64
	srv, res := startStreamProxy(t, Config{MaxBodyBytes: maxBody})
	client := plainClientThroughProxy(t, srv.Addr())

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	sr := newStreamReader(resp.Body)
	got := sr.waitDone(5 * time.Second)

	if got != want.String() {
		t.Fatalf("客户端收到的内容不完整：%d 字节，期望 %d 字节", len(got), want.Len())
	}
	completed, _ := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if !completed.ResponseTruncated {
		t.Fatalf("超限时 flow 应标记 ResponseTruncated")
	}
	if int64(len(completed.ResponseBody)) != maxBody {
		t.Fatalf("保存的正文应恰好被截到 %d 字节，实际 %d", maxBody, len(completed.ResponseBody))
	}
	if completed.ResponseSize != int64(want.Len()) {
		t.Fatalf("ResponseSize = %d, want %d（应为实际转发的字节数）", completed.ResponseSize, want.Len())
	}
}

// TestStreamSurvivesShortRequestTimeout 验证流式响应不受 RequestTimeout 限制：
// 上游持续 600ms，而 RequestTimeout 只有 150ms，流仍然完整送达。
func TestStreamSurvivesShortRequestTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 1; i <= 4; i++ {
			fmt.Fprintf(w, "data: tick-%d\n\n", i)
			fl.Flush()
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	srv, res := startStreamProxy(t, Config{RequestTimeout: 150 * time.Millisecond})
	client := plainClientThroughProxy(t, srv.Addr())

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	sr := newStreamReader(resp.Body)
	got := sr.waitDone(5 * time.Second)

	for i := 1; i <= 4; i++ {
		if !strings.Contains(got, fmt.Sprintf("tick-%d", i)) {
			t.Fatalf("第 %d 条事件丢失（流被超时掐断？），收到 %q", i, got)
		}
	}
	completed, _ := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if completed.Error != "" {
		t.Fatalf("流式响应不应因 RequestTimeout 报错，实际 error=%q", completed.Error)
	}
}

// TestStreamNDJSON 验证 NDJSON（每行一个 JSON）也走流式路径并逐行解析。
func TestStreamNDJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for _, line := range []string{`{"i":1}`, `{"i":2}`, `{"i":3}`} {
			fmt.Fprintln(w, line)
			fl.Flush()
		}
	}))
	defer upstream.Close()

	srv, res := startStreamProxy(t, Config{})
	client := plainClientThroughProxy(t, srv.Addr())

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	newStreamReader(resp.Body).waitDone(5 * time.Second)

	completed, streamed := res.snapshot()
	if completed == nil || completed.StreamKind != "ndjson" {
		t.Fatalf("应识别为 ndjson 流，实际 %+v", completed)
	}
	if len(streamed) != 3 {
		t.Fatalf("事件数 = %d, want 3", len(streamed))
	}
	if streamed[0].Data != `{"i":1}` {
		t.Fatalf("第一行解析结果 = %q", streamed[0].Data)
	}
}

// TestNonStreamStaysBuffered 确认普通响应仍走缓冲路径（响应工具/断点依赖完整正文）。
func TestNonStreamStaysBuffered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	srv, res := startStreamProxy(t, Config{})
	client := plainClientThroughProxy(t, srv.Addr())

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body := newStreamReader(resp.Body).waitDone(5 * time.Second)
	if body != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
	completed, streamed := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if completed.IsStream || len(streamed) != 0 {
		t.Fatalf("普通响应不应走流式路径：%+v", completed)
	}
	if string(completed.ResponseBody) != `{"ok":true}` {
		t.Fatalf("普通响应正文应被完整缓冲，实际 %q", completed.ResponseBody)
	}
}
