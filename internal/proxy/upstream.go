// 上游连接管理：负责转发请求时的传输层、外部代理与原始隧道拨号。
//
// 设计要点：
//   - 维护一个进程级共享的 http.Transport（keep-alive 连接池），支持
//     HTTP/2 上游直连；外部代理（External Proxy 工具）与"跳过证书校验"
//     选项变更时会重建 transport，保证配置即时生效；
//   - dialUpstream 负责原始隧道/WebSocket 的上游拨号：无外部代理时直连，
//     有外部代理时先向其发送 CONNECT 建立隧道；
//   - relay 在两条连接间双向转发字节（用于原始隧道）。
package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"wproxyman/internal/models"
)

// upstreamTransport is a keep-alive HTTP transport for forwarding requests.
var (
	upstreamTrMu      sync.RWMutex
	upstreamTr        *http.Transport
	upstreamProxy     *url.URL
	upstreamInsecure  bool
)

// SetUpstreamProxy configures the upstream proxy used for forwarded requests
// and raw tunnels (external proxy support). Passing nil clears it.
func (s *Server) SetUpstreamProxy(proxyURL *url.URL) {
	upstreamTrMu.Lock()
	upstreamProxy = proxyURL
	rebuildLocked()
	upstreamTrMu.Unlock()
}

// SetUpstreamInsecure toggles upstream TLS certificate verification
// (the "Disable SSL Certificate Validation" option).
func (s *Server) SetUpstreamInsecure(insecure bool) {
	upstreamTrMu.Lock()
	upstreamInsecure = insecure
	rebuildLocked()
	upstreamTrMu.Unlock()
}

func rebuildLocked() {
	upstreamTr = buildTransport()
}

// UpstreamProxy returns the currently configured upstream proxy.
func (s *Server) UpstreamProxy() *url.URL {
	upstreamTrMu.RLock()
	defer upstreamTrMu.RUnlock()
	return upstreamProxy
}

// insecureUpstream reports whether upstream TLS verification is disabled.
func (s *Server) insecureUpstream() bool {
	upstreamTrMu.RLock()
	defer upstreamTrMu.RUnlock()
	return upstreamInsecure
}

// RoundTrip sends a request through the shared upstream transport
// (used by Compose/Repeater and other out-of-band requests).
func (s *Server) RoundTrip(ctx context.Context, req *http.Request) (*http.Response, error) {
	return s.transport().RoundTrip(req.WithContext(ctx))
}

// BuildOutgoingRequest converts a flow into an upstream http.Request.
func BuildOutgoingRequest(f *models.Flow) (*http.Request, error) {
	return requestToOutgoing(f)
}

// HeadersFromResponse converts response headers to the header model.
func HeadersFromResponse(resp *http.Response) []models.Header {
	return headerFromTextproto(resp.Header)
}

// transport returns the shared HTTP transport, rebuilding it when the
// upstream proxy/insecure settings changed.
func (s *Server) transport() *http.Transport {
	upstreamTrMu.RLock()
	tr := upstreamTr
	upstreamTrMu.RUnlock()
	if tr != nil {
		return tr
	}
	upstreamTrMu.Lock()
	defer upstreamTrMu.Unlock()
	if upstreamTr == nil {
		upstreamTr = buildTransport()
	}
	return upstreamTr
}

func buildTransport() *http.Transport {
	return &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			upstreamTrMu.RLock()
			p := upstreamProxy
			upstreamTrMu.RUnlock()
			return p, nil
		},
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS10,
			// Verify upstream certificates against system roots unless the
			// user disabled validation.
			InsecureSkipVerify: upstreamInsecure, //nolint:gosec // user-controlled
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// dialUpstream opens a TCP connection to the target, going through the
// configured upstream proxy when present.
func (s *Server) dialUpstream(hostPort string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p := s.UpstreamProxy()
	if p == nil {
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", hostPort)
	}
	// Tunnel through the external proxy.
	proxyConn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", p.Host)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + hostPort + " HTTP/1.1\r\nHost: " + hostPort + "\r\n"
	if p.User != nil {
		user := p.User.Username()
		pass, _ := p.User.Password()
		req += "Proxy-Authorization: Basic " + basicAuth(user, pass) + "\r\n"
	}
	req += "\r\n"
	if _, err := proxyConn.Write([]byte(req)); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	br := newBufferedReader(proxyConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	// Drain response headers
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			_ = proxyConn.Close()
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	if len(statusLine) < 12 || statusLine[9:12] != "200" {
		_ = proxyConn.Close()
		return nil, err
	}
	// Wrap conn so buffered bytes are not lost.
	return &bufferedConn{Conn: proxyConn, br: br}, nil
}

// relay copies bytes bidirectionally between two connections until either
// side closes.
func (s *Server) relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyDir := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Half-close write side if possible so the peer sees EOF.
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go copyDir(a, b)
	go copyDir(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}
