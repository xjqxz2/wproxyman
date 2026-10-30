// App 是 Wails 应用根对象（服务层）：所有导出方法都会被 Wails 自动生成为
// 前端可调用的 TypeScript API。职责包括：
//
//   - 生命周期：startup（初始化设置/CA/代理自动启动）、shutdown（清理）；
//   - 代理控制：启动/停止、系统代理开关、SSL 解密开关（按域名）；
//   - 证书管理：安装/移除/检测系统信任（crypt32 等平台 API）；
//   - 流量仓库：内存中的 Flow 列表 + 事件推送到前端；
//   - 事件泵：所有发往前端的事件经有界队列异步投递，避免阻塞代理 handler
//     （Windows 上 Wails 的 EventsEmit 是同步 ExecJS，直接调用会卡死）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"wproxyman/internal/cert"
	"wproxyman/internal/mcp"
	"wproxyman/internal/models"
	"wproxyman/internal/proxy"
	"wproxyman/internal/storage"
	"wproxyman/internal/systemproxy"
	"wproxyman/internal/tools"
)

// App 是 Wails 应用根对象（服务层），见文件头说明。
type App struct {
	ctx context.Context

	// mu 保护流量仓库与代理状态等共享数据（读写锁）。
	mu sync.RWMutex

	ca         *cert.CA            // 本地证书颁发机构
	trustStore cert.TrustStore     // 平台信任库（Win/mac/Linux）
	engine     *tools.Engine       // 工具管线引擎
	proxySrv   *proxy.Server       // 拦截代理服务器
	sysProxy   systemproxy.Manager // 系统代理管理器
	settings   Settings            // 持久化设置

	// 流量仓库（内存中已捕获的 Flow）
	flows    []*models.Flow          // 按捕获顺序追加
	flowIdx  map[string]*models.Flow // ID → Flow 索引
	flowsSeq int

	// SSL 解密状态：host → 是否启用；空 host 表示全局默认值。
	sslEnabledDefault bool
	sslHosts          map[string]bool

	// 证书信任状态缓存（每次 CONNECT 都查系统存储太慢）。
	certInstalled bool

	// lastProxyErr 记录最近一次启动代理失败的原因（典型场景：偏好端口被
	// 其他程序占用）。前端据此在设置面板给出"请改端口"的可操作提示。
	lastProxyErr string

	// MCP 服务（AI 客户端控制本应用）；未启用时均为 nil。
	mcpServer   *http.Server
	mcpListener net.Listener
	mcpService  *mcp.Service

	// cleanupOnce 保证清理逻辑（还原系统代理、停止代理、关闭事件泵）只执行一次：
	// 正常退出走 Wails 的 OnShutdown，被信号终止走 Cleanup，两者可能都触发。
	cleanupOnce sync.Once

	// 事件泵：Windows 上 EventsEmit 是同步 ExecJS，直接调用会阻塞代理
	// handler goroutine。所有事件都先入这个有界队列，由单个泵 goroutine
	// 异步投递到前端。
	eventCh  chan eventMsg
	stopPump chan struct{}
}

// eventMsg 是一条预序列化的事件（JSON 字节）+ 事件名。
type eventMsg struct {
	name string
	data []byte
}

// NewApp 创建应用实例并初始化默认状态（工具引擎、SSL 默认开启等）。
func NewApp() *App {
	return &App{
		engine:            tools.NewEngine(),
		flowIdx:           make(map[string]*models.Flow),
		sslEnabledDefault: true,
		sslHosts:          make(map[string]bool),
		sysProxy:          systemproxy.NewManager(),
		eventCh:           make(chan eventMsg, 512),
		stopPump:          make(chan struct{}),
	}
}

// emitJSON enqueues a pre-marshaled event without blocking the caller.
func (a *App) emitJSON(name string, data []byte) {
	select {
	case a.eventCh <- eventMsg{name: name, data: data}:
	default:
		// Queue full: drop the event rather than stall the proxy.
	}
}

// startEventPump drains the event queue and dispatches to the frontend.
func (a *App) startEventPump() {
	go func() {
		for {
			select {
			case <-a.stopPump:
				return
			case msg := <-a.eventCh:
				if a.ctx != nil {
					wruntime.EventsEmit(a.ctx, msg.name, json.RawMessage(msg.data))
				}
			}
		}
	}()
}

// emit marshals data and enqueues it for the frontend (never blocks).
func (a *App) emit(event string, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	a.emitJSON(event, b)
}

