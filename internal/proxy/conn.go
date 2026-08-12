// 连接工具：bufferedConn 保留已被缓冲读取器消费的字节（避免数据丢失），
// 以及外部代理认证所需的 Basic 编码等小工具。
package proxy

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
)

// bufferedConn preserves bytes already read into the buffer.
type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.br.Read(p) }

func newBufferedReader(conn net.Conn) *bufio.Reader {
	return bufio.NewReader(conn)
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

var _ io.Reader = (*bufferedConn)(nil)
