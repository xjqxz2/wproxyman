package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// mobileconfigPath 是 iOS 描述文件（.mobileconfig）的路径。
//
// 为什么值得做：iOS 上装"抓包证书 + 配置代理"原本要两步手工操作（下载证书 →
// 设置里装描述文件 → 再去证书信任设置开完全信任 → 回来手填代理地址）。一个
// 描述文件可以同时带上根证书载荷与代理载荷，装一次两步都完成。
const mobileconfigPath = "/wpx-mobileconfig"

// serveMobileconfig 生成并返回 iOS 配置描述文件：
//   - com.apple.security.root：CA 根证书（DER，base64）
//   - com.apple.SystemConfiguration：HTTP/HTTPS 代理指向本机
//
// 主机名与端口取自**请求本身**（r.Host）：手机是通过哪个地址连过来的，
// 描述文件里就写哪个地址——比在服务端猜局域网 IP 可靠（多网卡、虚拟机网卡
// 都可能猜错）。
func (h *handler) serveMobileconfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.server.Config()
	der, err := pemToDER(cfg.CACertPEM)
	if err != nil {
		http.Error(w, "invalid CA certificate: "+err.Error(), http.StatusInternalServerError)
		return
	}
	host, port := hostPortFromRequest(r, cfg.Port)
	if host == "" {
		http.Error(w, "cannot determine host; open this URL from the device itself", http.StatusBadRequest)
		return
	}

	profile := buildMobileconfig(host, port, der)
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="wproxyman.mobileconfig"`)
	w.Header().Set("Content-Length", fmt.Sprint(len(profile)))
	_, _ = w.Write([]byte(profile))
}

// hostPortFromRequest 从请求里取出手机实际连接的地址与端口。
func hostPortFromRequest(r *http.Request, fallbackPort int) (string, int) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		if n, err := net.LookupPort("tcp", p); err == nil {
			return h, n
		}
		return h, fallbackPort
	}
	return host, fallbackPort
}

// pemToDER 把 PEM 证书转成 DER（描述文件里证书载荷要 base64 的 DER）。
func pemToDER(certPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	return block.Bytes, nil
}

// buildMobileconfig 拼出描述文件 XML。
func buildMobileconfig(host string, port int, caDER []byte) string {
	caB64 := base64.StdEncoding.EncodeToString(caDER)
	// 描述文件里的 UUID 必须唯一，否则 iOS 会拒绝安装。
	uuid := func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		s := hex.EncodeToString(b)
		return strings.Join([]string{s[0:8], s[8:12], s[12:16], s[16:20], s[20:32]}, "-")
	}
	profileUUID, caPayloadUUID, proxyPayloadUUID := uuid(), uuid(), uuid()
	// host 只会是手机连接用的地址（IP 或主机名），做一次 XML 转义以防万一。
	safeHost := xmlEscape(host)

	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>PayloadCertificateFileName</key><string>wproxyman-ca.cer</string>
      <key>PayloadContent</key><data>` + caB64 + `</data>
      <key>PayloadDescription</key><string>WProxyman CA</string>
      <key>PayloadDisplayName</key><string>WProxyman CA</string>
      <key>PayloadIdentifier</key><string>com.wproxyman.ca</string>
      <key>PayloadType</key><string>com.apple.security.root</string>
      <key>PayloadUUID</key><string>` + caPayloadUUID + `</string>
      <key>PayloadVersion</key><integer>1</integer>
    </dict>
    <dict>
      <key>PayloadDescription</key><string>HTTP/HTTPS 代理指向 WProxyman</string>
      <key>PayloadDisplayName</key><string>WProxyman Proxy</string>
      <key>PayloadIdentifier</key><string>com.wproxyman.proxy</string>
      <key>PayloadType</key><string>com.apple.SystemConfiguration</string>
      <key>PayloadUUID</key><string>` + proxyPayloadUUID + `</string>
      <key>PayloadVersion</key><integer>1</integer>
      <key>Proxies</key>
      <dict>
        <key>HTTPEnable</key><integer>1</integer>
        <key>HTTPProxy</key><string>` + safeHost + `</string>
        <key>HTTPPort</key><integer>` + fmt.Sprint(port) + `</integer>
        <key>HTTPSEnable</key><integer>1</integer>
        <key>HTTPSProxy</key><string>` + safeHost + `</string>
        <key>HTTPSPort</key><integer>` + fmt.Sprint(port) + `</integer>
        <key>ProxyAutoConfigEnable</key><integer>0</integer>
        <key>ExceptionsList</key>
        <array><string>127.0.0.1</string><string>localhost</string></array>
      </dict>
    </dict>
  </array>
  <key>PayloadDescription</key><string>安装后即可用 WProxyman 抓包（证书 + 代理一次性配置）</string>
  <key>PayloadDisplayName</key><string>WProxyman</string>
  <key>PayloadIdentifier</key><string>com.wproxyman.profile</string>
  <key>PayloadOrganization</key><string>WProxyman</string>
  <key>PayloadRemovalDisallowed</key><false/>
  <key>PayloadType</key><string>Configuration</string>
  <key>PayloadUUID</key><string>` + profileUUID + `</string>
  <key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>
`
}

// xmlEscape 只处理几个必要字符：描述文件里 host 是 IP/主机名，正常不需要转义。
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