// startup initializes services when the app launches.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startEventPump()

	// Load settings
	a.settings = loadSettings()

	// Certificate authority
	caDir := filepath.Join(configDir(), "cert")
	a.ca, _ = cert.LoadOrCreate(caDir, cert.Options{})
	a.trustStore = cert.NewTrustStore()

	// Apply persisted tool configuration
	cfg := a.settings.ToolConfig
	if cfg != nil {
		a.engine.SetConfig(cfg)
	}
	a.applyExternalProxy()

	// Restore SSL host settings
	a.sslEnabledDefault = a.settings.SSLEnabledDefault
	a.sslHosts = a.settings.SSLHosts

	// Auto-start the proxy if it was running when the app closed, then make
	// sure the OS system proxy points at the current port.
	// 端口是固定的（默认 10086），因此启动失败的常见原因只有"端口被占用"：
	// 此时保留配置不动，前端会拿到 lastProxyErr 提示用户改端口。
	if a.settings.AutoStartProxy {
		if _, err := a.startProxy(0); err != nil {
			println("WProxyman: failed to start proxy:", err.Error())
			// 崩溃/强杀后系统代理可能还指着我们，而这次代理没起来 —— 必须还原，
			// 否则用户表现为"完全没网"。
			if a.healStaleSystemProxy() {
				println("WProxyman: stale system proxy restored")
			}
		} else if err := a.SetSystemProxyEnabled(true); err != nil {
			println("WProxyman: failed to set system proxy:", err.Error())
		}
	} else if a.healStaleSystemProxy() {
		println("WProxyman: stale system proxy restored")
	}
	// 启用时按设置把 MCP 服务挂到回环地址（失败不阻断应用启动）。
	if a.settings.MCPEnabled {
		if err := a.startMCPServer(); err != nil {
			println("WProxyman: failed to start MCP server:", err.Error())
		}
	}
	// Refresh the cached certificate-trust state.
	a.refreshCertInstalled()
	// Windows：证书写入用户根存储无弹框，可静默自动安装。
	// macOS / Linux：安装会触发系统授权（输入密码），若检测不可靠会每次
	// 启动都弹框——改为由用户手动安装一次（设置 → Install Certificate），
	// 装好后检测为已安装，之后启动不再触发任何授权。
	if runtime.GOOS == "windows" {
		go func() {
			if !a.certInstalled {
				if err := a.InstallCertificate(); err != nil {
					println("WProxyman: certificate auto-install failed:", err.Error())
				}
			}
		}()
	}
	// 定时轮询证书信任状态：用户安装证书时（尤其 macOS 授权输入密码需要
	// 时间，或通过应用外的方式安装）可能需要几十秒，轮询保证 UI 自动更新。
	go a.watchCertStatus()
	a.emitProxyStatus()
	wruntime.EventsEmit(a.ctx, "app:ready", map[string]interface{}{
		"proxyRunning": a.proxySrv != nil && a.proxySrv.IsRunning(),
	})

	// Show the window once the frontend reports it has finished rendering
	// (avoids the startup black-frame flash). WindowShow is idempotent.
	wruntime.EventsOn(a.ctx, "ui:ready", func(...interface{}) {
		wruntime.WindowShow(a.ctx)
	})
}

// domReady shows the window as a safety net if the frontend never signals
// "ui:ready" (e.g. an unexpected render failure).
func (a *App) domReady(ctx context.Context) {
	go func() {
		time.Sleep(3 * time.Second)
		wruntime.WindowShow(ctx)
	}()
}

// refreshCertInstalled updates the cached trust state.
func (a *App) refreshCertInstalled() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ca != nil {
		ok, _ := a.trustStore.IsInstalled(a.ca.CertFile())
		a.certInstalled = ok
	}
}

// watchCertStatus 周期性检查证书信任状态；状态变化时通知前端刷新界面。
func (a *App) watchCertStatus() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopPump:
			return
		case <-ticker.C:
			before := a.certTrusted()
			a.refreshCertInstalled()
			after := a.certTrusted()
			if before != after {
				a.emit("cert:status", map[string]bool{"installed": after})
			}
		}
	}
}

// certTrusted reports whether the CA is trusted (cached, lock-free read of an
// immutable-ish bool).
func (a *App) certTrusted() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.certInstalled
}

