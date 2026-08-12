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
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"wproxyman/internal/cert"
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

	ca          *cert.CA           // 本地证书颁发机构
	trustStore  cert.TrustStore    // 平台信任库（Win/mac/Linux）
	engine      *tools.Engine      // 工具管线引擎
	proxySrv    *proxy.Server      // 拦截代理服务器
	sysProxy    systemproxy.Manager // 系统代理管理器
	settings    Settings           // 持久化设置

	// 流量仓库（内存中已捕获的 Flow）
	flows    []*models.Flow      // 按捕获顺序追加
	flowIdx  map[string]*models.Flow // ID → Flow 索引
	flowsSeq int

	// SSL 解密状态：host → 是否启用；空 host 表示全局默认值。
	sslEnabledDefault bool
	sslHosts          map[string]bool

	// 证书信任状态缓存（每次 CONNECT 都查系统存储太慢）。
	certInstalled bool

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
	// sure the OS system proxy points at the current port (the port may have
	// changed between runs, leaving a stale system proxy behind).
	if a.settings.AutoStartProxy {
		port, err := a.startProxy(a.settings.ProxyPort)
		if err == nil {
			a.settings.ProxyPort = port
			a.settings.AutoStartProxy = true
			_ = a.saveSettings()
			if err := a.SetSystemProxyEnabled(true); err != nil {
				println("WProxyman: failed to set system proxy:", err.Error())
			}
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

// shutdown stops the proxy and restores the system proxy on exit — but only
// if it still points at our own proxy (never clobber another tool's proxy).
func (a *App) shutdown(ctx context.Context) {
	close(a.stopPump)
	if a.proxySrv != nil {
		cur := a.sysProxy.Current()
		want := fmt.Sprintf("127.0.0.1:%d", a.proxySrv.Port())
		if cur == want || cur == "localhost:"+fmt.Sprint(a.proxySrv.Port()) {
			_ = a.sysProxy.Clear()
		}
		a.proxySrv.Stop()
	}
}

// --- Proxy control ---

// startProxy boots the interception proxy on the given port (0 = auto).
func (a *App) startProxy(port int) (int, error) {
	a.mu.Lock()
	if a.proxySrv != nil && a.proxySrv.IsRunning() {
		p := a.proxySrv.Port()
		a.mu.Unlock()
		return p, nil
	}
	srv := proxy.NewServer(proxy.Config{
		Port:      port,
		CA:        a.ca,
		OnFlow:    a.onFlow,
		WrapConn:  a.engine.WrapConn,
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
		a.mu.Unlock()
		return 0, err
	}
	a.proxySrv = srv
	a.settings.ProxyPort = srv.Port()
	a.settings.AutoStartProxy = true
	actual := srv.Port()
	a.mu.Unlock()

	_ = a.saveSettings()
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
	a.emitProxyStatus()
	return nil
}

// IsProxyRunning reports proxy state.
func (a *App) IsProxyRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.proxySrv != nil && a.proxySrv.IsRunning()
}

// GetProxyPort returns the current proxy port.
func (a *App) GetProxyPort() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.proxySrv == nil {
		return a.settings.ProxyPort
	}
	return a.proxySrv.Port()
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

func (a *App) emitProxyStatus() {
	a.emit("proxy:status", map[string]interface{}{
		"running":    a.IsProxyRunning(),
		"port":       a.GetProxyPort(),
		"systemProxy": a.sysProxy.Status(),
	})
}

// --- System proxy ---

// SetSystemProxyEnabled enables/disables the OS proxy pointing at our port.
func (a *App) SetSystemProxyEnabled(enabled bool) error {
	if !enabled {
		return a.sysProxy.Clear()
	}
	port := a.GetProxyPort()
	if port == 0 {
		return nil
	}
	err := a.sysProxy.Apply(systemproxy.Proxy{
		Host:   "127.0.0.1",
		Port:   port,
		Bypass: []string{"localhost", "127.0.0.1", "::1"},
	})
	a.emitProxyStatus()
	return err
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
	a.refreshCertInstalled()
	a.emit("cert:status", map[string]bool{"installed": a.certTrusted()})
	return err
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
func (a *App) IsCertificateInstalled() bool {
	return a.certTrusted()
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
