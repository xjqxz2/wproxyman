// Server 是 HTTP/HTTPS 拦截代理服务器，负责监听生命周期管理。
//
// 设计要点：
//   - 监听在 127.0.0.1（本地代理，避免对外暴露）；
//   - 使用 net/http.Server 处理普通 HTTP 请求和 CONNECT；
//   - 维护活跃连接集合（conns），便于 Stop 时统一关闭；
//   - 所有接受进来的连接可被 Config.WrapConn 包装（网络限速）。
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server 是 HTTP/HTTPS 拦截代理服务器，负责监听生命周期管理。
type Server struct {
	cfg Config

	mu       sync.RWMutex
	ln       net.Listener
	httpSrv  *http.Server
	started  bool
	stopOnce sync.Once

	handler *handler

	// connMu 保护活跃连接集合（供 Stop 时强制关闭）。
	connMu sync.Mutex
	conns  map[net.Conn]struct{}
}

// NewServer 根据配置创建代理服务器，并填充默认值
//（默认 64 MiB 体积上限、120 秒请求超时）。
func NewServer(cfg Config) *Server {
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 64 << 20 // 64 MiB
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 120 * time.Second
	}
	s := &Server{
		cfg:   cfg,
		conns: make(map[net.Conn]struct{}),
	}
	s.handler = &handler{server: s}
	return s
}

// Start 开始监听。若 cfg.Port 为 0 则自动选择空闲端口。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("proxy already started")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy listen on %s: %w", addr, err)
	}
	s.ln = ln
	s.started = true

	s.httpSrv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       5 * time.Minute,
		ConnContext:       s.connContext,
	}
	serveLn := ln
	if s.cfg.WrapConn != nil {
		serveLn = &wrappedListener{Listener: ln, wrap: s.cfg.WrapConn}
	}
	go s.httpSrv.Serve(serveLn)
	return nil
}

// Stop shuts down the proxy and closes all active connections.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	srv := s.httpSrv
	s.httpSrv = nil
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	s.closeAllConns()
}

// IsRunning reports whether the proxy is currently listening.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

// Port returns the actual listening port (valid after Start).
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln == nil {
		return 0
	}
	return s.ln.Addr().(*net.TCPAddr).Port
}

// Addr returns the listening address string.
func (s *Server) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.Port())
}

// Config returns a copy of the server configuration.
func (s *Server) Config() Config {
	return s.cfg
}

func (s *Server) trackConn(c net.Conn, add bool) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if add {
		s.conns[c] = struct{}{}
	} else {
		delete(s.conns, c)
	}
}

func (s *Server) closeAllConns() {
	s.connMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.connMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// wrappedListener wraps accepted connections (for network throttling).
type wrappedListener struct {
	net.Listener
	wrap func(net.Conn) net.Conn
}

func (l *wrappedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.wrap(c), nil
}
