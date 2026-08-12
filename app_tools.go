package main

// 文件说明： 工具联动：断点恢复（ResolveBreakpoint）、Compose/Repeater 请求发送

import (
	"context"
	"fmt"
	"time"

	"wproxyman/internal/models"
	"wproxyman/internal/proxy"
)

// ResolveBreakpoint resumes a paused flow with the user's modifications.
func (a *App) ResolveBreakpoint(flowID string, modified *models.Flow) error {
	err := a.engine.ResolveBreakpoint(flowID, modified)
	if err != nil {
		return err
	}
	// Restore the flow in the store (breakpoints mutate the original pointer,
	// but a fresh modified object may have been provided).
	a.mu.Lock()
	if modified != nil {
		a.flowIdx[flowID] = modified
		for i, f := range a.flows {
			if f.ID == flowID {
				a.flows[i] = modified
				break
			}
		}
	}
	a.mu.Unlock()
	a.emit("flow:resumed", flowID)
	return nil
}

// SendRequest performs a Compose/Repeater request outside the proxy.
func (a *App) SendRequest(f *models.Flow) (*models.Flow, error) {
	if f == nil || f.FullURL == "" {
		return nil, fmt.Errorf("no URL specified")
	}
	if f.Method == "" {
		f.Method = "GET"
	}
	f.ID = models.GenID()
	f.StartedAt = time.Now().UnixMilli()

	// Use a temporary proxy server to run the request through the tool
	// pipeline when the proxy is active; otherwise send directly.
	var srv *proxy.Server
	a.mu.RLock()
	srv = a.proxySrv
	a.mu.RUnlock()

	if srv != nil {
		return a.sendViaProxy(srv, f)
	}
	return a.sendDirect(f)
}

func (a *App) sendDirect(f *models.Flow) (*models.Flow, error) {
	cfg := proxy.Config{MaxBodyBytes: a.settings.MaxBodyBytes, RequestTimeout: 60 * time.Second}
	srv := proxy.NewServer(cfg)
	f = a.forwardOne(srv, f)
	f.CompletedAt = time.Now().UnixMilli()
	f.Duration = f.CompletedAt - f.StartedAt
	return f, nil
}

func (a *App) sendViaProxy(srv *proxy.Server, f *models.Flow) (*models.Flow, error) {
	return a.forwardOne(srv, f), nil
}

// forwardOne sends the flow through the shared upstream transport without
// exposing it to the interception listener.
func (a *App) forwardOne(srv *proxy.Server, f *models.Flow) *models.Flow {
	req, err := proxy.BuildOutgoingRequest(f)
	if err != nil {
		f.Error = err.Error()
		f.ResponseStatus = 400
		f.ResponseReason = "Bad Request"
		return f
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := srv.RoundTrip(ctx, req)
	if err != nil {
		f.Error = err.Error()
		f.ResponseStatus = 502
		f.ResponseReason = "Bad Gateway"
		return f
	}
	defer resp.Body.Close()
	f.ResponseStatus = resp.StatusCode
	f.ResponseReason = resp.Status
	f.ResponseHeaders = proxy.HeadersFromResponse(resp)
	body := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)
	max := a.settings.MaxBodyBytes
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if int64(len(body))+int64(n) > max {
				n = int(max - int64(len(body)))
				body = append(body, buf[:n]...)
				break
			}
			body = append(body, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	f.ResponseBody = body
	f.ResponseSize = int64(len(body))
	return f
}
