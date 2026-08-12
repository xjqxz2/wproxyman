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
