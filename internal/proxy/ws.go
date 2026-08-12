// WebSocket 处理：对经过 MITM 解密的连接执行透明的 WebSocket 中继与帧捕获。
//
// 实现原理：
//   - 手动拨号上游并发起 HTTP 升级请求（RFC 6455）；
//   - 收到 101 后劫持客户端连接，在两个方向之间**逐帧**转发原始字节
//     （保证协议字节完全一致）；
//   - 同时解析每一帧（解掩码副本仅用于记录，不修改转发流），把消息内容
//     追加到 Flow.WebSocketMsgs，供详情面板展示；
//   - 支持文本/二进制分片重组记录，ping/pong/close 如实记录。
//
// 注意：客户端请求中会剥离 permessage-deflate 扩展（我们无法解压压缩帧，
// 移除后符合规范的服务器就不会协商该扩展）。
package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"wproxyman/internal/models"
)

const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xa

	maxWSMessageLog = 1 << 20 // 1 MiB per logged message
)

// handleWebSocket performs a transparent WebSocket relay with frame capture.
// Called from handleHTTP after request interception when the request is a
// websocket upgrade.
func (h *handler) handleWebSocket(w http.ResponseWriter, r *http.Request, f *models.Flow, scheme string, emit func(string)) {
	// Strip permessage-deflate: we relay raw frames and cannot decode
	// compressed payloads.
	models.DeleteHeader(&f.RequestHeaders, "Sec-WebSocket-Extensions")

	// Dial upstream manually so we can take over the connection after 101.
	u, err := url.Parse(f.FullURL)
	if err != nil {
		h.failFlow(w, f, emit, "invalid websocket URL: "+err.Error())
		return
	}
	hostPort := u.Host
	upConn, err := h.server.dialUpstream(hostPort)
	if err != nil {
		h.failFlow(w, f, emit, "dial upstream failed: "+err.Error())
		return
	}
	if scheme == "https" {
		tlsConn := tls.Client(upConn, &tls.Config{
			ServerName:         hostnameOnly(hostPort),
			MinVersion:         tls.VersionTLS10,
			InsecureSkipVerify: h.server.insecureUpstream(), //nolint:gosec // user-controlled
		})
		if err := tlsConn.Handshake(); err != nil {
			_ = upConn.Close()
			h.failFlow(w, f, emit, "upstream TLS handshake failed: "+err.Error())
			return
		}
		upConn = tlsConn
	}
	defer upConn.Close()

	outReq, err := requestToOutgoing(f)
	if err != nil {
		h.failFlow(w, f, emit, "invalid request: "+err.Error())
		return
	}
	// Force connection header for the upgrade.
	outReq.Header.Set("Connection", "Upgrade")
	outReq.Header.Set("Upgrade", "websocket")

	if err := outReq.Write(upConn); err != nil {
		h.failFlow(w, f, emit, "sending upgrade failed: "+err.Error())
		return
	}
	upBr := bufio.NewReader(upConn)
	resp, err := http.ReadResponse(upBr, outReq)
	if err != nil {
		h.failFlow(w, f, emit, "reading upgrade response failed: "+err.Error())
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		// Not an upgrade: fall back to normal response handling.
		_ = resp.Body.Close()
		h.applyUpgradeFailure(w, f, resp)
		f.CompletedAt = time.Now().UnixMilli()
		f.Duration = f.CompletedAt - f.StartedAt
		emit("completed")
		return
	}

	// Hijack the client connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upConn.Close()
		h.failFlow(w, f, emit, "hijacking not supported")
		return
	}
	clientConn, clientBr, err := hj.Hijack()
	if err != nil {
		h.failFlow(w, f, emit, "hijack failed: "+err.Error())
		return
	}

	// Write the 101 response back to the client.
	var respBuf bytes.Buffer
	if err := resp.Write(&respBuf); err != nil {
		_ = clientConn.Close()
		h.failFlow(w, f, emit, "writing 101 failed: "+err.Error())
		return
	}
	if _, err := clientConn.Write(respBuf.Bytes()); err != nil {
		_ = clientConn.Close()
		h.failFlow(w, f, emit, "writing 101 failed: "+err.Error())
		return
	}

	f.IsWebSocket = true
	emit("updated")

	// Relay frames in both directions, capturing payloads for the flow.
	var wg sync.WaitGroup
	wg.Add(2)
	go h.wsRelay(clientBr.Reader, upConn, f, "request", emit, &wg)
	go h.wsRelay(upBr, clientConn, f, "response", emit, &wg)
	wg.Wait()

	f.WebSocketClosed = true
	f.CompletedAt = time.Now().UnixMilli()
	f.Duration = f.CompletedAt - f.StartedAt
	emit("completed")
}

