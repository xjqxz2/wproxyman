// Package proxy 实现 WProxyman 的核心：自研 HTTP/HTTPS 拦截代理引擎。
//
// 架构概览：
//   - Server 管理监听生命周期（启动/停止/端口）；
//   - handler 分发请求：普通 HTTP 直接转发，CONNECT 则按域名决定
//     走原始隧道（tunnel）还是 MITM 解密（TLS 拦截 + HTTP/1.1 或 HTTP/2 解析）；
//   - 每个请求被构造为 models.Flow，经过工具管线（Interceptor）后转发上游，
//     响应再流回客户端——期间全程可被工具修改或暂停；
//   - 引擎为纯 Go 实现，与操作系统无关（三平台行为一致）。
package proxy

import (
	"crypto/tls"
	"net"
	"time"

	"wproxyman/internal/models"
)

// InterceptDecision 告诉代理在请求拦截后如何继续处理。
type InterceptDecision struct {
	// ShortCircuit：Flow 已携带完整响应，跳过上游往返直接返回
	//（用于 Map Local、Block List 等本地响应的工具）。
	ShortCircuit bool
	// Wait：Flow 已暂停，等待用户决策（断点）。
	Wait bool
	// UpstreamURL：覆盖上游目标地址（用于 Map Remote 重定向）。
	UpstreamURL string
	// SkipCapture：透明放行且不记录该请求（用于 Allow List）。
	SkipCapture bool
}

// Interceptor 是工具管线实现的接口。代理在请求生命周期的各阶段调用它。
type Interceptor interface {
	// OnRequest 在上游转发前执行。可以修改 Flow（头/体）、短路响应、或暂停等待。
	OnRequest(f *models.Flow) (*InterceptDecision, error)

	// OnResponse 在收到上游响应后、返回客户端前执行。可以修改 Flow 或暂停等待。
	OnResponse(f *models.Flow) error

	// WaitForDecision 阻塞直到被暂停的 Flow 被用户恢复（断点处理），
	// 并返回剩余管线阶段执行完毕后的最终决策。
	WaitForDecision(flowID string) (*InterceptDecision, error)
}

// FlowCallback 向监听方（服务层）通知 Flow 生命周期事件。
// phase 取值："started"、"completed"、"paused"、"updated"。
type FlowCallback func(f *models.Flow, phase string)

// Config 配置代理服务器。
type Config struct {
	// Port 监听端口；0 表示自动选择空闲端口。
	Port int
	// CA 用于签发按域名的 MITM 证书（由 internal/cert 实现）。
	CA interface {
		SignForHost(host string) (*tls.Certificate, error)
	}
	// SSLProxyEnabled 判断某个主机是否启用 MITM 解密。
	SSLProxyEnabled func(host string) bool
	// Interceptor 是工具管线（可为 nil）。
	Interceptor Interceptor
	// OnFlow 接收 Flow 生命周期事件（可为 nil）。
	OnFlow FlowCallback
	// WrapConn 可选地包装接受的客户端连接（网络限速等）。可为 nil。
	WrapConn func(c net.Conn) net.Conn
	// MaxBodyBytes 限制请求/响应体保留的最大字节数。
	MaxBodyBytes int64
	// RequestTimeout 限制单次上游请求的往返时长。
	RequestTimeout time.Duration
}
