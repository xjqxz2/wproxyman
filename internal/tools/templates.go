// Package tools 工具模板（Templates）的实现。
// templates.go 提供预设的脚本模板和网络条件配置文件，供前端 UI 使用。
//
// 预设脚本模板：
//   - Hello World：基本的日志输出示
//   - Modify Request Header：修改请求头的示例
//   - Modify Request Body：替换请求体的示例
//   - Modify Response：修改响应头和响应体的示例
//
// 预设网络配置文件：
//   - 模拟从 56k 调制解调器到 LTE 的各种网络条件
//   - 每个配置包含下载/上传带宽（字节/秒）和延迟（毫秒）
package tools

// DefaultScriptTemplates 返回预设的脚本示例（对应 Proxyman 的内置模板）。
// 这些模板供脚本编辑器的"新建脚本"功能使用，提供常用用例的起点。
func DefaultScriptTemplates() []ScriptEntry {
	return []ScriptEntry{
		{
			ID:      "tpl-hello",
			Name:    "Hello World",
			Enabled: false,
			Match:   URLMatch{Pattern: "", IsRegex: false, Method: ""},
			Code: `// Hello World script
// This script runs for every request that matches the rules above.
function onRequest(context) {
    console.log("Hello, " + context.request.url);
    return true;
}

function onResponse(context) {
    // console.log(context.response.status);
    return true;
}
`,
		},
		{
			ID:      "tpl-header",
			Name:    "Modify Request Header",
			Enabled: false,
			Match:   URLMatch{Pattern: "", IsRegex: false, Method: ""},
			Code: `// Modify Request Header
// Adds / replaces a request header before it is sent upstream.
function onRequest(context) {
    context.request.setHeader("X-Modified-By", "WProxyman Script");
    context.request.removeHeader("X-Remove-Me");
    return true;
}
`,
		},
		{
			ID:      "tpl-body",
			Name:    "Modify Request Body",
			Enabled: false,
			Match:   URLMatch{Pattern: "", IsRegex: false, Method: ""},
			Code: `// Modify Request Body
// Replaces the request body text.
function onRequest(context) {
    if (context.request.hasBody) {
        context.request.body = context.request.body.replace("old-value", "new-value");
    }
    return true;
}
`,
		},
		{
			ID:      "tpl-resp",
			Name:    "Modify Response",
			Enabled: false,
			Match:   URLMatch{Pattern: "", IsRegex: false, Method: ""},
			Code: `// Modify Response
// Rewrites part of the response body and tweaks a header.
function onResponse(context) {
    if (context.response.status === 200) {
        context.response.setHeader("X-Debug", "yes");
        context.response.body = context.response.body + "\n<!-- injected by script -->";
    }
    return true;
}
`,
		},
	}
}

// DefaultNetworkProfiles 返回预设的网络条件配置文件（对应 Proxyman 的预设）。
// 带宽值已转换为字节/秒（Bps）。
// 例如：1 Mbps = 1,000,000 bps / 8 = 125,000 Bps
func DefaultNetworkProfiles() []NetworkProfile {
	return []NetworkProfile{
		{Name: "Off", DownloadBps: 0, UploadBps: 0, LatencyMs: 0},           // 关闭限速
		{Name: "56 kbps", DownloadBps: 56 * 1024 / 8, UploadBps: 33 * 1024 / 8, LatencyMs: 120},     // 拨号上网
		{Name: "256 kbps", DownloadBps: 256 * 1024 / 8, UploadBps: 128 * 1024 / 8, LatencyMs: 100},  // 低速宽带
		{Name: "1 Mbps", DownloadBps: 1_000_000 / 8, UploadBps: 500_000 / 8, LatencyMs: 60},         // 基础宽带
		{Name: "3G", DownloadBps: 1_600_000 / 8, UploadBps: 768_000 / 8, LatencyMs: 150},            // 3G 移动网络
		{Name: "4G", DownloadBps: 9_000_000 / 8, UploadBps: 3_000_000 / 8, LatencyMs: 40},           // 4G 移动网络
		{Name: "Fast 4G", DownloadBps: 22_000_000 / 8, UploadBps: 8_000_000 / 8, LatencyMs: 30},     // 高速 4G
		{Name: "LTE", DownloadBps: 40_000_000 / 8, UploadBps: 20_000_000 / 8, LatencyMs: 20},        // LTE
		{Name: "Edge", DownloadBps: 240_000 / 8, UploadBps: 200_000 / 8, LatencyMs: 400},            // EDGE（2.5G）
		{Name: "2G", DownloadBps: 50_000 / 8, UploadBps: 20_000 / 8, LatencyMs: 800},                // 2G 移动网络
	}
}
