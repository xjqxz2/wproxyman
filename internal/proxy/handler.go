// handler 是代理的核心请求分发器，处理两类流量：
//
//  1. 普通 HTTP 请求（ServeHTTP → handleHTTP）：走完整拦截管线，
//     可被工具修改/暂停/短路，然后转发上游。
//  2. CONNECT 请求（handleConnect）：按域名决定处理方式——
//     - SSLProxyEnabled(host) 为 true → mitmConnect：进行 TLS 拦截
//     （用本地 CA 签发该域名的证书，协商 HTTP/2 或 HTTP/1.1 后解密）；
//     - 否则 → 原始 TCP 隧道（transparent tunnel，不解密、不记录）。
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

// certDownloadPath 是内置 CA 证书下载端点的路径。
// 手机/模拟器把代理指向本机后，在浏览器访问
// http://<电脑IP>:<port>/wpx-cert.pem 即可下载并安装抓包证书
// （等价于 Proxyman 的证书安装页）。
const certDownloadPath = "/wpx-cert.pem"

// certInstallPagePath 是内置证书安装指引页的路径（不含 .pem 后缀）。
// 手机/模拟器访问 http://<电脑IP>:<port>/wpx-cert 得到含 iOS/Android
// 安装步骤（含 iOS 关键的"证书信任设置→完全信任"一步）与证书下载链接的
// HTML 页面，等价于 Proxyman 的证书安装页。
const certInstallPagePath = "/wpx-cert"

// ServeHTTP 是代理的主入口：CONNECT 走隧道/MITM，其余走 HTTP 转发。
// 在转发前先处理内置证书下载端点与安装指引页（均不产生 Flow）。
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		h.handleConnect(w, r)
		return
	}
	if cfg := h.server.Config(); len(cfg.CACertPEM) > 0 {
		switch r.URL.Path {
		case certDownloadPath:
			h.serveCACert(w, cfg.CACertPEM)
			return
		case certInstallPagePath:
			h.serveCertInstallPage(w)
			return
		}
	}
	h.handleHTTP(w, r)
}

// serveCACert 以证书下载形式返回本地 CA 的 PEM 内容。
func (h *handler) serveCACert(w http.ResponseWriter, pem []byte) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="wproxyman-ca.pem"`)
	_, _ = w.Write(pem)
}

// serveCertInstallPage 返回证书安装指引页（双语），突出 iOS 的
// "证书信任设置 → 完全信任"这一最容易被忽略的步骤。
func (h *handler) serveCertInstallPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WProxyman Certificate Install</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;background:#1a1b1e;color:#e6e6e6;margin:0;padding:24px;line-height:1.6}
.card{max-width:640px;margin:0 auto;background:#232428;border:1px solid #33363b;border-radius:10px;padding:24px}
h1{font-size:20px;margin:0 0 8px}
h2{font-size:15px;margin:20px 0 8px;color:#8ab4f8}
ol{margin:0;padding-left:20px}
li{margin:6px 0}
a{color:#8ab4f8;word-break:break-all}
code{background:#00000055;border:1px solid #33363b;border-radius:4px;padding:1px 6px;font-family:ui-monospace,Consolas,monospace;font-size:13px}
.warn{background:#3a2e1a;border:1px solid #6b5418;border-radius:8px;padding:10px 14px;margin-top:16px;font-size:14px;color:#f0d78a}
.btn{display:inline-block;background:#3b82f6;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;margin:12px 0;font-weight:600}
.lang{color:#8b8f98;font-size:13px;margin-bottom:12px}
</style>
</head>
<body>
<div class="card">
<h1>WProxyman 证书安装 / Certificate Install</h1>
<div class="lang">请确认手机已通过本代理上网后，再下载并信任证书。</div>
<a class="btn" href="` + certDownloadPath + `">下载证书 / Download Certificate</a>

<h2>iOS</h2>
<ol>
<li>点上方按钮下载证书（若未自动弹窗，用 Safari 打开本页再试）；</li>
<li>设置 → 通用 → VPN 与设备管理 → 安装描述文件，完成安装；</li>
<li><strong>关键：</strong>设置 → 通用 → 关于本机 → <strong>证书信任设置（Certificate Trust Settings）</strong> → 打开 WProxyman CA 的<strong>完全信任（Full Trust）</strong>开关（否则 HTTPS 会提示"证书不正确"）；</li>
<li>完成后即可解密抓包。</li>
</ol>

<h2>Android</h2>
<ol>
<li>点上方按钮下载证书（.pem 或 .cer）；</li>
<li>设置 → 安全 → 加密与凭据 → 安装证书 → CA 证书 → 选择刚下载的文件；</li>
<li>按系统提示输入锁屏密码确认；</li>
<li>完成后即可解密抓包。</li>
</ol>

<div class="warn">若仍提示"证书不正确"：请确认 ① 已开启 iOS 完全信任；② 证书是本次下载的最新文件；③ 部分 App 做了证书固定（pinning），此类流量无法解密。</div>
</div>
</body>
</html>
`))
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
