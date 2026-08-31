package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wproxyman/internal/cert"
	"wproxyman/internal/models"
)

// testCA creates a CA for tests.
func testCA(t *testing.T) *cert.CA {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cert")
	ca, err := cert.LoadOrCreate(dir, cert.Options{Org: "TestCA"})
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	return ca
}

// startTestProxy starts a proxy with the given SSL policy.
func startTestProxy(t *testing.T, ca *cert.CA, sslAll bool) (*Server, *[]*models.Flow) {
	t.Helper()
	var flows []*models.Flow
	srv := NewServer(Config{
		Port: 0,
		CA:   ca,
		SSLProxyEnabled: func(host string) bool {
			return sslAll
		},
		OnFlow: func(f *models.Flow, phase string) {
			if phase == "completed" {
				flows = append(flows, f)
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv, &flows
}

// httpClientThroughProxy returns a client whose requests go via the proxy
// and trust the test CA.
func httpClientThroughProxy(t *testing.T, proxyAddr string, ca *cert.CA) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	transport := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			return url.Parse("http://" + proxyAddr)
		},
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func TestPlainHTTP(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, false)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello %s %s", r.Method, r.URL.Path)
	}))
	defer backend.Close()

	client := httpClientThroughProxy(t, srv.Addr(), ca)
	resp, err := client.Get(backend.URL + "/ping")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello GET /ping" {
		t.Fatalf("unexpected body: %q", body)
	}
	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if f.Scheme != "http" || f.TLS {
		t.Fatalf("expected plain http flow, got scheme=%s tls=%v", f.Scheme, f.TLS)
	}
	if f.Method != "GET" || f.Path != "/ping" {
		t.Fatalf("unexpected flow: %s %s", f.Method, f.Path)
	}
	if f.ResponseStatus != 200 {
		t.Fatalf("expected 200, got %d", f.ResponseStatus)
	}
}

func TestMITMHTTPS(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, true)
	// The test backend is self-signed; the proxy must skip upstream
	// verification (mirrors Proxyman's "disable SSL validation" option).
	srv.SetUpstreamInsecure(true)

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "secure:%s", r.URL.Path)
	}))
	defer backend.Close()

	client := httpClientThroughProxy(t, srv.Addr(), ca)
	resp, err := client.Get(backend.URL + "/secret")
	if err != nil {
		t.Fatalf("GET via MITM: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "secure:/secret" {
		t.Fatalf("unexpected body: %q", body)
	}
	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if !f.TLS || f.Scheme != "https" {
		t.Fatalf("expected https flow, got scheme=%s tls=%v", f.Scheme, f.TLS)
	}
	if f.ResponseStatus != 200 {
		t.Fatalf("expected 200, got %d", f.ResponseStatus)
	}
}

func TestMITMDisabledFallsBackToTunnel(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, false) // no MITM

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tunneled"))
	}))
	defer backend.Close()

	// Client trusts the REAL backend cert (not our CA), proving raw tunneling.
	pool := x509.NewCertPool()
	pool.AddCert(backend.Certificate())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(r *http.Request) (*url.URL, error) {
				return url.Parse("http://" + srv.Addr())
			},
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
		Timeout: 15 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/t")
	if err != nil {
		t.Fatalf("GET via tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "tunneled" {
		t.Fatalf("unexpected body: %q", body)
	}
	// Tunneled flows are NOT recorded (transparent passthrough).
	if len(*flows) != 0 {
		t.Fatalf("expected 0 recorded flows, got %d", len(*flows))
	}
}

func TestRequestBodyAndPOST(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, false)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo", r.Header.Get("X-Test"))
		w.Write(b)
	}))
	defer backend.Close()

	client := httpClientThroughProxy(t, srv.Addr(), ca)
	req, _ := http.NewRequest("POST", backend.URL+"/submit", io.NopCloser(s2r("payload-data")))
	req.Header.Set("X-Test", "abc123")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "payload-data" {
		t.Fatalf("unexpected body: %q", body)
	}
	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if string(f.RequestBody) != "payload-data" {
		t.Fatalf("request body not captured: %q", f.RequestBody)
	}
	if models.HeaderValue(f.RequestHeaders, "X-Test") != "abc123" {
		t.Fatalf("request header not captured")
	}
	if models.HeaderValue(f.ResponseHeaders, "X-Echo") != "abc123" {
		t.Fatalf("response header not captured")
	}
}

