// handler 是代理的核心请求分发器，处理两类流量：
//
//  1. 普通 HTTP 请求（ServeHTTP → handleHTTP）：走完整拦截管线，
//     可被工具修改/暂停/短路，然后转发上游。
//  2. CONNECT 请求（handleConnect）：按域名决定处理方式——
//      - SSLProxyEnabled(host) 为 true → mitmConnect：进行 TLS 拦截
//        （用本地 CA 签发该域名的证书，协商 HTTP/2 或 HTTP/1.1 后解密）；
//      - 否则 → 原始 TCP 隧道（transparent tunnel，不解密、不记录）。
//
// 注意：MITM 后的 HTTP 服务器必须直接读取解密后的 tlsConn，绝不能复用
// 外层 http.Server 的缓冲读取器（那里还是密文）。
package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"wproxyman/internal/models"
)

// handler 分发进入代理的请求。
type handler struct {
	server *Server
}

// ServeHTTP 是代理的主入口：CONNECT 走隧道/MITM，其余走 HTTP 转发。
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		h.handleConnect(w, r)
		return
	}
	h.handleHTTP(w, r)
}

// sslProxyEnabled 查询该主机是否启用 MITM 解密。
func (h *handler) sslProxyEnabled(host string) bool {
	cfg := h.server.Config()
	if cfg.SSLProxyEnabled == nil {
		return false
	}
	return cfg.SSLProxyEnabled(host)
}

// connContext 将底层连接存入请求上下文，供后续获取客户端地址等。
func (s *Server) connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, clientConnKey{}, c)
}

// clientConnKey 是连接上下文中的键类型。
type clientConnKey struct{}

// clientConnFrom 从请求上下文取回底层连接。
func clientConnFrom(r *http.Request) (net.Conn, bool) {
	c, ok := r.Context().Value(clientConnKey{}).(net.Conn)
	return c, ok
}

// handleConnect 处理 HTTP CONNECT 方法：当目标主机启用 SSL 解密时走 MITM
// 拦截，否则建立原始 TCP 隧道（透明转发）。
func (h *handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	hostPort := r.Host
	if hostPort == "" {
		http.Error(w, "missing CONNECT host", http.StatusBadRequest)
		return
	}
	host := hostnameOnly(hostPort)

	if h.sslProxyEnabled(host) {
		h.mitmConnect(w, r, host, hostPort)
		return
	}

	// 原始隧道：在客户端与上游之间透明转发字节（不解密、不记录）。
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	upstream, err := h.server.dialUpstream(hostPort)
	if err != nil {
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstream.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	h.server.relay(clientConn, upstream)
}

// mitmConnect 对 CONNECT 目标执行 TLS 拦截（MITM 解密）。
//
// 流程：劫持连接 → 回复 200 确认 → 用本地 CA 签发目标域名证书 →
// TLS 握手 → 按 ALPN 协商结果选择 HTTP/2 或 HTTP/1.1 服务器解析解密流量。
func (h *handler) mitmConnect(w http.ResponseWriter, r *http.Request, host, hostPort string) {
	cfg := h.server.Config()
	if cfg.CA == nil {
		// 没有可用 CA：回退为原始隧道。
		h.handleConnect(w, r)
		return
	}
	cert, err := cfg.CA.SignForHost(host)
	if err != nil {
		http.Error(w, "failed to sign certificate: "+err.Error(), http.StatusInternalServerError)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	h.server.trackConn(clientConn, true)

	// 先向客户端确认 CONNECT 成功，再开始 TLS 握手（否则客户端会一直等待）。
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		h.server.trackConn(clientConn, false)
		_ = clientConn.Close()
		return
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		// 声明支持 HTTP/2，让客户端在拦截连接上协商 h2
		//（浏览器的大多数 HTTPS 流量都使用 h2）。
		NextProtos: []string{"h2", "http/1.1"},
		MinVersion: tls.VersionTLS10,
	}
	tlsConn := tls.Server(clientConn, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		h.server.trackConn(clientConn, false)
		_ = clientConn.Close()
		return
	}

	// 按 ALPN 协商结果分发协议。
	alpn := tlsConn.ConnectionState().NegotiatedProtocol
	if alpn == "h2" {
		// HTTP/2：ServeConn 会阻塞直到连接结束，无需额外的生命周期管理。
		h2srv := &http2.Server{IdleTimeout: 5 * time.Minute}
		h2srv.ServeConn(tlsConn, &http2.ServeConnOpts{
			Handler: h.mitmHandler(host, hostPort),
		})
		h.server.trackConn(clientConn, false)
		_ = clientConn.Close()
		return
	}

	// 在解密后的 TLS 连接上提供 HTTP/1.1 服务。注意：必须直接读取
	// tlsConn，绝不能使用外层 http.Server 的缓冲读取器——那里的字节仍是密文。
	//
	// singleListener 在第二次 Accept 时返回 EOF，导致 Serve 立即返回，
	// 而连接 goroutine 仍在处理请求。我们通过 connDone 通道（连接关闭或
	// 被劫持时触发）阻塞，以保持底层连接存活。
	var hijacked atomic.Bool
	var doneOnce sync.Once
	connDone := make(chan struct{})
	srv := &http.Server{
		Handler:           h.mitmHandler(host, hostPort),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       0, // 连接保持活跃直到客户端关闭；WebSocket 升级依赖此行为
		ConnState: func(_ net.Conn, s http.ConnState) {
			switch s {
			case http.StateHijacked:
				hijacked.Store(true)
				doneOnce.Do(func() { close(connDone) })
			case http.StateClosed:
				doneOnce.Do(func() { close(connDone) })
			}
		},
	}
	_ = srv.Serve(&singleListener{conn: tlsConn})

	select {
	case <-connDone:
	case <-time.After(2 * time.Minute):
	}

	h.server.trackConn(clientConn, false)
	if !hijacked.Load() {
		_ = clientConn.Close()
	}
}

// mitmHandler 包装解密连接上到达的请求，统一标记为 https 并补充 Host。
func (h *handler) mitmHandler(connectHost, connectHostPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = "https"
		if r.Host == "" {
			r.Host = connectHostPort
		}
		// 让 URL 的 Host 与请求头一致，便于 Flow 展示。
		r.URL.Host = r.Host
		h.handleHTTP(w, r)
	})
}

// singleListener 是一个只交付一条连接的监听器：第一次 Accept 返回该连接，
// 之后返回 EOF。用于让 http.Server 在单条 TLS 连接上提供 HTTP/1.1 服务。
type singleListener struct {
	conn net.Conn
	done bool
}

func (l *singleListener) Accept() (net.Conn, error) {
	if l.done {
		return nil, io.EOF
	}
	l.done = true
	return l.conn, nil
}
func (l *singleListener) Close() error   { return nil }
func (l *singleListener) Addr() net.Addr { return l.conn.LocalAddr() }

// hostnameOnly 从 host:port 字符串中剥离端口，兼容 IPv6 写法。
func hostnameOnly(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return strings.Trim(hostPort, "[]")
}

// headerFromTextproto 将 http.Header 转换为内部 Header 模型，
// 尽可能保留原始键的大小写。
func headerFromTextproto(h http.Header) []models.Header {
	out := make([]models.Header, 0, len(h))
	for k, v := range h {
		// 使用收到的原始键名（不强制规范化）
		for _, vv := range v {
			out = append(out, models.Header{Name: k, Value: vv})
		}
	}
	return out
}