// shutdown stops the proxy and restores the system proxy on exit.
//
// 关键：退出前必须还原系统代理，否则用户会表现为"完全没网"。
// 判断"是不是我们设的"依赖持久化记录（settings.SystemProxyTarget），
// 而不是 Manager.Current()——后者在 macOS/Linux 上读不到，曾导致
// macOS 上退出时永远不还原。
func (a *App) shutdown(ctx context.Context) {
	a.cleanupOnce.Do(func() {
		close(a.stopPump)
		if a.systemProxyIsOurs() {
			_ = a.clearSystemProxy()
		}
		if a.proxySrv != nil {
			a.proxySrv.Stop()
		}
		a.stopMCPServer()
	})
}

// Cleanup 执行与正常退出相同的清理，供信号处理等非 Wails 退出路径调用
// （例如被 SIGTERM 终止时）。可重复调用。
func (a *App) Cleanup() {
	a.shutdown(context.Background())
}

// --- 系统代理所有权判断与收敛点 ---

// systemProxyAddr 返回我们写入系统代理时使用的地址。
func systemProxyAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// systemProxyIntent 返回用户是否希望抓包时接管系统代理。
func (a *App) systemProxyIntent() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settings.SystemProxyEnabled
}

// systemProxyTarget 返回我们最后写入的系统代理地址（空 = 没设过）。
func (a *App) systemProxyTarget() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settings.SystemProxyTarget
}

// systemProxyPointsTo 判断系统代理当前是否正指向 addr。
// 平台读不到当前值（返回空串）时按"否"处理，由调用方决定是否重新写入。
func (a *App) systemProxyPointsTo(addr string) bool {
	cur := a.sysProxy.Current()
	if cur == "" {
		return false
	}
	return systemproxy.Normalize(cur) == systemproxy.Normalize(addr)
}

// systemProxyIsOurs 判断系统代理当前是否由我们设置（决定退出/停止时是否还原）。
//
// 规则：
//  1. 有持久化记录（SystemProxyTarget）→ 以记录为准；若平台能读到当前值且
//     当前值不是我们的地址，说明已被别的程序改写，此时不动它（不抢别人的代理）；
//  2. 没有记录（旧版本遗留）→ 只能靠平台读到的当前值与我们配置端口比对，
//     读不到就不动（宁可不还原，也不误清别人的代理）。
func (a *App) systemProxyIsOurs() bool {
	target := a.systemProxyTarget()
	if target == "" {
		a.mu.RLock()
		port := normalizeProxyPort(a.settings.ProxyPort)
		a.mu.RUnlock()
		if port == 0 || !a.sysProxy.Status() {
			return false
		}
		return a.systemProxyPointsTo(systemProxyAddr(port))
	}
	cur := a.sysProxy.Current()
	if cur == "" {
		return true
	}
	return systemproxy.Normalize(cur) == systemproxy.Normalize(target)
}

// applySystemProxy 把系统代理指向当前代理端口，并记录"我们设过它"。
func (a *App) applySystemProxy() error {
	if !a.IsProxyRunning() {
		return fmt.Errorf("代理未运行：请先启动代理（端口 %d），再开启系统代理", a.GetProxyPort())
	}
	port := a.GetProxyPort()
	addr := systemProxyAddr(port)
	// 已经指向我们就只补记录，避免重复调用平台命令（macOS 上要遍历所有网络服务）。
	if !a.systemProxyPointsTo(addr) {
		if err := a.sysProxy.Apply(systemproxy.Proxy{
			Host:   "127.0.0.1",
			Port:   port,
			Bypass: []string{"localhost", "127.0.0.1", "::1"},
		}); err != nil {
			a.emitProxyStatus()
			return err
		}
	}
	a.mu.Lock()
	a.settings.SystemProxyEnabled = true
	a.settings.SystemProxyTarget = addr
	a.mu.Unlock()
	_ = a.saveSettings()
	a.emitProxyStatus()
	return nil
}

// clearSystemProxy 关闭系统代理并清除"我们设过它"的记录（保留用户意图，
// 以便下次抓包时自动重新接管）。
func (a *App) clearSystemProxy() error {
	err := a.sysProxy.Clear()
	a.mu.Lock()
	a.settings.SystemProxyTarget = ""
	a.mu.Unlock()
	_ = a.saveSettings()
	a.emitProxyStatus()
	return err
}

// healStaleSystemProxy 还原"残留"的系统代理：系统代理还指向我们，但代理并没有
// 在运行（崩溃/强杀后启动失败、或本次不自动启动代理）。
// 不做这件事的话，用户看到的是"打开电脑就没网"。
// 返回值表示是否执行了还原。
func (a *App) healStaleSystemProxy() bool {
	if a.IsProxyRunning() || !a.systemProxyIsOurs() {
		return false
	}
	_ = a.clearSystemProxy()
	return true
}

