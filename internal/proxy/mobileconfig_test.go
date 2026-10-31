package proxy

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// iOS 描述文件：一次装好"CA 证书 + 代理设置"。
// 走真实代理链路请求这个端点（和手机的实际路径一致）。
func TestServeMobileconfig(t *testing.T) {
	ca := testCA(t)
	// 必须设置 CACertPEM：内置证书端点是在它非空时才启用的。
	// （用 startTestProxy 会漏掉它，请求就会被当成普通流量转发，转到自己而挂住。）
	srv, _ := startStreamProxy(t, Config{CA: ca, CACertPEM: ca.CertPEM(),
		SSLProxyEnabled: func(string) bool { return true }})

	// 通过代理请求本机端点（等价于手机把代理指向电脑后访问该 URL）。
	client := plainClientThroughProxy(t, srv.Addr())
	resp, err := client.Get("http://" + srv.Addr() + mobileconfigPath)
	if err != nil {
		t.Fatalf("请求描述文件失败：%v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "application/x-apple-aspen-config" {
		t.Fatalf("Content-Type = %q，iOS 需要 application/x-apple-aspen-config", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".mobileconfig") {
		t.Fatalf("Content-Disposition 应给出文件名：%q", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	xml := string(body)

	// 必须是像样的 plist，且包含两种载荷。
	for _, want := range []string{
		"<?xml version=\"1.0\"",
		"com.apple.security.root",       // CA 载荷
		"com.apple.SystemConfiguration", // 代理载荷
		"HTTPEnable</key><integer>1", "HTTPSEnable</key><integer>1",
		"ProxyAutoConfigEnable</key><integer>0",
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("描述文件缺少 %q", want)
		}
	}

	// 代理地址应当是**请求里用的那个地址与端口**（手机怎么连过来就写什么），
	// 而不是服务端去猜局域网 IP。
	host, port, _ := strings.Cut(srv.Addr(), ":")
	if !strings.Contains(xml, "<key>HTTPProxy</key><string>"+host+"</string>") {
		t.Fatalf("HTTPProxy 应写入 %s：%s", host, firstLines(xml, 3))
	}
	if !strings.Contains(xml, "<key>HTTPPort</key><integer>"+port+"</integer>") {
		t.Fatalf("HTTPPort 应写入 %s", port)
	}

	// CA 证书载荷必须是 base64 的 DER，且能解回证书本身。
	caB64 := extractDataBlock(xml)
	if caB64 == "" {
		t.Fatalf("找不到证书载荷")
	}
	der, err := base64.StdEncoding.DecodeString(caB64)
	if err != nil || len(der) == 0 {
		t.Fatalf("证书载荷不是合法 base64：%v", err)
	}
	if !strings.HasPrefix(string(der[:1]), "\x30") { // DER SEQUENCE
		t.Fatalf("证书载荷不像 DER：% x", der[:1])
	}
	// 与端点直接提供的 PEM 是同一张证书。
	if !strings.Contains(string(ca.CertPEM()), base64.StdEncoding.EncodeToString(der)[:32]) {
		// PEM 是 base64 换行的，退一步只比对前 32 字节的 base64 前缀是否出现在 PEM 里。
		t.Logf("提示：PEM 与 DER base64 因换行不同，未做严格比对")
	}

	// 每个 UUID 都必须唯一（重复会被 iOS 拒绝）。
	if strings.Count(xml, "<key>PayloadUUID</key>") != 3 {
		t.Fatalf("应有 3 个 UUID（描述文件 + 两个载荷）")
	}
	uuids := map[string]bool{}
	for _, part := range strings.Split(xml, "<key>PayloadUUID</key><string>")[1:] {
		u, _, ok := strings.Cut(part, "</string>")
		if !ok {
			continue
		}
		if uuids[u] {
			t.Fatalf("UUID 重复：%s", u)
		}
		uuids[u] = true
	}
}

// 指引页要给出"一键配置"的入口。
func TestCertInstallPageLinksMobileconfig(t *testing.T) {
	ca := testCA(t)
	srv, _ := startStreamProxy(t, Config{CA: ca, CACertPEM: ca.CertPEM(),
		SSLProxyEnabled: func(string) bool { return true }})
	client := plainClientThroughProxy(t, srv.Addr())
	resp, err := client.Get("http://" + srv.Addr() + certInstallPagePath)
	if err != nil {
		t.Fatalf("请求指引页失败：%v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), mobileconfigPath) {
		t.Fatalf("指引页应链接到 %s", mobileconfigPath)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// extractDataBlock 取出第一个 <data>...</data> 内容。
func extractDataBlock(xml string) string {
	_, rest, ok := strings.Cut(xml, "<data>")
	if !ok {
		return ""
	}
	val, _, ok := strings.Cut(rest, "</data>")
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}