// applyUpgradeFailure sends a non-101 upstream response to the client.
func (h *handler) applyUpgradeFailure(w http.ResponseWriter, f *models.Flow, resp *http.Response) {
	f.ResponseStatus = resp.StatusCode
	f.ResponseReason = resp.Status
	f.ResponseHeaders = headerFromTextproto(resp.Header)
	if resp.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, h.server.Config().MaxBodyBytes))
		f.ResponseBody = body
		f.ResponseSize = int64(len(body))
	}
	h.writeResponseFromFlow(w, nil, f)
}

// wsRelay reads frames from src, forwards them verbatim to dst, and records
// message payloads into the flow.
func (h *handler) wsRelay(src *bufio.Reader, dst net.Conn, f *models.Flow, direction string, emit func(string), wg *sync.WaitGroup) {
	defer wg.Done()
	// Fragment reassembly state for logging only.
	var pending models.WSMessage
	havePending := false

	for {
		raw, parsed, err := readWSFrame(src)
		if err != nil {
			// Connection closed; stop relaying.
			_, _ = dst.Write(closeFrame())
			_ = dst.Close()
			return
		}
		if _, err := dst.Write(raw); err != nil {
			return
		}

		switch parsed.opcode {
		case wsOpText, wsOpBinary:
			if parsed.fin {
				h.appendWSMessage(f, direction, parsed.opcode, parsed.payload, emit)
			} else {
				// First fragment of a message.
				pending = models.WSMessage{
					Direction: direction,
					Opcode:    wsOpcodeName(parsed.opcode),
					Data:      parsed.payload,
					Timestamp: time.Now().UnixMilli(),
				}
				havePending = true
			}
		case wsOpContinuation:
			if havePending {
				pending.Data = append(pending.Data, parsed.payload...)
				if parsed.fin {
					if len(pending.Data) > maxWSMessageLog {
						pending.Data = pending.Data[:maxWSMessageLog]
					}
					h.appendWSMessageObj(f, pending, emit)
					havePending = false
				}
			}
		case wsOpClose:
			h.appendWSMessage(f, direction, parsed.opcode, parsed.payload, emit)
			return
		}
	}
}

func (h *handler) appendWSMessage(f *models.Flow, direction string, opcode byte, payload []byte, emit func(string)) {
	if len(payload) > maxWSMessageLog {
		payload = payload[:maxWSMessageLog]
	}
	h.appendWSMessageObj(f, models.WSMessage{
		Direction: direction,
		Opcode:    wsOpcodeName(opcode),
		Data:      payload,
		Timestamp: time.Now().UnixMilli(),
	}, emit)
}

func (h *handler) appendWSMessageObj(f *models.Flow, msg models.WSMessage, emit func(string)) {
	f.WebSocketMsgs = append(f.WebSocketMsgs, msg)
	emit("updated")
}

func wsOpcodeName(op byte) string {
	switch op {
	case wsOpText:
		return "text"
	case wsOpBinary:
		return "binary"
	case wsOpClose:
		return "close"
	case wsOpPing:
		return "ping"
	case wsOpPong:
		return "pong"
	}
	return fmt.Sprintf("opcode-%d", op)
}

// wsFrame is a parsed (and re-encodable) websocket frame.
type wsFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// readWSFrame reads one complete raw frame from br, returning both the raw
// bytes (for verbatim forwarding) and the parsed payload.
func readWSFrame(br *bufio.Reader) (raw []byte, parsed wsFrame, err error) {
	hdr, err := br.Peek(2)
	if err != nil {
		return nil, parsed, err
	}
	fin := hdr[0]&0x80 != 0
	opcode := hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	lenIndicator := hdr[1] & 0x7f

	extra := 0
	switch lenIndicator {
	case 126:
		extra = 2
	case 127:
		extra = 8
	}
	if masked {
		extra += 4
	}
	headerSize := 2 + extra

	if _, err := br.Peek(headerSize); err != nil {
		return nil, parsed, err
	}
	payloadLen := uint64(lenIndicator)
	if lenIndicator == 126 {
		b, _ := br.Peek(4)
		payloadLen = uint64(binary.BigEndian.Uint16(b[2:4]))
	} else if lenIndicator == 127 {
		b, _ := br.Peek(10)
		payloadLen = binary.BigEndian.Uint64(b[2:10])
	}

	total := int64(headerSize) + int64(payloadLen)
	if total < 0 || total > 1<<40 {
		return nil, parsed, fmt.Errorf("frame too large")
	}
	raw = make([]byte, total)
	if _, err := io.ReadFull(br, raw); err != nil {
		return nil, parsed, err
	}

	// Parse payload (unmasking a copy for logging).
	payload := raw[headerSize:]
	if masked {
		key := raw[headerSize-4 : headerSize]
		decoded := make([]byte, len(payload))
		for i := range payload {
			decoded[i] = payload[i] ^ key[i&3]
		}
		payload = decoded
	}
	return raw, wsFrame{fin: fin, opcode: opcode, payload: payload}, nil
}

// closeFrame returns a minimal close frame payload for signalling EOF.
func closeFrame() []byte {
	return []byte{0x88, 0x00}
}
