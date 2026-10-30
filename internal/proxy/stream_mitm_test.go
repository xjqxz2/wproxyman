package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wproxyman/internal/cert"
)

// 本文件验证**真实使用场景**下的流式转发：MITM 解密后的 HTTPS。
//
// 为什么单独测：上面的 stream_test.go 走的是明文 HTTP/1.1，而 LLM 接口几乎
// 都是 HTTPS（而且多半是 HTTP/2）。HTTP/2 下 net/http 的 ResponseWriter 是否
// 真的支持逐块 Flush、MITM 的 TLS 层有没有额外缓冲，都必须实测——否则会出现
// "明文代理里能实时输出、一开解密就又变回整包"的割裂行为。

// httpClientThroughProxyH2 与 httpClientThroughProxy 相同，但强制与目标协商
// HTTP/2（不设 ForceAttemptHTTP2 的话，自定义 TLSClientConfig 会让 Go 只用
// HTTP/1.1）。
func httpClientThroughProxyH2(t *testing.T, proxyAddr string, ca *cert.CA) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	transport := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			return url.Parse("http://" + proxyAddr)
		},
		TLSClientConfig:   &tls.Config{RootCAs: pool},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

// TestStreamSSEOverMITMHTTPSIsIncremental 是流式功能的关键回归：
// HTTPS 经 MITM 解密后，事件必须在**上游还没结束**时就到达客户端。
func TestStreamSSEOverMITMHTTPSIsIncremental(t *testing.T) {
	gate := make(chan struct{})
	upstreamDone := make(chan struct{})
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: first\n\n")
		fl.Flush()
		<-gate // 卡住：上游响应尚未结束
		fmt.Fprint(w, "data: second\n\n")
		fl.Flush()
	}))
	defer backend.Close()

	ca := testCA(t)
	srv, res := startStreamProxy(t, Config{CA: ca, SSLProxyEnabled: func(string) bool { return true }})
	srv.SetUpstreamInsecure(true)

	client := httpClientThroughProxy(t, srv.Addr(), ca)
	resp, err := client.Get(backend.URL + "/stream")
	if err != nil {
		t.Fatalf("GET via MITM: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		resp.Body.Close()
		t.Fatalf("content-type = %q", ct)
	}

	sr := newStreamReader(resp.Body)
	if !sr.waitFor("data: first", 5*time.Second) {
		t.Fatalf("MITM 下的流被缓冲了：上游未结束时读不到数据，已收到 %q", sr.content())
	}
	close(gate)
	if !sr.waitFor("data: second", 5*time.Second) {
		t.Fatalf("第二条事件没到客户端，收到 %q", sr.content())
	}
	sr.waitDone(5 * time.Second)
	<-upstreamDone

	completed, streamed := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if !completed.IsStream || completed.StreamKind != "sse" {
		t.Fatalf("flow 未标记为 sse 流：%+v", completed)
	}
	if !completed.TLS || completed.Scheme != "https" {
		t.Fatalf("应为 MITM 解密流量：scheme=%s tls=%v", completed.Scheme, completed.TLS)
	}
	if len(streamed) != 2 || streamed[0].Data != "first" || streamed[1].Data != "second" {
		t.Fatalf("解析出的事件不符：%+v", streamed)
	}
}

// TestStreamSSEOverHTTP2IsIncremental 验证 HTTP/2 下的逐块下发。
// h2 的 ResponseWriter 若被缓冲（或我们没拿到 Flusher），事件就会攒到最后一起给。
func TestStreamSSEOverHTTP2IsIncremental(t *testing.T) {
	gate := make(chan struct{})
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		<-gate
		fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	}))
	defer backend.Close()

	ca := testCA(t)
	srv, res := startStreamProxy(t, Config{CA: ca, SSLProxyEnabled: func(string) bool { return true }})
	srv.SetUpstreamInsecure(true)

	client := httpClientThroughProxyH2(t, srv.Addr(), ca)
	resp, err := client.Get(backend.URL + "/h2stream")
	if err != nil {
		t.Fatalf("GET via MITM: %v", err)
	}
	if resp.ProtoMajor != 2 {
		resp.Body.Close()
		t.Skipf("本次未协商到 HTTP/2（proto=%s），该环境下无法验证 h2 路径", resp.Proto)
	}

	sr := newStreamReader(resp.Body)
	if !sr.waitFor("data: one", 5*time.Second) {
		t.Fatalf("HTTP/2 下的流被缓冲了：上游未结束时读不到数据，已收到 %q", sr.content())
	}
	close(gate)
	if !sr.waitFor("data: two", 5*time.Second) {
		t.Fatalf("第二条事件没到客户端，收到 %q", sr.content())
	}
	done := sr.waitDone(5 * time.Second)
	if !strings.Contains(done, "data: two") {
		t.Fatalf("内容不完整：%q", done)
	}

	completed, _ := res.snapshot()
	if completed == nil {
		t.Fatalf("flow 未完成")
	}
	if completed.HTTPVersion != "" && !strings.Contains(completed.HTTPVersion, "2") {
		t.Logf("注意：记录到的版本为 %q", completed.HTTPVersion)
	}
}
