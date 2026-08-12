package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"wproxyman/internal/models"
)

// TestMITMHTTP2 verifies that HTTP/2 is negotiated over the intercepted
// connection and that h2 flows are captured correctly.
func TestMITMHTTP2(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, true)
	srv.SetUpstreamInsecure(true)

	// HTTP/2-capable backend.
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "h2:%s:%s", r.Proto, r.URL.Path)
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(r *http.Request) (*url.URL, error) {
				return url.Parse("http://" + srv.Addr())
			},
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: pool},
		},
		Timeout: 15 * time.Second,
	}

	resp, err := client.Get(backend.URL + "/hello")
	if err != nil {
		t.Fatalf("h2 GET via MITM: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "h2:HTTP/2.0:/hello" {
		t.Fatalf("unexpected body: %q", body)
	}

	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if f.HTTPVersion != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2.0 flow, got %q", f.HTTPVersion)
	}
	if !f.TLS || f.Scheme != "https" {
		t.Fatalf("expected https flow, got %s tls=%v", f.Scheme, f.TLS)
	}
	if f.Path != "/hello" {
		t.Fatalf("unexpected path: %s", f.Path)
	}
	if f.ResponseStatus != 200 {
		t.Fatalf("expected 200, got %d", f.ResponseStatus)
	}
	t.Log("h2 flow captured:", f.HTTPVersion, f.Method, f.FullURL)
}

// TestMITMHTTP2PostBody verifies request bodies flow over h2.
func TestMITMHTTP2PostBody(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, true)
	srv.SetUpstreamInsecure(true)

	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Write(b)
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(r *http.Request) (*url.URL, error) {
				return url.Parse("http://" + srv.Addr())
			},
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: pool},
		},
		Timeout: 15 * time.Second,
	}

	req, _ := http.NewRequest("POST", backend.URL+"/submit", io.NopCloser(&sr{s: "h2-body-data"}))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("h2 POST: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "h2-body-data" {
		t.Fatalf("unexpected echo: %q", body)
	}

	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if string(f.RequestBody) != "h2-body-data" {
		t.Fatalf("request body not captured over h2: %q", f.RequestBody)
	}
}

type sr struct{ s string }

func (r *sr) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := copy(p, r.s)
	r.s = r.s[n:]
	return n, nil
}

var _ = time.Second
var _ = models.Flow{}
