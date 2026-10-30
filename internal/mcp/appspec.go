package mcp

// AppToolSpecs 是 WProxyman 暴露给 MCP 的全部能力清单。
//
// 覆盖 App 的每个导出方法，例外只有两个（都在测试里被断言，防止漏写）：
//   - Cleanup：生命周期方法，注册表里有硬性拒绝名单；
//   - GetCACertPEM：返回一大段 PEM，对模型无意义，改为以资源形式提供。
//
// 描述是写给模型看的：只读方法说明"能查到什么"，写方法说明"会改什么"，
// 危险方法必须写明副作用（清空数据、改系统信任库、读写任意路径）。
// 权限档默认只放行 read，write/dangerous 需要用户在设置里显式开启。
func AppToolSpecs() []ToolSpec {
	return []ToolSpec{
		// ---- 代理（proxy）----
		{Name: "get_proxy_status", Method: "IsProxyRunning", Group: "proxy", Tier: TierRead,
			Description: "代理当前是否在运行。"},
		{Name: "get_proxy_port", Method: "GetProxyPort", Group: "proxy", Tier: TierRead,
			Description: "当前代理端口（默认固定 10086；未运行时返回配置端口）。"},
		{Name: "get_proxy_error", Method: "GetProxyError", Group: "proxy", Tier: TierRead,
			Description: "最近一次启动代理失败的原因（如端口被占用）。空串表示无错误。"},
		{Name: "start_proxy", Method: "StartProxy", Group: "proxy", Tier: TierWrite, Params: []string{"port"},
			Description: "启动抓包代理。port=0 表示使用设置里的端口；端口被占用会失败（不会自动换端口）。"},
		{Name: "stop_proxy", Method: "StopProxy", Group: "proxy", Tier: TierWrite,
			Description: "停止抓包代理，并自动还原系统代理（避免整机断网）。"},
		{Name: "get_system_proxy_enabled", Method: "GetSystemProxyEnabled", Group: "proxy", Tier: TierRead,
			Description: "系统代理当前是否已开启。"},
		{Name: "set_system_proxy_enabled", Method: "SetSystemProxyEnabled", Group: "proxy", Tier: TierWrite, Params: []string{"enabled"},
			Description: "开启/关闭系统代理（指向本机代理端口）。代理未运行时不允许开启。关闭时不改用户意图。"},
		{Name: "get_lan_ips", Method: "GetLANIPs", Group: "proxy", Tier: TierRead,
			Description: "本机局域网 IPv4 地址列表（手机/模拟器配置代理时用）。"},

		// ---- SSL 解密与证书（ssl）----
		{Name: "get_ssl_state", Method: "GetSSLProxyState", Group: "ssl", Tier: TierRead,
			Description: "SSL 解密状态：默认开关 + 各域名的单独开关。"},
		{Name: "set_ssl_enabled", Method: "SetSSLProxyEnabled", Group: "ssl", Tier: TierWrite, Params: []string{"host", "enabled"},
			Description: "开关某个域名的 HTTPS 解密（host 传空串表示全局默认）。"},
		{Name: "is_certificate_installed", Method: "IsCertificateInstalled", Group: "ssl", Tier: TierRead,
			Description: "本地 CA 是否已被系统信任（未信任时 HTTPS 会降级为隧道直通）。"},
		{Name: "install_certificate", Method: "InstallCertificate", Group: "ssl", Tier: TierDangerous,
			Description: "把本地 CA 安装进系统信任库。会修改系统证书存储，macOS/Linux 可能弹出授权。"},
		{Name: "remove_certificate", Method: "RemoveCertificate", Group: "ssl", Tier: TierDangerous,
			Description: "从系统信任库移除本地 CA。移除后 HTTPS 解密会失效。"},

		// ---- 设置（settings）----
		{Name: "get_settings", Method: "GetSettings", Group: "settings", Tier: TierRead,
			Description: "读取全部设置（端口、AllowLAN、SSL 默认、流式解析规则等）。"},
		{Name: "update_settings", Method: "SetSettings", Group: "settings", Tier: TierWrite, Params: []string{"settings"},
			Description: "保存设置。注意：改端口会重启代理；端口被占用会失败并回滚。字段需完整给出（拿 get_settings 的结果改）。"},
		{Name: "get_tool_config", Method: "GetToolConfig", Group: "settings", Tier: TierRead,
			Description: "读取全部调试工具配置（Map Local / Map Remote / Block List / Rules / 脚本 / 限速等）。"},
		{Name: "apply_tool_config", Method: "ApplyToolConfig", Group: "settings", Tier: TierWrite, Params: []string{"config"},
			Description: "整体替换工具配置并立即生效（先 get_tool_config 拿到现有配置，改完再提交）。"},
		{Name: "get_script_templates", Method: "GetScriptTemplates", Group: "settings", Tier: TierRead,
			Description: "脚本工具的内置模板示例。"},
		{Name: "get_network_profiles", Method: "GetNetworkProfiles", Group: "settings", Tier: TierRead,
			Description: "弱网模拟的内置预设档位。"},
		{Name: "set_upstream_insecure", Method: "SetUpstreamInsecure", Group: "settings", Tier: TierDangerous, Params: []string{"insecure"},
			Description: "关闭/开启上游 TLS 证书校验。开启（不校验）会降低安全性，仅用于自签环境的排查。"},

		// ---- 流量（flows）----
		{Name: "get_flow_count", Method: "GetFlowCount", Group: "flows", Tier: TierRead,
			Description: "当前已捕获的流量条数。"},
		{Name: "get_flow_raw", Method: "GetFlow", Group: "flows", Tier: TierRead, Params: []string{"id"},
			Description: "按 id 返回单条流量（含完整正文）。正文很大时请改用 get_flow_body 分段读取。"},
		{Name: "get_flows_by_domain", Method: "GetFlowsByDomain", Group: "flows", Tier: TierRead,
			Description: "按域名分组的流量概览（每组的条数等）。"},
		{Name: "set_flow_pinned", Method: "SetFlowPinned", Group: "flows", Tier: TierWrite, Params: []string{"id", "pinned"},
			Description: "置顶/取消置顶一条流量。"},
		{Name: "delete_flow", Method: "DeleteFlow", Group: "flows", Tier: TierDangerous, Params: []string{"id"},
			Description: "删除一条流量。不可撤销。"},
		{Name: "clear_flows", Method: "ClearFlows", Group: "flows", Tier: TierDangerous,
			Description: "清空全部已捕获流量。不可撤销。"},

		// ---- 工具动作（tools）----
		{Name: "resolve_breakpoint", Method: "ResolveBreakpoint", Group: "tools", Tier: TierWrite, Params: []string{"flowId", "modified"},
			Description: "对处于断点暂停状态的请求做出决定：放行、修改后放行或取消（modified 传修改后的 flow）。"},
		{Name: "send_request", Method: "SendRequest", Group: "tools", Tier: TierWrite, Params: []string{"flow"},
			Description: "把构造好的请求真实发出去（Compose / Repeater 用）。会产生真实网络请求。"},

		// ---- 会话与导入导出（sessions）----
		{Name: "save_session", Method: "SaveSession", Group: "sessions", Tier: TierDangerous, Params: []string{"path"},
			Description: "把全部流量保存为 .wpx 会话文件到指定路径（任意路径写入）。"},
		{Name: "open_session", Method: "OpenSession", Group: "sessions", Tier: TierDangerous, Params: []string{"path"},
			Description: "打开 .wpx 会话文件并替换当前流量列表（任意路径读取）。"},
		{Name: "import_session", Method: "ImportSession", Group: "sessions", Tier: TierDangerous, Params: []string{"path"},
			Description: "导入会话文件并追加到当前流量列表（任意路径读取）。"},
		{Name: "export_har", Method: "ExportHAR", Group: "sessions", Tier: TierDangerous, Params: []string{"path", "ids"},
			Description: "把指定流量导出为 HAR 文件（任意路径写入）。ids 为空表示全部。"},
		{Name: "import_har", Method: "ImportHAR", Group: "sessions", Tier: TierDangerous, Params: []string{"path"},
			Description: "导入 HAR 文件（任意路径读取）。"},
		{Name: "import_curl_text", Method: "ImportCurlText", Group: "sessions", Tier: TierWrite, Params: []string{"text"},
			Description: "把一段 cURL 命令解析成一条流量（不发送请求）。"},
		{Name: "import_curl_from_file", Method: "ImportCurlFromFile", Group: "sessions", Tier: TierDangerous, Params: []string{"path"},
			Description: "从文件读取 cURL 命令并解析成流量（任意路径读取）。"},
		{Name: "save_flow_content", Method: "SaveFlowContent", Group: "sessions", Tier: TierDangerous, Params: []string{"flowId", "side"},
			Description: "把某条流量的请求或响应正文保存到文件（side 取 request/response；会弹保存对话框）。"},

		// ---- 代码生成（codegen）----
		{Name: "generate_curl", Method: "GenerateCurl", Group: "codegen", Tier: TierRead, Params: []string{"flowId"},
			Description: "为一条流量生成 cURL 命令。"},
		{Name: "generate_node_fetch", Method: "GenerateNodeFetch", Group: "codegen", Tier: TierRead, Params: []string{"flowId"},
			Description: "为一条流量生成 Node fetch 代码。"},
		{Name: "generate_postman", Method: "GeneratePostmanCollection", Group: "codegen", Tier: TierRead, Params: []string{"ids"},
			Description: "把若干流量导出为 Postman collection（JSON）。"},
		{Name: "get_request_body", Method: "GetRequestBody", Group: "codegen", Tier: TierRead, Params: []string{"flowId"},
			Description: "取某条流量的请求正文（base64）。正文大时请用 get_flow_body 分段读。"},
		{Name: "get_response_body", Method: "GetResponseBody", Group: "codegen", Tier: TierRead, Params: []string{"flowId"},
			Description: "取某条流量的响应正文（base64）。正文大时请用 get_flow_body 分段读。"},
	}
}
