// app_mcp.go —— MCP 能力装配：把反射工具（覆盖 App 全部方法）与"包装工具"
// （为模型重新设计过的查询接口）组装成一份注册表。
//
// 为什么需要包装工具：反射只能原样转发。而 get_flows_raw 会把**全部流量连同
// 正文**一次性返回 —— 对模型来说这是上下文炸弹。所以对外提供 list_flows：
// 过滤 + 分页 + 字段裁剪（复用 internal/flowquery），原始方法不再暴露。
package main

import (
	"encoding/json"
	"strings"

	"wproxyman/internal/flowquery"
	"wproxyman/internal/mcp"
	"wproxyman/internal/models"
)

// buildMCPRegistry 组装 MCP 工具表：反射工具 + 包装工具。
// 返回的 warnings 会透出"元数据与真实签名不符"之类的信息，便于日志与测试发现。
func (a *App) buildMCPRegistry() (*mcp.Registry, []string, error) {
	reg, warns, err := mcp.NewRegistry(a, mcp.AppToolSpecs())
	if err != nil {
		return nil, warns, err
	}
	if err := reg.AddHandler(
		mcp.ToolSpec{
			Name:   "list_flows",
			Method: "list_flows", // 包装工具，不对应 App 方法
			Group:  "flows",
			Tier:   mcp.TierRead,
			Description: "按条件查询已捕获的流量，返回摘要列表（默认不含正文）。" +
				"支持主机/方法/状态/关键字筛选与分页；正文请用 get_flow_body 按需分段读取。" +
				"返回里的 hasMore 表示还有更多，可继续翻页。",
		},
		[]mcp.Param{
			{Name: "host", Type: "string", Description: "只看该主机（不区分大小写，精确匹配）"},
			{Name: "method", Type: "string", Description: "只看该方法，如 GET / POST"},
			{Name: "status", Type: "string", Description: "状态筛选：精确值 200，或分类 2xx/4xx/5xx，或 failed（失败）"},
			{Name: "search", Type: "string", Description: "关键字，在 URL/主机/路径/方法/状态码里做子串匹配"},
			{Name: "onlyFailed", Type: "boolean", Description: "只看失败请求（有错误或状态码 >= 400）"},
			{Name: "onlyStream", Type: "boolean", Description: "只看流式响应（SSE / NDJSON）"},
			{Name: "onlyWebSocket", Type: "boolean", Description: "只看 WebSocket"},
			{Name: "sinceMs", Type: "integer", Description: "只看该时间戳（unix 毫秒）之后开始的请求"},
			{Name: "untilMs", Type: "integer", Description: "只看该时间戳（unix 毫秒）之前开始的请求"},
			{Name: "limit", Type: "integer", Description: "返回条数，默认 50，最大 500", Default: 50},
			{Name: "offset", Type: "integer", Description: "跳过前 N 条（翻页用）"},
			{Name: "fields", Type: "array", Items: "string",
				Description: "额外要返回的字段：fullUrl/scheme/query/httpVersion/tls/clientAddr/requestSize/" +
					"requestMimeType/responseMimeType/requestHeaders/responseHeaders/truncated/streamKind/" +
					"firstByteAt/eventCount/pinned/saved（正文永远不返回）"},
		},
		func(args json.RawMessage) (any, *mcp.Error) { return a.mcpListFlows(args) },
	); err != nil {
		return nil, warns, err
	}

	if err := reg.AddHandler(
		mcp.ToolSpec{
			Name:   "list_capabilities",
			Method: "list_capabilities",
			Group:  "meta",
			Tier:   mcp.TierRead,
			Description: "列出本服务能做什么：按分组的工具名，以及各权限档（read/write/dangerous）" +
				"当前是否被允许调用。不确定该用哪个工具时先调这个。",
		},
		nil,
		func(json.RawMessage) (any, *mcp.Error) {
			groups := reg.Groups()
			byTier := map[string][]string{}
			for _, spec := range reg.Specs() {
				byTier[string(spec.Tier)] = append(byTier[string(spec.Tier)], spec.Name)
			}
			return map[string]any{
				"groups": groups,
				"tiers":  byTier,
				"note": "默认只允许 read；write/dangerous 需要在 WProxyman 设置 → MCP 里显式开启，" +
					"未开启时调用会被拒绝并说明原因。",
			}, nil
		},
	); err != nil {
		return nil, warns, err
	}

	if err := reg.AddHandler(
		mcp.ToolSpec{
			Name:   "get_flow_body",
			Method: "get_flow_body",
			Group:  "flows",
			Tier:   mcp.TierRead,
			Description: "按需分段读取一条流量的请求或响应正文（默认 32 KiB，最多 256 KiB）。" +
				"文本直接给 text；二进制给 base64。返回 hasMore 表示可带更大的 offset 继续读；" +
				"truncated 表示这段正文在保存时已被正文上限截断（读到结尾也不代表拿到全部）。",
		},
		[]mcp.Param{
			{Name: "flowId", Type: "string", Description: "流量 id（取自 list_flows）", Required: true},
			{Name: "side", Type: "string", Description: "读哪一侧", Required: true, Enum: []string{"request", "response"}},
			{Name: "offset", Type: "integer", Description: "起始字节偏移（翻页用）", Default: 0},
			{Name: "limit", Type: "integer", Description: "本次读取字节数，默认 32768，最大 262144",
				Default: flowquery.DefaultBodyLimit},
		},
		func(args json.RawMessage) (any, *mcp.Error) { return a.mcpGetFlowBody(args) },
	); err != nil {
		return nil, warns, err
	}

	return reg, warns, nil
}

