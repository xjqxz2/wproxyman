// HTTP 转发主管线：处理每一个被拦截的 HTTP 请求（明文代理流量或解密后的
// MITM 请求），执行完整的"捕获 → 工具 → 上游 → 响应"生命周期。
//
// 处理顺序：
//  1. 构造 Flow（请求行/头/来源地址）；
//  2. 运行请求阶段工具管线（Allow List 可跳过记录、断点可暂停、Map Local
//     等可短路响应）；
//  3. 检测 WebSocket 升级 → 单独走帧捕获中继；
//  4. 读取请求体（超限部分溢出到临时文件，保证上游收到完整 body）；
//  5. 上游转发（transport 支持 keep-alive、外部代理、跳过证书校验选项）；
//  6. 捕获并（若未超限）缓冲响应 → 运行响应阶段工具管线；
//  7. 将响应写回客户端，补全计时并发出 "completed" 事件。
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"wproxyman/internal/models"
)

// handleHTTP 处理单个被拦截的 HTTP 请求（明文代理流量或解密后的 MITM 请求），
// 并将响应写回客户端。
func (h *handler) handleHTTP(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.URL.Scheme == "https" {
		scheme = "https"
	}
	cfg := h.server.Config()
	f := flowFromRequest(r, scheme)

	// 记录客户端地址（用于 Source List 的设备来源展示）。
	if clientConn, ok := clientConnFrom(r); ok {
		f.ClientAddr = clientConn.RemoteAddr().String()
	}

	// 工具管线（请求阶段）。在记录之前执行，这样 Allow List 可以直接
	// 跳过整个记录流程。
	emit := func(phase string) {
		if cfg.OnFlow != nil {
			cfg.OnFlow(f, phase)
		}
	}
	decision, err := h.runRequestInterceptors(f)
	if err != nil {
		h.failFlow(w, f, emit, err.Error())
		return
	}
	if decision != nil && decision.SkipCapture {
		// Allow List：透明放行且不记录。
		h.transparentForward(w, r, scheme)
		return
	}
	emit("started")

	// WebSocket 升级：单独走帧捕获 + 中继。
	if isWebSocketUpgrade(r) {
		h.handleWebSocket(w, r, f, scheme, emit)
		return
	}

	// 读取请求体（超限部分溢出到临时文件，上游仍能收到完整 body）。
	spool, err := h.readRequestBody(f, r.Body, r.ContentLength, cfg.MaxBodyBytes)
	if err != nil {
		h.failFlow(w, f, emit, "failed to read request body: "+err.Error())
		return
	}
	if spool != nil {
		defer spool.Cleanup()
	}

	// Wait for breakpoint resolution if the flow was paused.
	done := false
	if decision != nil && decision.Wait {
		emit("paused")
		decision2, err := h.waitForDecision(f)
		if err != nil {
			h.failFlow(w, f, emit, err.Error())
			return
		}
		emit("updated")
		if decision2 != nil && decision2.ShortCircuit {
			done = true
		} else if decision2 != nil && decision2.UpstreamURL != "" {
			f.FullURL = decision2.UpstreamURL
		}
	}
	if done || (decision != nil && decision.ShortCircuit) {
		h.writeResponseFromFlow(w, r, f)
		f.CompletedAt = time.Now().UnixMilli()
		f.Duration = f.CompletedAt - f.StartedAt
		emit("completed")
		return
	}
	if decision != nil && decision.UpstreamURL != "" {
		f.FullURL = decision.UpstreamURL
	}

	// Build the outgoing request (may be modified by tools).
	outReq, err := requestToOutgoing(f)
	if err != nil {
		h.failFlow(w, f, emit, "invalid request URL: "+err.Error())
		return
	}
	if spool != nil {
		outReq.Body = io.NopCloser(spool.Reader())
		outReq.ContentLength = spool.Total()
	}

	// Forward upstream.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout)
	defer cancel()
	resp, err := h.server.transport().RoundTrip(outReq.WithContext(ctx))
	if err != nil {
		h.failFlow(w, f, emit, err.Error())
		return
	}
	defer resp.Body.Close()

	// Stream + capture the response, then run the response tool pipeline
	// before writing to the client.
	buffered := h.captureResponse(f, resp, cfg.MaxBodyBytes)
	if buffered {
		if wait2, err2 := h.runResponseInterceptors(f); wait2 {
			emit("paused")
			if _, err2 = h.waitForDecision(f); err2 != nil {
				h.failFlow(w, f, emit, err2.Error())
				return
			}
			emit("updated")
		}
	}
	h.writeResponseFromFlow(w, r, f)

	f.CompletedAt = time.Now().UnixMilli()
	f.Duration = f.CompletedAt - f.StartedAt
	emit("completed")
}