func TestInterceptorShortCircuit(t *testing.T) {
	ca := testCA(t)
	interceptor := &fakeInterceptor{
		onReq: func(f *models.Flow) (*InterceptDecision, error) {
			if f.Path == "/mapped" {
				f.ResponseStatus = 200
				f.ResponseReason = "OK"
				f.ResponseBody = []byte("mapped!")
				f.ResponseHeaders = []models.Header{{Name: "Content-Type", Value: "text/plain"}}
				return &InterceptDecision{ShortCircuit: true}, nil
			}
			return nil, nil
		},
	}
	srv := NewServer(Config{
		Port:            0,
		CA:              ca,
		Interceptor:     interceptor,
		SSLProxyEnabled: func(string) bool { return false },
		OnFlow:          func(f *models.Flow, phase string) {},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream"))
	}))
	defer backend.Close()

	client := httpClientThroughProxy(t, srv.Addr(), ca)
	resp, err := client.Get(backend.URL + "/mapped")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "mapped!" {
		t.Fatalf("expected mapped response, got %q", body)
	}
}

// TestLANBindReachable 验证 BindHost="0.0.0.0" 时：
//   - 回环地址（127.0.0.1）仍然可达（0.0.0.0 包含回环）；
//   - 本机非回环 IPv4（局域网地址）可 TCP 直连代理端口，证明局域网设备能接入。
//
// 若本机没有非回环 IPv4（如 CI 隔离环境），跳过局域网断言但保留回环断言。
func TestLANBindReachable(t *testing.T) {
	ca := testCA(t)
	srv := NewServer(Config{
		Port:     0,
		BindHost: "0.0.0.0",
		CA:       ca,
		SSLProxyEnabled: func(host string) bool {
			return false
		},
		OnFlow: func(f *models.Flow, phase string) {},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy on 0.0.0.0: %v", err)
	}
	defer srv.Stop()

	port := srv.Port()

	// 回环仍可达。
	if err := dialTimeout("127.0.0.1", port); err != nil {
		t.Fatalf("loopback 127.0.0.1:%d should be reachable when bound to 0.0.0.0: %v", port, err)
	}

	// 局域网 IPv4 可达（若存在），并完成一次真实 HTTP 代理往返。
	ips := lanIPv4s(t)
	if len(ips) == 0 {
		t.Skip("no non-loopback IPv4 addresses on this machine; skipping LAN reachability assertions")
	}
	var lastErr error
	for _, ip := range ips {
		if err := dialTimeout(ip, port); err != nil {
			lastErr = err
			continue
		}
		// 通过局域网地址走完整 HTTP 代理链路（真实设备场景等价）。
		lanAddr := net.JoinHostPort(ip, strconv.Itoa(port))
		client := httpClientThroughProxy(t, lanAddr, ca)
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "lan-ok %s", r.Method)
		}))
		resp, err := client.Get(backend.URL + "/x")
		if err != nil {
			lastErr = err
			backend.Close()
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		backend.Close()
		if string(body) != "lan-ok GET" {
			t.Fatalf("via LAN addr %s: expected lan-ok GET, got %q", lanAddr, body)
		}
		return // 至少一个局域网地址完整可用即通过
	}
	t.Fatalf("none of the LAN addresses %v are reachable on 0.0.0.0:%d: %v", ips, port, lastErr)
}

// dialTimeout 对 host:port 发起 2 秒超时的 TCP 连接尝试。
func dialTimeout(host string, port int) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// lanIPv4s 列出本机非回环 IPv4 地址（与 app.go GetLANIPs 的筛选逻辑一致）。
func lanIPv4s(t *testing.T) []string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, ok := addr.(*net.IPNet)
			if ok && ip.IP.To4() != nil {
				out = append(out, ip.IP.String())
			}
		}
	}
	return out
}

