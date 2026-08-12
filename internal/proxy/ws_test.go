package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"wproxyman/internal/models"
)

func TestWebSocketCapture(t *testing.T) {
	ca := testCA(t)
	srv, flows := startTestProxy(t, ca, true)
	srv.SetUpstreamInsecure(true)

	// Echo server (TLS so it matches the MITM https scheme)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(mt, msg)
		}
	}))
	defer backend.Close()

	wsURL := "ws" + strings.TrimPrefix(backend.URL, "http") + "/ws"

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	dialer := websocket.Dialer{
		Proxy: func(r *http.Request) (*url.URL, error) {
			return url.Parse("http://" + srv.Addr())
		},
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v (status %v)", err, resp)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello-ws")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, reply, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(reply) != "hello-ws" {
		t.Fatalf("unexpected echo: %q", reply)
	}
	_ = conn.Close()

	// Wait for the flow to complete with WS messages captured.
	waitFlows(t, flows, 1)
	f := (*flows)[0]
	if !f.IsWebSocket {
		t.Fatalf("flow not marked as websocket")
	}
	var msgs []models.WSMessage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs = f.WebSocketMsgs
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected >=2 ws messages, got %d", len(msgs))
	}
	if string(msgs[0].Data) != "hello-ws" {
		t.Fatalf("unexpected first ws message: %q", msgs[0].Data)
	}
	if msgs[0].Direction != "request" || msgs[1].Direction != "response" {
		t.Fatalf("unexpected message directions: %s, %s", msgs[0].Direction, msgs[1].Direction)
	}
	fmt.Println("ws test ok, messages:", len(msgs))
}