// --- Proxy control ---

// bindHost 返回当前应监听的地址（调用方需持有 a.mu）。
// AllowLAN 开启时监听 0.0.0.0，允许局域网设备（手机/模拟器）接入抓包；
// 否则保持 127.0.0.1 回环（不对局域网暴露）。
func (a *App) bindHost() string {
	if a.settings.AllowLAN {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// proxyListenError 把监听失败包装成用户可操作的提示（重点：告诉用户去改端口）。
func proxyListenError(port int, err error) error {
	return fmt.Errorf("端口 %d 无法使用，可能已被其他程序占用（%v）。请改填其他端口后重试", port, err)
}

// canBind 探测端口当前能否绑定（只探测，随即释放，不长期占用）。
func canBind(host string, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// startProxy boots the interception proxy on the given port.
//
// 端口语义（固定端口，避免手机/模拟器上填写的代理地址失效）：
//
//	port < 0  → 非法，返回错误；
//	port == 0 → 使用设置中的端口（未设置或历史值 0 时回落 DefaultProxyPort）；
//	port > 0  → 绑定该端口。
//
// 端口被占用时直接返回错误——不自动顺延、不随机分配，由用户在设置里改端口。
func (a *App) startProxy(port int) (int, error) {
	a.mu.Lock()
	// 解析目标端口（settings.ProxyPort 需在锁内读取）。
	want := port
	if want == 0 {
		want = normalizeProxyPort(a.settings.ProxyPort)
	}
	if !validProxyPort(want) {
		a.mu.Unlock()
		return 0, fmt.Errorf("端口 %d 非法：请填写 %d-%d 之间的整数", port, MinProxyPort, MaxProxyPort)
	}

	// 已在运行：端口相同则复用；端口不同则迁移到新端口。
	if a.proxySrv != nil && a.proxySrv.IsRunning() {
		cur := a.proxySrv.Port()
		if cur == want {
			a.mu.Unlock()
			return cur, nil
		}
		// 先探测新端口能否绑定：不能则报错并保持原代理继续运行（不打断抓包）。
		if err := canBind(a.bindHost(), want); err != nil {
			a.mu.Unlock()
			return 0, proxyListenError(want, err)
		}
		srv := a.proxySrv
		a.proxySrv = nil
		a.mu.Unlock()
		srv.Stop()
		if _, err := a.startProxy(want); err != nil {
			// 迁移失败：尽力恢复到原端口，避免代理停摆。
			if _, rerr := a.startProxy(cur); rerr == nil {
				return 0, fmt.Errorf("%v（已恢复原端口 %d）", err, cur)
			}
			return 0, err
		}
		return want, nil
	}

	bindHost := a.bindHost()
	// 启用内置证书下载端点：手机可访问 http://<电脑IP>:<port>/wpx-cert.pem
	// 下载并安装 CA（等价于 Proxyman 的证书安装页）。CA 缺失时置空禁用。
	var certPEM []byte
	if a.ca != nil {
		certPEM = a.ca.CertPEM()
	}
	srv := proxy.NewServer(proxy.Config{
		Port:        want,
		BindHost:    bindHost,
		CA:          a.ca,
		CACertPEM:   certPEM,
		OnFlow:      a.onFlow,
		OnStream:    a.onStream,
		WrapConn:    a.engine.WrapConn,
		Interceptor: a.engine,
		SSLProxyEnabled: func(host string) bool {
			// Only MITM when the CA is trusted by the client; otherwise the
			// HTTPS flow would break with certificate errors. Fall back to a
			// raw tunnel so traffic still works.
			if !a.certTrusted() {
				return false
			}
			a.mu.RLock()
			defer a.mu.RUnlock()
			if v, ok := a.sslHosts[host]; ok {
				return v
			}
			return a.sslEnabledDefault
		},
	})
	if err := srv.Start(); err != nil {
		err = proxyListenError(want, err)
		a.lastProxyErr = err.Error()
		a.mu.Unlock()
		// 失败也要同步一次状态，前端才能拿到 lastProxyErr 做提示。
		a.emitProxyStatus()
		return 0, err
	}
	a.proxySrv = srv
	// 只在绑定成功后记录端口；端口不会再漂移，这里存的就是用户选择的端口。
	a.settings.ProxyPort = want
	a.settings.AutoStartProxy = true
	a.lastProxyErr = ""
	actual := want
	a.mu.Unlock()

	_ = a.saveSettings()
	// 暂停后再恢复抓包：如果用户此前让代理接管了系统代理，重新指向当前端口
	// （端口可能已被用户改过）。失败不阻断抓包，仅记录。
	if a.systemProxyIntent() && !a.systemProxyPointsTo(systemProxyAddr(actual)) {
		if err := a.applySystemProxy(); err != nil {
			println("WProxyman: failed to restore system proxy:", err.Error())
		}
	}
	// NOTE: must not call a.mu-protected helpers while holding the lock above
	// (emitProxyStatus → IsProxyRunning → RLock would self-deadlock).
	a.emitProxyStatus()
	return actual, nil
}

// StartProxy starts the proxy (frontend-facing).
func (a *App) StartProxy(port int) (int, error) {
	return a.startProxy(port)
}

// StopProxy stops the proxy. Pausing is a session-only state: the app always
// resumes capturing on the next launch (Proxyman behavior), so we never
// persist autoStartProxy=false.
//
// 停止抓包必须同时还原系统代理：否则整机流量会打到一个已经没人监听的端口上
// （表现为"断网"）。用户的接管意图会保留，下次启动抓包时自动重新接管。
func (a *App) StopProxy() error {
	a.mu.Lock()
	if a.proxySrv == nil {
		a.mu.Unlock()
		return nil
	}
	srv := a.proxySrv
	a.proxySrv = nil
	a.mu.Unlock()
	srv.Stop()
	if a.systemProxyIsOurs() {
		_ = a.clearSystemProxy()
	}
	a.emitProxyStatus()
	return nil
}

// IsProxyRunning reports proxy state.
func (a *App) IsProxyRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.proxySrv != nil && a.proxySrv.IsRunning()
}

// GetProxyPort returns the current proxy port. 代理未运行时返回配置端口
// （固定为偏好端口，默认 10086），便于界面展示"将使用哪个端口"。
func (a *App) GetProxyPort() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.proxySrv != nil && a.proxySrv.IsRunning() {
		return a.proxySrv.Port()
	}
	return normalizeProxyPort(a.settings.ProxyPort)
}

// GetProxyError 返回最近一次启动代理失败的原因（空串表示无错误）。
func (a *App) GetProxyError() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastProxyErr
}