// listFlowsArgs 是 list_flows 的入参。
type listFlowsArgs struct {
	Host          string   `json:"host"`
	Method        string   `json:"method"`
	Status        string   `json:"status"`
	Search        string   `json:"search"`
	OnlyFailed    bool     `json:"onlyFailed"`
	OnlyStream    bool     `json:"onlyStream"`
	OnlyWebSocket bool     `json:"onlyWebSocket"`
	SinceMs       int64    `json:"sinceMs"`
	UntilMs       int64    `json:"untilMs"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
	Fields        []string `json:"fields"`
}

// mcpListFlows 执行一次流量查询。
func (a *App) mcpListFlows(args json.RawMessage) (any, *mcp.Error) {
	var in listFlowsArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, mcp.Errorf(mcp.CodeInvalidParams, "参数解析失败：%v", err)
		}
	}
	// 字段名先校验：拼错的字段应当报错，而不是静默少返回数据。
	for _, f := range in.Fields {
		if !flowquery.KnownField(f) {
			return nil, mcp.Errorf(mcp.CodeInvalidParams,
				"不支持的字段 %q（正文不可请求；可用字段见 list_capabilities 或工具描述）", f)
		}
	}

	// 查询期间不能长时间持锁：先快照，再在锁外做过滤/投影。
	a.mu.RLock()
	snapshot := make([]*models.Flow, len(a.flows))
	copy(snapshot, a.flows)
	a.mu.RUnlock()

	return flowquery.Query(snapshot, flowquery.Filter{
		Host:       in.Host,
		Method:     in.Method,
		Status:     in.Status,
		Search:     in.Search,
		OnlyFailed: in.OnlyFailed,
		OnlyStream: in.OnlyStream,
		OnlyWS:     in.OnlyWebSocket,
		SinceMs:    in.SinceMs,
		UntilMs:    in.UntilMs,
	}, flowquery.Page{Limit: in.Limit, Offset: in.Offset}, in.Fields), nil
}

// getFlowBodyArgs 是 get_flow_body 的入参。
type getFlowBodyArgs struct {
	FlowID string `json:"flowId"`
	Side   string `json:"side"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// mcpGetFlowBody 返回正文的一个分片。
func (a *App) mcpGetFlowBody(args json.RawMessage) (any, *mcp.Error) {
	var in getFlowBodyArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, mcp.Errorf(mcp.CodeInvalidParams, "参数解析失败：%v", err)
	}
	if in.FlowID == "" {
		return nil, mcp.Errorf(mcp.CodeInvalidParams, "缺少参数 flowId")
	}
	side := strings.ToLower(strings.TrimSpace(in.Side))
	if side != "request" && side != "response" {
		return nil, mcp.Errorf(mcp.CodeInvalidParams, "side 只能是 request 或 response，收到 %q", in.Side)
	}

	a.mu.RLock()
	fl := a.flowIdx[in.FlowID]
	var body []byte
	var truncated bool
	var mime string
	if fl != nil {
		if side == "request" {
			body, truncated, mime = fl.RequestBody, fl.RequestTruncated, fl.RequestMimeType
		} else {
			body, truncated, mime = fl.ResponseBody, fl.ResponseTruncated, fl.ResponseMimeType
		}
	}
	a.mu.RUnlock()
	if fl == nil {
		return nil, mcp.Errorf(mcp.CodeInvalidParams, "找不到流量 %s（可能已被清空或删除）", in.FlowID)
	}

	chunk := flowquery.SliceBody(body, in.Offset, in.Limit)
	// 截断标记只有调用方知道（SliceBody 看不到 Flow 的标记）。
	chunk.Truncated = truncated
	return map[string]any{
		"flowId":    in.FlowID,
		"side":      side,
		"mimeType":  mime,
		"chunk":     chunk,
		"total":     chunk.Total,
		"hasMore":   chunk.HasMore,
		"truncated": truncated,
	}, nil
}
