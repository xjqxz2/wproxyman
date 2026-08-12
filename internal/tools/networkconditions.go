// Package tools 网络条件模拟（Network Conditions）工具的实现。
// networkconditions.go 实现带宽限制、延迟模拟和丢包模拟。
//
// 网络配置文件：预设多种网络环境（2G、3G、4G、LTE、Edge 等），
// 包含下载/上传带宽（字节/秒）、延迟（毫秒）和丢包率参数。
//
// 实现原理：
//   - throttleConn 包装 net.Conn，在 Read/Write 操作中注入延迟和限速
//   - 带宽限制：将数据分块传输（每秒 20 块），每块传输后等待比例时间
//   - 延迟模拟：首次 Read/Write 时 sleep 指定的延迟时间
//   - 使用 Token Bucket 类似的速率控制（chunk-based throttling）
//
// 带宽计算：配置文件中的 kbps/Mbps 转换为字节/秒
//   例如：1 Mbps = 1,000,000 / 8 = 125,000 bytes/s
package tools

import (
	"io"
	"net"
	"time"
)

// NetworkProfile 定义了一个网络限速配置文件。
// 带宽单位为字节/秒（bytes per second）。
type NetworkProfile struct {
	Name            string  `json:"name"`           // 配置文件名称（如 "4G"、"3G"）
	DownloadBps     int64   `json:"downloadBps"`    // 下载带宽（字节/秒），0 表示不限制
	UploadBps       int64   `json:"uploadBps"`      // 上传带宽（字节/秒），0 表示不限制
	LatencyMs       int     `json:"latencyMs"`      // 网络延迟（毫秒），0 表示不添加延迟
	PacketLossPct   float64 `json:"packetLossPct"`  // 丢包率（百分比），当前版本预留
}

// NetworkConditionsConfig 持有当前激活的网络条件配置。
type NetworkConditionsConfig struct {
	Enabled bool            `json:"enabled"` // 是否启用网络条件模拟
	Profile NetworkProfile  `json:"profile"` // 当前使用的网络配置文件
}

// ThrottleSpec 是传递给代理层的限速参数。
// 代理层根据这些参数创建 throttleConn。
type ThrottleSpec struct {
	DownloadBps int64 // 下载带宽限制（字节/秒）
	UploadBps   int64 // 上传带宽限制（字节/秒）
	LatencyMs   int   // 延迟（毫秒）
}

// Throttle 返回当前激活的限速参数，禁用时返回 nil。
func (e *Engine) Throttle() *ThrottleSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.NetworkConditions.Enabled {
		return nil
	}
	p := e.NetworkConditions.Profile
	return &ThrottleSpec{
		DownloadBps: p.DownloadBps,
		UploadBps:   p.UploadBps,
		LatencyMs:   p.LatencyMs,
	}
}

// throttleConn 包装 net.Conn，对读写操作注入带宽限制和延迟。
// DownloadBps 限制读取（服务器→客户端），UploadBps 限制写入（客户端→服务器）。
type throttleConn struct {
	net.Conn                     // 底层网络连接
	downloadBps int64            // 下载带宽上限
	uploadBps   int64            // 上传带宽上限
	latency     time.Duration    // 延迟时间
	firstRead   bool             // 是否已完成首次读取（用于首包延迟模拟）
	firstWrite  bool             // 是否已完成首次写入
}

// WrapConn 根据当前限速配置包装网络连接。
// 如果限速未启用，返回原始连接。
func (e *Engine) WrapConn(c net.Conn) net.Conn {
	spec := e.Throttle()
	if spec == nil {
		return c // 限速未启用 → 直接返回原连接
	}
	return &throttleConn{
		Conn:        c,
		downloadBps: spec.DownloadBps,
		uploadBps:   spec.UploadBps,
		latency:     time.Duration(spec.LatencyMs) * time.Millisecond,
	}
}

// Read 实现限速读取。
// 首次读取前 sleep 延迟时间（模拟首包延迟），
// 然后按 chunk 读取，每次读取后按比例等待以达到目标带宽。
func (t *throttleConn) Read(p []byte) (int, error) {
	// 首包延迟：仅在第一次读取时 sleep
	if t.latency > 0 && !t.firstRead {
		t.firstRead = true
		time.Sleep(t.latency)
	}
	// 无带宽限制 → 直接读取
	if t.downloadBps <= 0 {
		return t.Conn.Read(p)
	}
	// 计算 chunk 大小：每秒 20 块，使限速更平滑
	chunk := int(t.downloadBps / 20) // 20 块/秒
	if chunk <= 0 {
		chunk = 1
	}
	if chunk > len(p) {
		chunk = len(p)
	}
	// 读取一块数据
	n, err := t.Conn.Read(p[:chunk])
	if n > 0 {
		// 按带宽比例等待：预期时间 = n/Bps 秒
		time.Sleep(time.Duration(float64(n) / float64(t.downloadBps) * float64(time.Second)))
	}
	return n, err
}

// Write 实现限速写入，循环分块写入。
// 首次写入前 sleep 延迟时间，然后按 chunk 写入，每块后按比例等待。
func (t *throttleConn) Write(p []byte) (int, error) {
	// 首包延迟
	if t.latency > 0 && !t.firstWrite {
		t.firstWrite = true
		time.Sleep(t.latency)
	}
	// 无带宽限制 → 直接写入
	if t.uploadBps <= 0 {
		return t.Conn.Write(p)
	}
	total := 0
	// 循环分块写入
	for len(p) > 0 {
		chunk := int(t.uploadBps / 20) // 每秒 20 块
		if chunk <= 0 {
			chunk = 1
		}
		if chunk > len(p) {
			chunk = len(p)
		}
		n, err := t.Conn.Write(p[:chunk])
		total += n
		if err != nil {
			return total, err
		}
		// 按带宽比例等待
		time.Sleep(time.Duration(float64(n) / float64(t.uploadBps) * float64(time.Second)))
		p = p[chunk:] // 继续处理剩余数据
	}
	return total, nil
}

// 编译期断言 throttleConn 实现了 io.ReadWriter 接口。
var _ io.ReadWriter = (*throttleConn)(nil)