// normalizeFlow guarantees slice fields are never nil: Go marshals nil
// slices to JSON `null`, and the frontend assumes arrays (e.g. headers.map).
// Failed/errored flows often leave these unset.
func normalizeFlow(f *models.Flow) {
	if f.RequestHeaders == nil {
		f.RequestHeaders = []models.Header{}
	}
	if f.ResponseHeaders == nil {
		f.ResponseHeaders = []models.Header{}
	}
	if f.RequestCookies == nil {
		f.RequestCookies = []models.Cookie{}
	}
	if f.ResponseCookies == nil {
		f.ResponseCookies = []models.Cookie{}
	}
	if f.WebSocketMsgs == nil {
		f.WebSocketMsgs = []models.WSMessage{}
	}
}

// onFlow is the proxy's flow callback; it records flows and emits events.
func (a *App) onFlow(f *models.Flow, phase string) {
	normalizeFlow(f)
	switch phase {
	case "started":
		a.mu.Lock()
		a.flowIdx[f.ID] = f
		a.flows = append(a.flows, f)
		a.flowsSeq++
		a.mu.Unlock()
		a.emit("flow:new", f)
	case "paused":
		a.emit("flow:paused", f)
	case "updated":
		a.emit("flow:updated", f)
	case "completed":
		a.mu.Lock()
		a.flowIdx[f.ID] = f
		a.mu.Unlock()
		a.emit("flow:completed", f)
	}
}

// onStream 推送流式响应（SSE / NDJSON）的增量事件。
//
// 不重推整条 flow：流式响应期间正文与事件都在增长，整条重推会让桥接数据量
// 随流长度平方级放大。前端收到增量后自行追加，流结束时再由 "completed"
// 一次性带上完整（受上限约束的）内容。
func (a *App) onStream(f *models.Flow, msgs []models.SSEMessage) {
	a.emit("flow:stream", map[string]interface{}{
		"id":                f.ID,
		"messages":          msgs,
		"size":              f.ResponseSize,
		"firstByteAt":       f.FirstByteAt,
		"streamKind":        f.StreamKind,
		"truncated":         f.StreamTruncated,
		"responseTruncated": f.ResponseTruncated,
	})
}