// TestServeCACertDownload 验证代理内置的 CA 证书下载端点（手机/模拟器可通过
// http://<电脑IP>:<port>/wpx-cert.pem 下载并安装 CA，等价于 Proxyman 的证书页）：
//   - 配置了 CACertPEM 时，GET /wpx-cert.pem 返回 200 且正文为该 PEM；
//   - Content-Type 为 x-x509-ca-cert，便于手机识别为证书；
//   - 该请求不产生 Flow（不应污染流量列表）；
//   - 未配置 CACertPEM 时端点不启用（回退为普通转发，此处仅验证启用分支）。
func TestServeCACertDownload(t *testing.T) {
	ca := testCA(t)
	pem := ca.CertPEM()
	if len(pem) == 0 {
		t.Fatal("CA PEM should not be empty")
	}

	var flows []*models.Flow
	srv := NewServer(Config{
		Port:      0,
		CA:        ca,
		CACertPEM: pem,
		SSLProxyEnabled: func(host string) bool {
			return false
		},
		OnFlow: func(f *models.Flow, phase string) {
			if phase == "completed" {
				flows = append(flows, f)
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer srv.Stop()

	// 用代理客户端请求下载端点（等价于手机浏览器把代理指向本机后访问该 URL）。
	client := httpClientThroughProxy(t, srv.Addr(), ca)
	resp, err := client.Get("http://" + srv.Addr() + "/wpx-cert.pem")
	if err != nil {
		t.Fatalf("GET /wpx-cert.pem via proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Fatalf("expected application/x-x509-ca-cert content type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(pem) {
		t.Fatalf("expected body == CA PEM (%d bytes), got %d bytes", len(pem), len(body))
	}

	// 证书下载请求不应产生 Flow。
	time.Sleep(100 * time.Millisecond)
	if len(flows) != 0 {
		t.Fatalf("cert download should not create a flow, got %d", len(flows))
	}
}

// TestLANMITMHTTPS 模拟手机经局域网连到代理并解密 HTTPS 的场景：
// 代理绑定 0.0.0.0（AllowLAN 等价），经本机非回环 IPv4 走完整 MITM 链路，
// 客户端信任本地 CA，访问自签名 HTTPS 后端。若本机无局域网地址则跳过。
func TestLANMITMHTTPS(t *testing.T) {
	ips := lanIPv4s(t)
	if len(ips) == 0 {
		t.Skip("no non-loopback IPv4 addresses on this machine")
	}

	ca := testCA(t)
	var flows []*models.Flow
	srv := NewServer(Config{
		Port:     0,
		BindHost: "0.0.0.0",
		CA:       ca,
		SSLProxyEnabled: func(host string) bool {
			return true // 手机 HTTPS 需要 MITM 解密
		},
		OnFlow: func(f *models.Flow, phase string) {
			if phase == "completed" {
				flows = append(flows, f)
			}
		},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy on 0.0.0.0: %v", err)
	}
	defer srv.Stop()
	srv.SetUpstreamInsecure(true) // 与 TestMITMHTTPS 一致：上游自签名需跳过校验

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "secure-lan:%s", r.URL.Path)
	}))
	defer backend.Close()

	// 通过局域网地址连接代理（等价于手机把代理指向电脑 IP:端口）。
	lanAddr := net.JoinHostPort(ips[0], strconv.Itoa(srv.Port()))
	client := httpClientThroughProxy(t, lanAddr, ca)
	resp, err := client.Get(backend.URL + "/secret")
	if err != nil {
		t.Fatalf("GET via LAN MITM: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "secure-lan:/secret" {
		t.Fatalf("unexpected body: %q", body)
	}

	waitFlows(t, &flows, 1)
	f := flows[0]
	if !f.TLS || f.Scheme != "https" {
		t.Fatalf("expected https flow, got scheme=%s tls=%v", f.Scheme, f.TLS)
	}
	if f.ResponseStatus != 200 {
		t.Fatalf("expected 200, got %d", f.ResponseStatus)
	}
}

// --- helpers ---

type fakeInterceptor struct {
	onReq  func(f *models.Flow) (*InterceptDecision, error)
	onResp func(f *models.Flow) error
}

func (fi *fakeInterceptor) OnRequest(f *models.Flow) (*InterceptDecision, error) {
	if fi.onReq != nil {
		return fi.onReq(f)
	}
	return nil, nil
}
func (fi *fakeInterceptor) OnResponse(f *models.Flow) error {
	if fi.onResp != nil {
		return fi.onResp(f)
	}
	return nil
}
func (fi *fakeInterceptor) WaitForDecision(id string) (*InterceptDecision, error) {
	return nil, nil
}

func waitFlows(t *testing.T, flows *[]*models.Flow, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(*flows) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d flows, got %d", n, len(*flows))
}

func s2r(s string) io.Reader { return &stringReader{s: s} }

type stringReader struct {
	s string
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := copy(p, r.s)
	r.s = r.s[n:]
	return n, nil
}

var _ = net.Conn(nil)
var _ = os.File{}