// readRequestBody reads the request body into the flow, spilling bytes beyond
// maxBytes into a temporary file so upstream still receives the full body.
func (h *handler) readRequestBody(f *models.Flow, body io.ReadCloser, contentLength, maxBytes int64) (*bodySpool, error) {
	if body == nil {
		if contentLength > 0 {
			f.RequestSize = contentLength
		}
		return nil, nil
	}
	spool := newBodySpool(maxBytes)
	total, err := spool.ReadFrom(body)
	_ = body.Close()
	if err != nil && total == 0 {
		return nil, err
	}
	f.RequestBody = spool.Head()
	f.RequestTruncated = spool.Truncated()
	if contentLength >= 0 {
		f.RequestSize = contentLength
	} else {
		f.RequestSize = total
	}
	return spool, nil
}

// captureResponse reads the upstream response body. If the body fits within
// maxBytes it is fully buffered into the flow (returning true so response
// tools may run). Oversized bodies stream straight through (returning false).
func (h *handler) captureResponse(f *models.Flow, resp *http.Response, maxBytes int64) bool {
	f.ResponseStatus = resp.StatusCode
	f.ResponseReason = resp.Status
	if i := strings.Index(f.ResponseReason, " "); i >= 0 {
		f.ResponseReason = f.ResponseReason[i+1:]
	}
	f.ResponseHeaders = headerFromTextproto(resp.Header)
	f.ResponseMimeType = sniffMime(resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	f.ResponseCookies = cookiesFromPairs(resp.Cookies())

	if resp.Body == nil {
		return true
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil && len(buf) == 0 {
		return true
	}
	if int64(len(buf)) > maxBytes {
		// Oversized: keep first maxBytes for the flow; the caller must not
		// run response tools and must stream the remainder directly.
		f.ResponseBody = buf[:maxBytes]
		f.ResponseTruncated = true
		f.ResponseSize = resp.ContentLength
		if f.ResponseSize < 0 {
			f.ResponseSize = int64(len(buf)) // may undercount; acceptable
		}
		return false
	}
	f.ResponseBody = buf
	if resp.ContentLength >= 0 {
		f.ResponseSize = resp.ContentLength
	} else {
		f.ResponseSize = int64(len(buf))
	}
	return true
}

// runRequestInterceptors invokes the tool pipeline on the request.
func (h *handler) runRequestInterceptors(f *models.Flow) (*InterceptDecision, error) {
	cfg := h.server.Config()
	if cfg.Interceptor == nil {
		return nil, nil
	}
	decision, err := cfg.Interceptor.OnRequest(f)
	if err != nil {
		return nil, err
	}
	return decision, nil
}

// transparentForward relays a request upstream without any capture or
// interception (Allow List passthrough).
func (h *handler) transparentForward(w http.ResponseWriter, r *http.Request, scheme string) {
	cfg := h.server.Config()
	u := *r.URL
	if u.Scheme == "" {
		u.Scheme = scheme
	}
	if u.Host == "" {
		u.Host = r.Host
	}
	out := r.Clone(r.Context())
	out.URL = &u
	out.RequestURI = ""
	out.Header = r.Header.Clone()
	// Restore the Host header (Clone preserves it; transport uses URL.Host).
	resp, err := h.server.transport().RoundTrip(out)
	if err != nil {
		http.Error(w, "transparent forward failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if cfg.MaxBodyBytes > 0 {
		_, _ = io.Copy(w, io.LimitReader(resp.Body, cfg.MaxBodyBytes+1))
	} else {
		_, _ = io.Copy(w, resp.Body)
	}
}

// runResponseInterceptors invokes the tool pipeline on the response.
// Returns whether the flow is paused awaiting a breakpoint decision.
func (h *handler) runResponseInterceptors(f *models.Flow) (bool, error) {
	cfg := h.server.Config()
	if cfg.Interceptor == nil {
		return false, nil
	}
	if err := cfg.Interceptor.OnResponse(f); err != nil {
		return false, err
	}
	// The interceptor indicates a pause by setting a flag on the flow.
	if f.WaitingForDecision {
		return true, nil
	}
	return false, nil
}

// waitForDecision blocks until a paused flow is resumed.
func (h *handler) waitForDecision(f *models.Flow) (*InterceptDecision, error) {
	cfg := h.server.Config()
	if cfg.Interceptor == nil {
		return nil, fmt.Errorf("no interceptor available")
	}
	return cfg.Interceptor.WaitForDecision(f.ID)
}

// writeResponseFromFlow writes the flow's response to the client.
func (h *handler) writeResponseFromFlow(w http.ResponseWriter, r *http.Request, f *models.Flow) {
	hdr := w.Header()
	for _, hh := range f.ResponseHeaders {
		// Skip hop-by-hop headers; Go manages them.
		if isHopByHop(hh.Name) {
			continue
		}
		hdr.Add(hh.Name, hh.Value)
	}
	status := f.ResponseStatus
	if status == 0 {
		status = http.StatusOK
	}
	if status == 101 {
		// Upgraded connections are handled elsewhere.
		return
	}

	noBody := r.Method == http.MethodHead || status == http.StatusNoContent || status == http.StatusNotModified
	if noBody {
		w.WriteHeader(status)
		return
	}

	body := f.ResponseBody
	if len(body) > 0 {
		if _, ok := hdr["Content-Length"]; !ok {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		}
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

// failFlow writes a 502 response and records the error on the flow.
func (h *handler) failFlow(w http.ResponseWriter, f *models.Flow, emit func(string), msg string) {
	f.Error = msg
	f.ResponseStatus = http.StatusBadGateway
	f.ResponseReason = "Bad Gateway"
	f.CompletedAt = time.Now().UnixMilli()
	f.Duration = f.CompletedAt - f.StartedAt
	emit("completed")
	http.Error(w, msg, http.StatusBadGateway)
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !headerContainsToken(r.Header.Get("Connection"), "upgrade") {
		return false
	}
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func headerContainsToken(v, token string) bool {
	for _, part := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func isHopByHop(name string) bool {
	for _, h := range hopByHopHeaders {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// bodySpool buffers a request body in memory up to max bytes, spilling the
// remainder to a temporary file.
type bodySpool struct {
	head      bytes.Buffer
	tail      *os.File
	max       int64
	total     int64
	truncated bool
}

func newBodySpool(max int64) *bodySpool {
	return &bodySpool{max: max}
}

func (s *bodySpool) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			remaining := s.max - int64(s.head.Len())
			if remaining > 0 {
				if int64(len(chunk)) <= remaining {
					s.head.Write(chunk)
				} else {
					s.head.Write(chunk[:remaining])
					if err := s.writeTail(chunk[remaining:]); err != nil {
						return s.total, err
					}
					s.truncated = true
				}
			} else if err := s.writeTail(chunk); err != nil {
				return s.total, err
			}
			s.total += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				return s.total, nil
			}
			return s.total, err
		}
	}
}

func (s *bodySpool) writeTail(b []byte) error {
	if s.tail == nil {
		f, err := os.CreateTemp("", "wproxyman-body-*")
		if err != nil {
			return err
		}
		s.tail = f
	}
	_, err := s.tail.Write(b)
	return err
}

// Head returns the in-memory portion of the body.
func (s *bodySpool) Head() []byte { return s.head.Bytes() }

// Total returns the full body size in bytes.
func (s *bodySpool) Total() int64 { return s.total }

// Truncated reports whether the body overflowed the memory cap.
func (s *bodySpool) Truncated() bool { return s.truncated }

// Reader returns a reader over the full body (memory + tail).
func (s *bodySpool) Reader() io.Reader {
	if s.tail == nil {
		return bytes.NewReader(s.head.Bytes())
	}
	if _, err := s.tail.Seek(0, io.SeekStart); err != nil {
		return bytes.NewReader(s.head.Bytes())
	}
	return io.MultiReader(bytes.NewReader(s.head.Bytes()), s.tail)
}

// Cleanup removes the temp file if one was created.
func (s *bodySpool) Cleanup() {
	if s.tail != nil {
		_ = s.tail.Close()
		_ = os.Remove(s.tail.Name())
		s.tail = nil
	}
}