func (a *App) emitProxyStatus() {
	a.emit("proxy:status", map[string]interface{}{
		"running":     a.IsProxyRunning(),
		"port":        a.GetProxyPort(),
		"systemProxy": a.sysProxy.Status(),
		"error":       a.GetProxyError(),
	})
}

// --- System proxy ---

// SetSystemProxyEnabled enables/disables the OS proxy pointing at our port.
//
// 开启/关闭都收敛到 applySystemProxy / clearSystemProxy 两个内部方法，
// 保证"记录我们写过什么"与系统设置始终一致（退出与崩溃自愈都靠这个记录）。
func (a *App) SetSystemProxyEnabled(enabled bool) error {
	if !enabled {
		a.mu.Lock()
		a.settings.SystemProxyEnabled = false
		a.mu.Unlock()
		return a.clearSystemProxy()
	}
	// 代理没在运行时把系统代理指向它，会让整机流量打到无人监听的端口上。
	return a.applySystemProxy()
}

// GetSystemProxyEnabled reports whether the OS proxy is set.
func (a *App) GetSystemProxyEnabled() bool {
	return a.sysProxy.Status()
}

// --- SSL proxying ---

// SetSSLProxyEnabled enables/disables MITM for a specific host ("" = all).
func (a *App) SetSSLProxyEnabled(host string, enabled bool) {
	a.mu.Lock()
	if host == "" {
		a.sslEnabledDefault = enabled
		a.settings.SSLEnabledDefault = enabled
	} else {
		a.sslHosts[host] = enabled
		a.settings.SSLHosts[host] = enabled
	}
	a.mu.Unlock()
	_ = a.saveSettings()
}

// GetSSLProxyState returns the per-host SSL state plus the default.
func (a *App) GetSSLProxyState() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	hosts := make(map[string]bool, len(a.sslHosts))
	for k, v := range a.sslHosts {
		hosts[k] = v
	}
	return map[string]interface{}{
		"default": a.sslEnabledDefault,
		"hosts":   hosts,
	}
}

// --- Certificates ---

// GetCACertPEM returns the PEM-encoded CA certificate.
func (a *App) GetCACertPEM() string {
	if a.ca == nil {
		return ""
	}
	return string(a.ca.CertPEM())
}

// InstallCertificate installs the CA into the OS trust store.
func (a *App) InstallCertificate() error {
	if a.ca == nil {
		return nil
	}
	err := a.trustStore.Install(a.ca.CertFile())
	// macOS 上授权/落盘可能有延迟，稍等再刷新检测。
	time.Sleep(1500 * time.Millisecond)
	a.refreshCertInstalled()
	installed := a.certTrusted()
	a.emit("cert:status", map[string]bool{"installed": installed})
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("安装命令已执行，但系统信任库中未检测到证书；请检查 macOS 授权是否完成（钥匙串访问中确认 WProxyman CA）")
	}
	return nil
}

// RemoveCertificate removes the CA from the OS trust store.
func (a *App) RemoveCertificate() error {
	if a.ca == nil {
		return nil
	}
	err := a.trustStore.Remove(a.ca.CertFile())
	a.refreshCertInstalled()
	a.emit("cert:status", map[string]bool{"installed": a.certTrusted()})
	return err
}

// IsCertificateInstalled reports whether the CA is trusted.
// 实时检测（不经缓存）：前端初始化、点击刷新、设置页面调用时总能拿到
// 真实状态，避免安装完成后界面仍显示旧缓存导致的"未安装"误报。
func (a *App) IsCertificateInstalled() bool {
	a.mu.RLock()
	ca := a.ca
	a.mu.RUnlock()
	if ca == nil {
		return false
	}
	ok, _ := a.trustStore.IsInstalled(ca.CertFile())
	// 同步缓存（供 MITM 的 CONNECT 决策使用，避免每连接都 exec）。
	a.mu.Lock()
	a.certInstalled = ok
	a.mu.Unlock()
	return ok
}

// --- Misc ---

// GetLANIPs lists non-loopback IPv4 addresses (for device setup).
func (a *App) GetLANIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, ok := addr.(*net.IPNet)
			if ok && ip.IP.To4() != nil {
				out = append(out, ip.IP.String())
			}
		}
	}
	return out
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	d := filepath.Join(dir, "wproxyman")
	_ = os.MkdirAll(d, 0o755)
	return d
}

var _ = storage.SaveSession // ensure storage import stays wired
